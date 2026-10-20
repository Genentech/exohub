// Package whoami implements the `exo whoami` command: it reports who the
// user is logged in as, how long the session lasts, and which AWS
// credentials exo will use — reusing the JWT decoding shared with `exo
// doctor` (internal/auth).
package whoami

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"gopkg.in/ini.v1"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/Genentech/exohub/go/exo/commands/doctor"
	"github.com/Genentech/exohub/go/exo/commands/login"
	"github.com/Genentech/exohub/go/exo/configdir"
	"github.com/Genentech/exohub/go/exo/internal/auth"
)

var (
	flagJSON bool
	flagAWS  bool
)

// awsSTSTimeout bounds the live sts:GetCallerIdentity call made by --aws, so
// a black-holed network (VPN down, proxy misconfigured) can't hang the CLI
// or the MCP "whoami" tool indefinitely.
const awsSTSTimeout = 10 * time.Second

// Field is a single label/value line, used by the internal extension hook so
// build-specific overlays can add lines to both the text and JSON output
// without this package knowing about them.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// extraWhoamiFields is an internal-only extension hook: build-specific
// overlays set it from a //go:build internal init() (see
// commands/login/command_internal.go for the established pattern) to add
// extra identity lines to both the text and JSON output. Left nil in the
// OSS build.
var extraWhoamiFields func() []Field

// NewCommand returns the `exo whoami` cobra command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you're logged in as, session info, and AWS credentials",
		Long: `Show identity, session, organisation, and AWS credential info from the
stored login.

Never prints tokens, refresh tokens, or AWS secrets.`,
		RunE:         runWhoami,
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output JSON (for MCP/automation)")
	cmd.Flags().BoolVar(&flagAWS, "aws", false, "Also perform a live sts:GetCallerIdentity call")
	return cmd
}

func runWhoami(cmd *cobra.Command, args []string) error {
	result, err := Run(flagAWS)
	if err != nil {
		return err
	}

	if flagJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	printHuman(cmd.OutOrStdout(), result)
	return nil
}

// Run loads the stored login and builds the whoami Result, optionally
// performing a live sts:GetCallerIdentity call. Exported so other callers
// (e.g. the MCP "whoami" tool) can reuse it without shelling out.
func Run(includeAWS bool) (Result, error) {
	tokenFile, err := login.GetTokenFile()
	if err != nil {
		return Result{}, fmt.Errorf("failed to resolve token file: %w", err)
	}

	token, err := login.LoadToken(tokenFile)
	if err != nil || token == nil || token.AccessToken == "" {
		return Result{}, fmt.Errorf("not logged in, run 'exo login'")
	}

	claims, claimsErr := auth.DecodeJWTClaims(token.AccessToken)

	result := buildResult(token, claims)

	if claimsErr != nil || claims == nil || claims.Username() == "" {
		// The stored token isn't a decodable ID token (e.g. an opaque OAuth
		// access token from an IdP that doesn't expose claims), or it carries
		// no username. The user is still logged in, so this isn't an error —
		// just say explicitly why identity/organisation fields are missing.
		result.Identity.Note = "identity claims unavailable: the stored token is not a decodable ID token"
	}

	if includeAWS {
		account, arn, awsErr := fetchAWSIdentity(result.AWS.Profile)
		if awsErr != nil {
			result.AWS.Error = doctor.Redact(awsErr.Error())
		} else {
			result.AWS.Account = account
			result.AWS.Arn = arn
		}
	}

	if extraWhoamiFields != nil {
		result.Extra = extraWhoamiFields()
	}

	return result, nil
}

// identityInfo holds standard OIDC identity claims.
type identityInfo struct {
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Provider string `json:"provider,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	// Note explains why identity/organisation fields are empty when the
	// stored token isn't a decodable ID token (e.g. an opaque OAuth access
	// token). Empty when claims decoded normally.
	Note string `json:"note,omitempty"`
}

// sessionInfo holds token lifetime and refreshability.
type sessionInfo struct {
	ExpiresAt  string `json:"expires_at,omitempty"`
	ExpiresIn  string `json:"expires_in,omitempty"`
	CanRefresh bool   `json:"can_refresh"`
}

// organisationInfo holds org claims, printed only if present.
type organisationInfo struct {
	Department      string   `json:"department,omitempty"`
	CostCenter      string   `json:"cost_center,omitempty"`
	ManagerUsername string   `json:"manager_username,omitempty"`
	ExohubGroups    []string `json:"exohub_groups,omitempty"`
}

// awsInfo holds the AWS profile exo uses and, optionally, live identity info.
type awsInfo struct {
	Profile           string `json:"profile,omitempty"`
	CredentialsFile   string `json:"credentials_file,omitempty"`
	CredentialsExpire string `json:"credentials_expire,omitempty"`
	Account           string `json:"account,omitempty"`
	Arn               string `json:"arn,omitempty"`
	Error             string `json:"error,omitempty"`
}

// Result is the stable JSON shape for `exo whoami --json`, also used to
// render the human-readable text output.
type Result struct {
	Identity     identityInfo      `json:"identity"`
	Session      sessionInfo       `json:"session"`
	Organisation *organisationInfo `json:"organisation,omitempty"`
	AWS          awsInfo           `json:"aws"`
	Extra        []Field           `json:"extra,omitempty"`
}

func buildResult(token *oauth2.Token, claims *auth.JWTClaims) Result {
	result := Result{
		Identity: identityInfo{
			Provider: auth.ProviderName(),
		},
		Session: sessionInfo{
			CanRefresh: token.RefreshToken != "",
		},
	}

	if claims != nil {
		result.Identity.Username = claims.Username()
		result.Identity.Name = claims.Name
		result.Identity.Email = claims.Email
		result.Identity.Issuer = claims.Iss

		groups := auth.NormalizeGroups(claims)
		if claims.Department != "" || claims.CostCenter != "" || claims.ManagerUsername != "" || len(groups) > 0 {
			result.Organisation = &organisationInfo{
				Department:      claims.Department,
				CostCenter:      claims.CostCenter,
				ManagerUsername: claims.ManagerUsername,
				ExohubGroups:    groups,
			}
		}
	}

	expiry := token.Expiry
	if expiry.IsZero() && claims != nil && claims.Exp != 0 {
		expiry = time.Unix(claims.Exp, 0)
	}
	if !expiry.IsZero() {
		result.Session.ExpiresAt = expiry.Format(time.RFC3339)
		result.Session.ExpiresIn = relativeExpiry(expiry)
	}

	profile := auth.ExohubAWSProfile()
	result.AWS.Profile = profile
	if credPath, err := configdir.AWSCredentialsFile(); err == nil {
		result.AWS.CredentialsFile = credPath
		result.AWS.CredentialsExpire = readAWSCredentialExpiry(credPath, profile)
	}

	return result
}

// relativeExpiry renders exp as "expires in 42m" or "expired 3h ago".
func relativeExpiry(exp time.Time) string {
	d := time.Until(exp)
	if d >= 0 {
		return fmt.Sprintf("expires in %s", d.Round(time.Second))
	}
	return fmt.Sprintf("expired %s ago", (-d).Round(time.Second))
}

// readAWSCredentialExpiry looks for a conventional expiration key in the AWS
// shared credentials file for the given profile. exo's own credential writer
// (internal/auth.WriteAWSCredentials) does not currently persist one, so this
// returns "" when absent — callers should omit the field rather than show a
// fake value.
func readAWSCredentialExpiry(credPath, profile string) string {
	cfg, err := ini.Load(credPath)
	if err != nil {
		return ""
	}
	section, err := cfg.GetSection(profile)
	if err != nil {
		return ""
	}
	for _, key := range []string{"aws_session_expiration", "x_security_token_expires", "aws_expiration"} {
		if v := section.Key(key).String(); v != "" {
			return v
		}
	}
	return ""
}

// fetchAWSIdentity performs a live sts:GetCallerIdentity call for the given
// profile, mirroring commands/doctor's checkAWSIdentity.
func fetchAWSIdentity(profile string) (account, arn string, err error) {
	region := os.Getenv("EXOHUB_GRANTS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), awsSTSTimeout)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return "", "", fmt.Errorf("AWS profile %q does not resolve: %w", profile, err)
	}

	client := sts.NewFromConfig(cfg)
	identity, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("sts:GetCallerIdentity timed out for profile %q after %s: %w", profile, awsSTSTimeout, ctx.Err())
		}
		return "", "", fmt.Errorf("sts:GetCallerIdentity failed for profile %q: %w", profile, err)
	}

	if identity.Account != nil {
		account = *identity.Account
	}
	if identity.Arn != nil {
		arn = *identity.Arn
	}
	return account, arn, nil
}

func printHuman(w interface{ Write([]byte) (int, error) }, r Result) {
	fprintln := func(a ...any) { fmt.Fprintln(w, a...) }
	fprintf := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	fprintln("Identity")
	if r.Identity.Username != "" {
		fprintf("  Username: %s\n", r.Identity.Username)
	}
	if r.Identity.Name != "" {
		fprintf("  Name:     %s\n", r.Identity.Name)
	}
	if r.Identity.Email != "" {
		fprintf("  Email:    %s\n", r.Identity.Email)
	}
	if r.Identity.Provider != "" {
		if r.Identity.Issuer != "" {
			fprintf("  Provider: %s (issuer: %s)\n", r.Identity.Provider, r.Identity.Issuer)
		} else {
			fprintf("  Provider: %s\n", r.Identity.Provider)
		}
	}
	if r.Identity.Note != "" {
		fprintf("  Note:     %s\n", r.Identity.Note)
	}

	fprintln()
	fprintln("Session")
	if r.Session.ExpiresAt != "" {
		fprintf("  Expires:  %s (%s)\n", r.Session.ExpiresAt, r.Session.ExpiresIn)
	}
	if r.Session.CanRefresh {
		fprintln("  Refresh:  yes (can refresh silently)")
	} else {
		fprintln("  Refresh:  no")
	}

	if r.Organisation != nil {
		fprintln()
		fprintln("Organisation")
		if r.Organisation.Department != "" {
			fprintf("  Department:       %s\n", r.Organisation.Department)
		}
		if r.Organisation.CostCenter != "" {
			fprintf("  Cost center:      %s\n", r.Organisation.CostCenter)
		}
		if r.Organisation.ManagerUsername != "" {
			fprintf("  Manager:          %s\n", r.Organisation.ManagerUsername)
		}
		if len(r.Organisation.ExohubGroups) > 0 {
			fprintf("  ExoHub groups:    %s\n", strings.Join(r.Organisation.ExohubGroups, ", "))
		}
	}

	fprintln()
	fprintln("AWS")
	if r.AWS.Profile != "" {
		fprintf("  Profile:            %s\n", r.AWS.Profile)
	}
	if r.AWS.CredentialsFile != "" {
		fprintf("  Credentials file:   %s\n", r.AWS.CredentialsFile)
	}
	if r.AWS.CredentialsExpire != "" {
		fprintf("  Credentials expire: %s\n", r.AWS.CredentialsExpire)
	}
	if r.AWS.Account != "" {
		fprintf("  Account:            %s\n", r.AWS.Account)
	}
	if r.AWS.Arn != "" {
		fprintf("  Role ARN:           %s\n", r.AWS.Arn)
	}
	if r.AWS.Error != "" {
		fprintf("  Error:              %s\n", r.AWS.Error)
	}

	if len(r.Extra) > 0 {
		fprintln()
		for _, f := range r.Extra {
			fprintf("%s: %s\n", f.Label, f.Value)
		}
	}
}
