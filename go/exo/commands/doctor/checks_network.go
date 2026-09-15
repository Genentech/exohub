package doctor

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/credhelper"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const groupNetwork = "Network Reachability"

const probeTimeout = 5 * time.Second

func runNetworkChecks() []CheckResult {
	apiURL := defaults.APIBase()
	region := os.Getenv("EXOHUB_GRANTS_REGION")
	if region == "" {
		region = "us-west-2"
	}

	// Derive OIDC issuer: prefer EXO_OIDC_ISSUER, then the "iss" claim from the
	// stored JWT, then fall back to a well-known OIDC IdP URL.
	issuer, issuerSource := resolveOIDCIssuer()

	return []CheckResult{
		probeAPI(apiURL),
		probeS3(region),
		probeS3Control(region),
		probeOIDCIssuer(issuer, issuerSource),
	}
}

// resolveOIDCIssuer returns the OIDC issuer URL to probe and a source label
// that identifies how the issuer was resolved (used to annotate probe output).
// Precedence: EXO_OIDC_ISSUER env → stored JWT "iss" claim → empty (skip probe).
// Source values: "env" | "JWT iss claim".
func resolveOIDCIssuer() (string, string) {
	if v := os.Getenv("EXO_OIDC_ISSUER"); v != "" {
		return v, "env"
	}
	// Try to read the stored token and extract the "iss" claim.
	if token, _, err := loadDoctorToken(); err == nil && token != nil && token.AccessToken != "" {
		if claims, err := decodeJWTClaims(token.AccessToken); err == nil && claims.Iss != "" {
			return claims.Iss, "JWT iss claim"
		}
	}
	// No issuer configured — skip the OIDC probe.
	return "", ""
}

func probeAPI(apiURL string) CheckResult {
	id := "net.api"
	if apiURL == "" {
		return skip(id, groupNetwork, "API probe skipped: no API endpoint configured (set EXOHUB_API_URL)")
	}
	// Use the /api/ base with GET: HEAD returns 405 but GET returns 200.
	probeURL := strings.TrimRight(apiURL, "/") + "/"
	status, err := httpProbeGET(probeURL)
	if err != nil {
		// Could be no VPN — check if AWS is reachable to distinguish.
		return fail(id, groupNetwork,
			fmt.Sprintf("exohub API unreachable: %s", apiURL),
			Redact(err.Error()),
			"Check VPN connection. The API requires VPN access.")
	}
	if status >= 500 {
		return warn(id, groupNetwork,
			fmt.Sprintf("exohub API returned %d (server error)", status),
			fmt.Sprintf("URL: %s", probeURL),
			"The API may be degraded. Try again shortly.")
	}
	return pass(id, groupNetwork, fmt.Sprintf("exohub API reachable (HTTP %d)", status))
}

func probeS3(region string) CheckResult {
	id := "net.s3"
	url := fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	status, err := httpProbe(url)
	if err != nil {
		return fail(id, groupNetwork,
			fmt.Sprintf("AWS S3 endpoint unreachable: %s", url),
			Redact(err.Error()),
			"No AWS egress — git-annex data transfer will fail. Check firewall/proxy.")
	}
	// S3 returns 403 for anonymous HEAD on root, which is fine.
	if status == 403 || (status >= 200 && status < 500) {
		return pass(id, groupNetwork, fmt.Sprintf("AWS S3 (%s) reachable (HTTP %d)", region, status))
	}
	return warn(id, groupNetwork,
		fmt.Sprintf("AWS S3 probe returned unexpected status %d", status),
		fmt.Sprintf("URL: %s", url),
		"S3 endpoint may be proxied or firewalled")
}

func probeS3Control(region string) CheckResult {
	id := "net.s3control"
	// The AWS S3 Control API for Access Grants uses an account-scoped endpoint:
	// https://<accountId>.s3-control.<region>.amazonaws.com
	// The generic s3-control.<region>.amazonaws.com endpoint is not used by the SDK
	// and will return 403/400 even when Access Grants work correctly.
	// We probe the account-scoped endpoint so the result reflects real reachability.
	accountID := os.Getenv("EXOHUB_GRANTS_ACCOUNT_ID")
	if accountID == "" {
		accountID = credhelper.DefaultAccountID
	}
	if accountID == "" {
		return skip(id, groupNetwork, "EXOHUB_GRANTS_ACCOUNT_ID not set — S3 Control probe skipped")
	}
	url := fmt.Sprintf("https://%s.s3-control.%s.amazonaws.com", accountID, region)
	status, err := httpProbe(url)
	if err != nil {
		return fail(id, groupNetwork,
			fmt.Sprintf("AWS S3 Control endpoint unreachable: %s", url),
			Redact(err.Error()),
			"S3 Access Grants fast path requires S3 Control access. Check firewall/proxy.")
	}
	if status == 403 || (status >= 200 && status < 500) {
		return pass(id, groupNetwork, fmt.Sprintf("AWS S3 Control (%s) reachable (HTTP %d)", region, status))
	}
	return warn(id, groupNetwork,
		fmt.Sprintf("AWS S3 Control probe returned unexpected status %d", status),
		fmt.Sprintf("URL: %s", url),
		"S3 Control endpoint may be proxied or firewalled")
}

func probeOIDCIssuer(issuerURL string, source string) CheckResult {
	id := "net.oidc-issuer"
	// Probe the OIDC discovery endpoint with GET (HEAD is often rejected).
	probeURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	status, err := httpProbeGET(probeURL)
	if err != nil {
		// Try the base issuer URL as fallback.
		status, err = httpProbeGET(issuerURL)
		if err != nil {
			return warn(id, groupNetwork,
				fmt.Sprintf("OIDC issuer unreachable: %s", issuerURL),
				fmt.Sprintf("source=%s %s", source, Redact(err.Error())),
				"Token refresh may fail if the OIDC issuer is unreachable")
		}
	}
	if status >= 200 && status < 500 {
		return pass(id, groupNetwork, fmt.Sprintf("OIDC issuer reachable (HTTP %d, source=%s)", status, source))
	}
	return warn(id, groupNetwork,
		fmt.Sprintf("OIDC issuer probe returned %d (source=%s)", status, source),
		fmt.Sprintf("URL: %s", probeURL),
		"Token refresh may be affected")
}

// httpProbeGET performs a GET request with a short timeout and returns the status code.
func httpProbeGET(url string) (int, error) {
	return httpProbeMethod(http.MethodGet, url)
}

// httpProbe performs a HEAD request with a short timeout and returns the status code.
// Falls back to GET if HEAD is blocked (e.g., proxy returns transport error).
func httpProbe(url string) (int, error) {
	client := newProbeClient()
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "exo-doctor/1.0")
	resp, err := client.Do(req)
	if err != nil {
		// If HEAD is blocked by proxy, try GET
		return httpProbeMethod(http.MethodGet, url)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// httpProbeMethod performs an HTTP request with the given method.
func httpProbeMethod(method, url string) (int, error) {
	client := newProbeClient()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "exo-doctor/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

// newProbeClient creates an HTTP client with short timeout and redirect limit.
func newProbeClient() *http.Client {
	return &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
