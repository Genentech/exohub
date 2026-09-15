# Git Repository Creation - Authentication Setup

## Overview
The `exo init` command can automatically create git repositories on hosting providers (Gitea, GitHub, GitLab) using their REST APIs. Authentication is handled via API tokens.

## Setting Up Authentication

### Gitea

#### Required Token Scopes
When creating a token in Gitea, ensure it has **at minimum**:
- **`write:organization`** - Required to create repositories in organizations
- **`write:repository`** - Required to create and manage repositories

#### Creating a Token
1. Go to your Gitea instance → Settings → Applications → Generate New Token
2. Select the required scopes:
   - ✅ `write:organization`
   - ✅ `write:repository`
3. Copy the token and set it in your environment:

```bash
export EXOHUB_GITEA_TOKEN="your-gitea-token"
```

### Generic Fallback  
```bash
export EXOHUB_GIT_TOKEN="your-token"
```

Provider-specific tokens (e.g., `EXOHUB_GITEA_TOKEN`) take precedence over the generic `EXOHUB_GIT_TOKEN`.

## Provider Detection

The CLI automatically detects providers via HTTP headers or API endpoints. For reliability, add explicit provider to .exohub/context:

provider: gitea  # gitea, github, or gitlab

## Usage Example

```bash
# 1. Create token with write:organization and write:repository scopes
export EXOHUB_GITEA_TOKEN="your-token"

# 2. Create project directory
mkdir my-project && cd my-project

# 3. Create context file
cat > .exohub/context <<EOF
name: mycompany
host: https://git.example.com/
org: sandbox
provider: gitea
EOF

# 4. Initialize (creates repo automatically if needed)
exo init
```

## Troubleshooting

### "token does not have required scope"
If you see an error like:
```
token does not have at least one of required scope(s), required=[write:organization write:repository]
```

Your token is missing required permissions. Recreate the token with:
- ✅ `write:organization` 
- ✅ `write:repository`
