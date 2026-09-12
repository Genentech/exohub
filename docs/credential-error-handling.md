# Credential Error Handling

## Overview

The exo CLI now automatically detects AWS credential errors (such as expired tokens) during S3 operations and prompts you to re-authenticate instead of failing the entire operation.

## How It Works

When running `exo sync` or `exo export` commands that interact with S3 remotes (via type=S3 or the s5cmd external special remote), the CLI:

1. **Monitors for credential errors** - Detects common AWS authentication issues like:
   - `ExpiredToken: The provided token has expired`
   - `InvalidAccessKeyId`
   - `SignatureDoesNotMatch`
   - HTTP 401/403 status codes
   - Other authentication-related errors

2. **Prompts for re-authentication** - When a credential error is detected:
   ```
   ⚠️  AWS credential error detected:
      ExpiredToken: The provided token has expired. status code: 400

   🔄 Attempting to refresh credentials...
   ```

3. **Initiates device flow login** - Launches the OIDC device flow authentication:
   ```
   🔐 Please visit the following URL to log in and verify code: ABC-XYZ
       https://your-idp.example.com/device?user_code=ABC-XYZ

   ⏳ Waiting for authentication...
   🔑 Fetching AWS credentials...
   ✅ AWS credentials written to ~/.aws/credentials [profile: default]
   ```

4. **Retries the operation** - After successful authentication, the operation is automatically retried with fresh credentials:
   ```
   ✅ Successfully authenticated as: user@example.com
   🔄 Retrying operation with refreshed credentials...
   ```

## Example Error Scenarios

### Expired Token During Export

```bash
$ exo export --to s5-export-studies --path ef/UKB_23288_EUR_F.parquet

export s5-export-studies ef/UKB_23288_EUR_F.parquet
  ERROR "cp .git/annex/objects/.../file s3://bucket/key":
    ExpiredToken: The provided token has expired. status code: 400

⚠️  AWS credential error detected
🔄 Attempting to refresh credentials...
🔐 Please visit the following URL to log in...
✅ Successfully authenticated as: user@example.com
🔄 Retrying operation with refreshed credentials...
```

### Expired Token During Sync

```bash
$ exo sync --with s3-remote

Enabling git-annex remote 's3-remote'
Syncing content with remote 's3-remote'

  ERROR: The provided token has expired. status code: 400

⚠️  AWS credential error detected
🔄 Attempting to refresh credentials...
[authentication flow...]
✅ Successfully authenticated
🔄 Retrying operation with fresh credentials...
```

## Manual Re-authentication

If you prefer to manually refresh credentials before running operations, you can always use:

```bash
exo login --force
```

This will:
- Delete existing cached credentials
- Initiate a new device flow authentication
- Fetch fresh AWS credentials
- Save them to `~/.aws/credentials`

## Configuration

### AWS Profile

The credential handling respects the `AWS_PROFILE` environment variable. If not set, it uses the `default` profile.

```bash
export AWS_PROFILE=production
exo sync --with s3-remote
```

### Credentials File Location

By default, credentials are written to `~/.aws/credentials`. You can override this with the `AWS_SHARED_CREDENTIALS_FILE` environment variable:

```bash
export AWS_SHARED_CREDENTIALS_FILE=/custom/path/credentials
exo login
```

## Disabling Automatic Retry

Currently, automatic credential error retry is enabled by default for all sync and export operations. If you encounter issues with this behavior, please report them to the exo team.

## Technical Details

### Error Detection

The credential error detection is implemented in `go/exo/commandutil/credentials.go` and checks for these patterns (case-insensitive):

- `expiredtoken`
- `token has expired`
- `invalid credentials`
- `invalidaccesskeyid`
- `signaturedoesnotmatch`
- `the security token included in the request is invalid`
- `not authorized`
- `access denied`
- `status code: 400/401/403`

### Logging

When a credential error triggers automatic retry:
- The original failed operation is logged with the standard naming convention
- The retry operation is logged with a `_retry` suffix in the filename
- The retry log's metadata includes `"retry_after_cred_refresh": true`

Example log files:
```
.git/exohub/logs/content_s3-remote_20260212T153045Z_pid12345_j4.log
.git/exohub/logs/content_s3-remote_retry_20260212T153200Z_pid12345_j4.log
```

## Troubleshooting

### Authentication Fails During Retry

If the device flow authentication fails, the original error is returned and the operation exits. Check:
- Network connectivity to your OIDC provider
- Your SSO credentials
- Browser access to the verification URL

### Credential Error Not Detected

If you encounter a credential error that isn't automatically detected and retried, please:
1. Check the log file in `.git/exohub/logs/`
2. Report the error message to the exo team
3. Manually run `exo login --force` to refresh credentials
4. Retry your operation

### Loop of Re-authentication

If you get stuck in a loop of re-authentication requests:
- This may indicate a problem with the OIDC token exchange
- Check that AWS credentials are being written correctly to `~/.aws/credentials`
- Verify the AWS profile configuration
- Contact the exo team for support
