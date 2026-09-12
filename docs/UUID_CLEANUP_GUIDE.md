# UUID Cleanup Guide

## Problem: Duplicate Remotes with Different UUIDs

When you clone a data repository and run `exo init`, you may accidentally create duplicate remotes with the same name but different UUIDs. This happens because `git annex initremote` creates a new UUID even if a remote with that name already exists.

### How to Detect Duplicates

Run `git annex info -F` to see all UUIDs:

```bash
$ git annex info -F
trusted repositories: 0
semitrusted repositories: 14
        018c3a7d-fff2-4128-8dbb-73596ecdc680 -- [exospace-metadata]  # Duplicate (wrong)
        afb6e679-92a3-4e1c-b01b-06f52d5ecb2e -- exospace-metadata     # Original (correct)
...
```

Remotes in brackets `[name]` are often duplicates created locally.

---

## Solution 1: Mark UUID as Dead (Recommended)

The **safe** approach that removes the duplicate without rewriting history.

### Command

```bash
exo init --dead <uuid>
```

### What It Does

1. Finds the remote name for the UUID
2. Marks the remote as dead with `git annex dead`
3. Removes dead remotes from git-annex metadata
4. Removes local git config for the remote
5. Displays instructions to reconnect

### When to Use

- ✅ You have a duplicate UUID you want to remove
- ✅ You want to clean up safely
- ✅ You might need to undo the operation later
- ✅ Other repository clones exist

### Example Workflow

```bash
# 1. Identify the wrong UUID
$ git annex info -F | grep exospace-metadata
018c3a7d-fff2-4128-8dbb-73596ecdc680 -- [exospace-metadata]  # Wrong one
afb6e679-92a3-4e1c-b01b-06f52d5ecb2e -- exospace-metadata    # Correct one

# 2. Preview what will be done
$ exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680 --dry-run

# 3. Execute the cleanup
$ exo init --dead 018c3a7d-fff2-4128-8dbb-73596ecdc680
🔍 Looking up UUID 018c3a7d-fff2-4128-8dbb-73596ecdc680...
   Remote name: exospace-metadata

⚠️  This will:
  - Mark remote 'exospace-metadata' (018c3a7d-fff2-4128-8dbb-73596ecdc680) as dead
  - Remove dead remotes from git-annex metadata
  - Remove git config for remote.exospace-metadata

[Interactive Bubble Tea confirmation appears]

🔄 Marking exospace-metadata as dead...
✓ Marked as dead
🔄 Forgetting dead remotes from git-annex...
✓ Forgot dead remotes
🔄 Removing git config...
✓ Removed git config section

✅ Cleanup complete

To reconnect to the correct remote, run: exo init

# 4. Reconnect to the correct remote
$ exo init
```

### Non-Interactive Mode

For scripts or automation:

```bash
exo init --dead <uuid> --yes
```

---

## Solution 2: Destroy UUID (Advanced)

The **destructive** approach that completely removes the UUID from git-annex history.

⚠️ **WARNING**: This rewrites the git-annex branch and cannot be easily undone!

### Command

```bash
exo init --destroy <uuid>
```

### What It Does

1. Displays destructive operation warnings
2. Prompts for migration confirmation
3. Requires explicit UUID re-entry for safety
4. Creates a timestamped backup branch
5. Marks UUID as dead and forgets it
6. Creates a temporary worktree of git-annex branch
7. Filters the UUID from ALL log files:
   - `remote.log`, `uuid.log`, `trust.log`, `group.log`
   - All location logs in hashed directories
8. Commits changes to git-annex branch
9. Removes git config section
10. Displays backup branch name for recovery

### When to Use

- ⚠️ You're absolutely sure the UUID should be completely removed
- ⚠️ You've verified no content is stored only on this UUID
- ⚠️ You understand this rewrites git-annex history
- ⚠️ You have backups and know how to restore them
- ⚠️ The UUID was created by mistake and has no actual content

### Example Workflow

```bash
# 1. Preview what will be done
$ exo init --destroy 018c3a7d-fff2-4128-8dbb-73596ecdc680 --dry-run

# 2. Execute the destruction
$ exo init --destroy 018c3a7d-fff2-4128-8dbb-73596ecdc680
⚠️  WARNING: DESTRUCTIVE OPERATION ⚠️

You are about to DESTROY UUID 018c3a7d-fff2-4128-8dbb-73596ecdc680

Remote name: exospace-metadata

This will:
  ✗ Remove ALL references from git-annex branch
  ✗ Remove from remote.log, uuid.log, trust.log
  ✗ Remove from ALL location tracking logs
  ✗ Rewrite git-annex branch history
  ✗ Cannot be undone without restoring from backup

This does NOT delete actual data on S3/rsync, but you will lose
tracking information about which keys were stored on this UUID.

⚠️  WARNING: This UUID has 5 keys tracked in location logs
Consider migrating content before destroying this UUID.

A backup will be created at: backup/git-annex-20260223-150405

[Interactive Bubble Tea confirmation appears]
Have you migrated all content away from this UUID?
  No    Yes

[If Yes, Bubble Tea UUID input widget appears]
To confirm, type the UUID exactly as shown above:
> 018c3a7d-fff2-4128-8dbb-73596ecdc680

🔄 Creating backup branch...
✓ Backup created: backup/git-annex-20260223-150405

🔄 Marking UUID as dead...
✓ Marked as dead

🔄 Forgetting dead remotes...
✓ Forgot dead remotes

🔄 Destroying UUID from git-annex branch...
  - Creating temporary worktree
  - Filtering remote.log
  - Filtering uuid.log
  - Filtering trust.log
  - Filtering group.log
  - Filtering location logs (123 files)
  - Committing changes
  - Cleaning up worktree
✓ UUID destroyed from git-annex

🔄 Removing git config...
✓ Removed git config

✅ UUID completely destroyed

Verification:
✓ UUID not found in git-annex logs

Backup available at: backup/git-annex-20260223-150405
To restore: git branch -f git-annex backup/git-annex-20260223-150405
```

### Non-Interactive Mode

For scripts (requires explicit confirmation):

```bash
exo init --destroy <uuid> --yes --confirm <uuid>
```

### Recovery from Backup

If you need to restore the destroyed UUID:

```bash
# List available backups
$ git branch | grep backup/git-annex

# Restore from backup
$ git branch -f git-annex backup/git-annex-20260223-150405

# Verify restoration
$ git annex info -F | grep <uuid>
```

---

## Decision Guide: `--dead` vs `--destroy`

### Use `--dead` (Recommended) When:

- ✅ You have a duplicate remote with the wrong UUID
- ✅ You want to clean up safely
- ✅ You might need to undo the operation
- ✅ Other repository clones exist that might reference this UUID
- ✅ You're not sure which UUID is correct
- ✅ **This is the default choice for most situations**

### Use `--destroy` (Advanced) When:

- ⚠️ The UUID was created by mistake and has no actual content
- ⚠️ You've already migrated all content away from this UUID
- ⚠️ You want to completely clean up git-annex history
- ⚠️ You understand the risks and have backups
- ⚠️ You're certain the UUID should never have existed

### If Unsure: Always Try `--dead` First!

The `--dead` command is safe and reversible. You can always run `exo init` again to reconnect to the correct remote.

---

## Common Scenarios

### Scenario 1: Fresh Clone with Duplicate

```bash
# After cloning a data repo
$ git clone <repo-url>
$ cd repo
$ exo init  # Oops! Created duplicate UUIDs

# Fix: Mark the wrong UUID as dead
$ exo init --dead <wrong-uuid>
$ exo init  # Reconnects to correct UUIDs
```

### Scenario 2: Multiple Duplicates

```bash
# Clean up multiple duplicates one at a time
$ exo init --dead <uuid1>
$ exo init --dead <uuid2>
$ exo init --dead <uuid3>

# Then reconnect
$ exo init
```

### Scenario 3: Automated Cleanup in CI/CD

```bash
#!/bin/bash
# cleanup-duplicates.sh

# Identify wrong UUIDs (custom logic)
WRONG_UUIDS=$(./detect-wrong-uuids.sh)

# Clean them up non-interactively
for uuid in $WRONG_UUIDS; do
  exo init --dead "$uuid" --yes
done

# Reconnect to correct remotes
exo init --yes
```

---

## Troubleshooting

### Error: "invalid UUID format"

Make sure the UUID is in the correct format:
```
xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

### Error: "UUID not found"

The UUID doesn't exist in this repository. Check with:
```bash
git annex info -F
```

### After `--dead`, remote still shows up

This is expected! The UUID is marked as dead but still appears in `git annex info -F`. It's no longer active. Run `exo init` to reconnect to the correct remote.

### `--destroy` failed partway through

Check the backup branch:
```bash
git branch | grep backup/git-annex
```

Restore if needed:
```bash
git branch -f git-annex backup/git-annex-<timestamp>
```

---

## Prevention: Avoid Future Duplicates

The latest version of `exo` (with the duplicate fix) prevents this issue by:

1. Using `git annex enableremote` before `initremote`
2. Reconnecting to existing remotes by name instead of creating new ones
3. Tracking UUIDs in `.exohub/remotes` for visibility

To benefit from this fix:
1. Update to the latest `exo` version
2. Clean up existing duplicates using `--dead`
3. Run `exo init` to properly reconnect

---

## Additional Resources

- Run `exo init --help` for command-line reference
- See `DUPLICATE_REMOTE_FIX.md` for technical details about the fix
- Check `.exohub/remotes` to see tracked UUIDs for your remotes
