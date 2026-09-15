# Renaming Git-Annex Remotes

This guide explains how to rename git-annex remotes managed by `exo` using the `.exohub/remotes` configuration file.

## Overview

When you create a git-annex remote using `exo init`, the remote is assigned a unique UUID. This UUID identifies the remote in git-annex's metadata, while the `name` field is the user-facing identifier you use in commands.

As of `exo` version 0.35.0+, you can rename remotes by simply editing the `.exohub/remotes` file. The `exo init` command automatically detects the rename and updates git-annex accordingly.

## How Automatic Rename Detection Works

When you run `exo init`, it:

1. Reads the `.exohub/remotes` configuration file
2. For each remote with a UUID, checks if that UUID exists in git-annex under a different name
3. If a name mismatch is detected, automatically runs `git annex renameremote <old-name> <new-name>`
4. Continues with normal validation and configuration

This works for **all remote types**: `annex`, `export`, `import`, and `exospace`.

## Step-by-Step Rename Process

### Example: Renaming an Exospace Remote

Let's say you have a remote called `exospace-metadata` that you want to rename to `delta-data-release`.

**Step 1: Check your current configuration**

View your `.exohub/remotes` file:

```yaml
remotes:
  - name: exospace-metadata
    type: exospace
    uuid: afb6e679-92a3-4e1c-b01b-06f52d5ecb2e
    rsyncurl: /gne/nearline/germline/lelongs/exohub/exospace/gwasdb
    include:
      - ebi_gwas/summary_statistics/**/*.yaml
      - gne_gwas/summary_statistics/**/*.yaml
```

**Step 2: Edit the remote name**

Change the `name` field while keeping the `uuid` intact:

```yaml
remotes:
  - name: delta-data-release  # ← Changed from exospace-metadata
    type: exospace
    uuid: afb6e679-92a3-4e1c-b01b-06f52d5ecb2e  # ← Keep the UUID!
    rsyncurl: /gne/nearline/germline/lelongs/exohub/exospace/gwasdb
    include:
      - ebi_gwas/summary_statistics/**/*.yaml
      - gne_gwas/summary_statistics/**/*.yaml
```

**Step 3: Run exo init**

```bash
exo init
```

You should see output like:

```
🔄 Renaming remote: exospace-metadata → delta-data-release (UUID: afb6e679-92a3-4e1c-b01b-06f52d5ecb2e)
✅ Remote renamed successfully

==> Validating 1 remote(s) from .exohub/remotes
✅ Remote delta-data-release is correctly configured
```

**Step 4: Verify the rename**

Check that the remote now has the new name:

```bash
git annex info delta-data-release
```

The old name should no longer exist:

```bash
git annex info exospace-metadata
# Should return an error: "not found"
```

## Renaming Different Remote Types

### Annex Remote

```yaml
remotes:
  - name: s3-backup-new  # Renamed from s3-backup
    type: annex
    uuid: 12345678-1234-1234-1234-123456789abc
    s3url: s3://my-bucket/backup
    chunk: 1GiB
```

### Export Remote

```yaml
remotes:
  - name: production-export  # Renamed from prod-export
    type: export
    uuid: 87654321-4321-4321-4321-cba987654321
    s3url: s3://my-bucket/exports
    tracking-branch: main
```

### Import Remote

```yaml
remotes:
  - name: data-import-new  # Renamed from data-import
    type: import
    uuid: abcd1234-5678-9012-3456-789012345678
    s3url: s3://source-bucket/data
    tracking-branch: import-branch
```

## Important Notes

### ✅ What You Can Do

- Rename any remote type (`annex`, `export`, `import`, `exospace`)
- Rename multiple remotes in one `exo init` run
- Change other remote properties at the same time (e.g., update `rsyncurl`, `include` patterns)

### ⚠️ What to Remember

- **Always keep the UUID**: The UUID is what identifies the remote. Changing it would create a new remote instead of renaming.
- **One name per UUID**: You cannot have multiple remotes with the same UUID.
- **Git-annex special remotes only**: This feature renames git-annex special remotes, not regular git remotes (like `origin`).

### 🚫 What NOT to Do

- **Don't change the UUID** if you want to rename an existing remote
- **Don't remove the UUID** during a rename - keep the original UUID value
- **Don't create duplicate names** - each remote must have a unique name

## Troubleshooting

### Remote not renamed

**Symptom:** `exo init` doesn't show the rename message, and the old name still exists.

**Possible causes:**

1. **Missing UUID in config** - The remote entry needs the `uuid` field for rename detection.

   **Fix:** Add the UUID to your `.exohub/remotes` entry. You can find it with:
   ```bash
   git config --get remote.<old-name>.annex-uuid
   ```

2. **UUID doesn't exist in git-annex** - The UUID you specified doesn't match any existing remote.

   **Fix:** Verify the UUID is correct. List all remotes and their UUIDs:
   ```bash
   git config --get-regexp 'remote\..*\.annex-uuid'
   ```

3. **Names already match** - The remote already has the desired name in git-annex.

   **Fix:** No action needed - the remote is already named correctly.

### Rename fails with error

**Symptom:** You see an error message like:
```
❌ Failed to rename remote: ...
```

**Possible causes:**

1. **New name conflicts with existing remote** - Another remote already has the target name.

   **Fix:** Choose a different name, or remove/rename the conflicting remote first.

2. **Git-annex not initialized** - The repository doesn't have git-annex initialized.

   **Fix:** Run `git annex init` first, then try `exo init` again.

### Manual rename if needed

If automatic rename fails, you can always rename manually:

```bash
git annex renameremote <old-name> <new-name>
```

Then run `exo init` to verify the configuration.

## Advanced: Renaming Without UUID in Config

If you haven't captured the UUID in your `.exohub/remotes` file yet, you can add it:

1. Get the UUID:
   ```bash
   git config --get remote.<current-name>.annex-uuid
   ```

2. Add it to your `.exohub/remotes`:
   ```yaml
   remotes:
     - name: new-name
       type: annex
       uuid: <paste-uuid-here>
       # ... other config
   ```

3. Run `exo init` to trigger the rename.

## History: Before Automatic Rename

Before version 0.35.0, renaming required manual steps:

```bash
# Old workflow (no longer necessary):
git annex renameremote old-name new-name
# Then edit .exohub/remotes
# Then run exo init
```

The new automatic detection eliminates these manual steps and reduces errors.

## See Also

- [Git-annex renameremote documentation](https://git-annex.branchable.com/git-annex-renameremote/)
- [Exo remote configuration guide](./export-remotes.md)
- [UUID cleanup guide](./UUID_CLEANUP_GUIDE.md)
