# Authentication

`exo login` authenticates you via an OIDC device flow and provisions base AWS
credentials using `sts:AssumeRoleWithWebIdentity`. Two build variants are
provided, selected at compile time via Go build tags.

## Configuration (generic/default build)

The generic OIDC provider requires the following to be configured. Values are
resolved in order: **environment variable → built-in default**.

| Setting | Environment variable | Default | Required |
|---|---|---|---|
| OIDC issuer / discovery URL | `EXO_OIDC_ISSUER` | — | ✅ |
| OIDC client ID | `EXO_OIDC_CLIENT_ID` | — | ✅ |
| OIDC scopes | `EXO_OIDC_SCOPES` | `openid profile` | no |
| OIDC audience | `EXO_OIDC_AUDIENCE` | — | no |
| STS role ARN | `EXO_AWS_ROLE_ARN` | — | ✅ |
| STS session duration (seconds) | `EXO_AWS_SESSION_DURATION` | `3600` | no |
| AWS region (for STS call) | `EXO_AWS_REGION` | `us-east-1` | no |

If any required value is missing, `exo login` prints a clear error with the
missing variable names and exits without starting the device flow.

### Example setup

```bash
export EXO_OIDC_ISSUER="https://your-idp.example.com"
export EXO_OIDC_CLIENT_ID="your-client-id"
export EXO_AWS_ROLE_ARN="arn:aws:iam::123456789012:role/ExoHubRole"
exo login
```

## Credential storage

After a successful login:

- The OAuth token (access + refresh token) is stored in
  `~/.config/exo/credentials/token.json` (or `$EXO_CONFIG_DIR/exo/credentials/token.json`).
- Base AWS credentials are written to `~/.aws/credentials` under the
  `exohub` profile (overridable via `$EXOHUB_AWS_PROFILE`).

Both the internal SSO provider and the generic OIDC provider write credentials in
the same format, so the files are interchangeable between builds.

## Silent refresh

Commands that require authentication automatically attempt a silent token
refresh when the stored access token is expired, using the stored refresh
token. If the refresh token is also expired, the command returns an error
asking the user to run `exo login` again.

## Doctor check

`exo doctor` probes the configured OIDC issuer endpoint
(`EXO_OIDC_ISSUER` → `/.well-known/openid-configuration`) and reports
whether it is reachable. This check works for both builds.
