# Using exo init with Existing .git/ Folder

## Your Situation
You have a folder with `.git/` already and want to create the remote repository to push to.

## Solution 1: Add repo field to context (Recommended)

```bash
# 1. Set token
export EXOHUB_GITEA_TOKEN="your-token"

# 2. Create .exohub/context with repo name
cat > .exohub/context <<YAML
name: mycontext
host: https://git.example.com/
org: sandbox
provider: gitea
repo: my-project-name
YAML

# 3. Run exo init
exo init

# 4. Push your code
git add .
git commit -m "Initial commit"
git push -u origin main
```

## Solution 2: Use --create-repo flag

```bash
# Will prompt for repo name and create it
exo init --create-repo
```

## What Happens

1. Detects provider (Gitea) from host
2. Checks if repository exists
3. Creates repository if needed
4. Sets origin remote: `git remote add origin <url>`
5. Saves repo name to `.exohub/context` for next time

## Key Points

✅ Safe to run multiple times (idempotent)
✅ Won't overwrite existing remote repos
✅ Repo name saved to context for future use
