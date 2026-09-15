package doctor

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const groupEnv = "Environment & Tooling"

func runEnvChecks() []CheckResult {
	return []CheckResult{
		checkExoVersion(),
		checkCredentialHelper(),
		checkGitAnnex(),
		checkS5cmd(),
		checkAPIURL(),
		checkEnvVars(),
		checkClockSkew(),
	}
}

func checkExoVersion() CheckResult {
	id := "env.exo-version"
	exe, err := os.Executable()
	if err != nil {
		return warn(id, groupEnv, "exo binary not identifiable", err.Error(), "Ensure exo is installed correctly")
	}
	out, err := exec.Command(exe, "version").Output()
	if err != nil {
		return warn(id, groupEnv, "Could not determine exo version", err.Error(), "Reinstall exo")
	}
	ver := strings.TrimSpace(string(out))
	return pass(id, groupEnv, fmt.Sprintf("exo version: %s", ver))
}

func checkCredentialHelper() CheckResult {
	id := "env.credential-helper"
	bin := os.Getenv("EXO_CREDENTIAL_HELPER_BIN")
	if bin == "" {
		bin = "exo-credential-helper"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		detail := fmt.Sprintf("binary %q not found in PATH", bin)
		rem := "Install exo-credential-helper and ensure it is on your PATH, or set EXO_CREDENTIAL_HELPER_BIN"
		if os.Getenv("EXO_CREDENTIAL_HELPER_BIN") != "" {
			detail = fmt.Sprintf("EXO_CREDENTIAL_HELPER_BIN=%q not executable", bin)
		}
		return fail(id, groupEnv, "exo-credential-helper not resolvable", detail, rem)
	}
	out, err := exec.Command(path, "--version").Output()
	ver := strings.TrimSpace(string(out))
	if err != nil || ver == "" {
		ver = "unknown"
	}
	return pass(id, groupEnv, fmt.Sprintf("exo-credential-helper: %s (%s)", ver, path))
}

func checkGitAnnex() CheckResult {
	id := "env.git-annex"
	path, err := exec.LookPath("git-annex")
	if err != nil {
		return fail(id, groupEnv, "git-annex not found in PATH",
			"git-annex is required for data transfer",
			"Install git-annex: https://git-annex.branchable.com/install/")
	}
	out, err := exec.Command(path, "version", "--raw").Output()
	ver := strings.TrimSpace(string(out))
	if err != nil || ver == "" {
		ver = "unknown"
	}
	return pass(id, groupEnv, fmt.Sprintf("git-annex: %s", ver))
}

func checkS5cmd() CheckResult {
	id := "env.s5cmd"
	path, err := exec.LookPath("s5cmd")
	if err != nil {
		return warn(id, groupEnv, "s5cmd not found in PATH",
			"s5cmd is required for S3 data transfer via git-annex-remote-s5cmd",
			"Install s5cmd: https://github.com/peak/s5cmd")
	}
	out, err := exec.Command(path, "version").Output()
	ver := strings.TrimSpace(string(out))
	if err != nil || ver == "" {
		ver = "unknown"
	}
	// Take first line
	if idx := strings.IndexByte(ver, '\n'); idx >= 0 {
		ver = ver[:idx]
	}
	return pass(id, groupEnv, fmt.Sprintf("s5cmd: %s", ver))
}

func checkAPIURL() CheckResult {
	id := "env.api-url"
	apiURL := os.Getenv("EXOHUB_API_URL")
	if apiURL == "" {
		return pass(id, groupEnv, "EXOHUB_API_URL not set (using built-in default)")
	}
	if !strings.Contains(apiURL, "/api") {
		return fail(id, groupEnv,
			fmt.Sprintf("EXOHUB_API_URL=%q does not contain /api path", apiURL),
			"The API URL must include the /api path segment",
			"Set EXOHUB_API_URL to e.g. https://your-exohub-server.example.com/api")
	}
	return pass(id, groupEnv, fmt.Sprintf("EXOHUB_API_URL: %s", apiURL))
}

func checkEnvVars() CheckResult {
	id := "env.vars"
	var info []string
	if v := os.Getenv("EXO_CONFIG_DIR"); v != "" {
		info = append(info, fmt.Sprintf("EXO_CONFIG_DIR=%s", v))
	}
	if v := os.Getenv("EXOHUB_AWS_PROFILE"); v != "" {
		info = append(info, fmt.Sprintf("EXOHUB_AWS_PROFILE=%s", v))
	} else {
		info = append(info, "EXOHUB_AWS_PROFILE=exohub (default)")
	}
	if v := os.Getenv("AWS_PROFILE"); v != "" {
		info = append(info, fmt.Sprintf("AWS_PROFILE=%s", v))
	}
	return pass(id, groupEnv, strings.Join(info, "; "))
}

func checkClockSkew() CheckResult {
	id := "env.clock-skew"
	const skewThreshold = 5 * time.Minute

	client := &http.Client{Timeout: 5 * time.Second}
	var resp *http.Response
	var localBefore, localAfter time.Time
	var err error

	// Try to get a trusted time from the exohub API Date header.
	if apiBase := defaults.APIBase(); apiBase != "" {
		probeURL := strings.TrimRight(apiBase, "/") + "/healthz"
		var req *http.Request
		req, err = http.NewRequest(http.MethodHead, probeURL, nil)
		if err == nil {
			localBefore = time.Now()
			resp, err = client.Do(req)
			localAfter = time.Now()
		}
	}

	// Fall back to a well-known AWS endpoint for the Date header.
	if resp == nil {
		var req2 *http.Request
		req2, err = http.NewRequest(http.MethodHead, "https://s3.amazonaws.com", nil)
		if err != nil {
			return skip(id, groupEnv, "Clock skew check skipped (could not build request)")
		}
		localBefore = time.Now()
		resp, err = client.Do(req2)
		localAfter = time.Now()
		if err != nil {
			return skip(id, groupEnv, "Clock skew check skipped (no reachable time source)")
		}
	}
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	dateHdr := ""
	if resp != nil {
		dateHdr = resp.Header.Get("Date")
	}
	if dateHdr == "" {
		return skip(id, groupEnv, "Clock skew check skipped (no Date header in response)")
	}

	serverTime, err := http.ParseTime(dateHdr)
	if err != nil {
		return skip(id, groupEnv, "Clock skew check skipped (could not parse Date header)")
	}
	localTime := localBefore.Add(localAfter.Sub(localBefore) / 2)
	skew := localTime.Sub(serverTime)
	if skew < 0 {
		skew = -skew
	}

	if skew > skewThreshold {
		return warn(id, groupEnv,
			fmt.Sprintf("Clock skew: %s (threshold: %s)", skew.Round(time.Second), skewThreshold),
			"Large clock skew breaks SigV4 request signing and JWT validation",
			"Sync your system clock (e.g. timedatectl set-ntp true)")
	}
	return pass(id, groupEnv, fmt.Sprintf("Clock skew: %s (within threshold)", skew.Round(time.Millisecond)))
}
