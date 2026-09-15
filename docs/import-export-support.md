# git-annex-remote-s5cmd Import/Export Support

## Overview

git-annex-remote-s5cmd now supports bidirectional synchronization with S3 through the import/export interface. This allows you to:

- **Export**: Push files from git-annex to S3 as a directory tree
- **Import**: Pull files from S3 into git-annex, tracking changes made by external tools

## Quick Start

### Initialize an Import/Export Remote

```bash
# Initialize a remote with both import and export support
git annex initremote myremote \
    type=external \
    externaltype=s5cmd \
    encryption=none \
    s3url=s3://my-bucket/prefix/ \
    exporttree=yes \
    importtree=yes

# Track the remote
git annex wanted myremote standard
```

### Export a Tree

```bash
# Export a git branch/tag to S3
git annex export main --to myremote

# The files are now available in S3 at:
# s3://my-bucket/prefix/refs/heads/main/...
```

### Import Changes from S3

```bash
# Import changes made to S3 (by you or external tools)
git annex import main --from myremote

# Review imported changes
git log --oneline

# Push to your git remote
git push origin main
```

## Configuration

### Required Parameters

- `type=external` - Use external special remote
- `externaltype=s5cmd` - Use the s5cmd external remote
- `s3url=s3://bucket/prefix` - S3 location
- `exporttree=yes` - Enable export support
- `importtree=yes` - Enable import support

### Optional Parameters

- `encryption=none|shared|hybrid|pubkey` - Encryption mode (default: none)
- Environment variable `S5CMD_BIN` - Path to s5cmd binary (default: searches PATH)

### AWS Credentials

Credentials are read from standard AWS locations:

1. Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
2. AWS credentials file: `~/.aws/credentials` (profile from `AWS_PROFILE` or `default`)
3. EC2 instance metadata (when running on EC2)

Use `exo login` to automatically fetch and store AWS credentials.

## How It Works

### Content Identifiers

To detect changes, the remote uses **content identifiers** that track file versions:

- **Primary**: S3 ETag (MD5 hash for single-part uploads)
- **Fallback**: `size:mtime` tuple (similar to git's index)

Format examples:
- `etag:abc123def456`
- `size:1024:1704067200`

### Export Structure

Files are organized in S3 using the git ref path:

```
s3://bucket/prefix/
  └── refs/heads/main/          # or refs/tags/v1.0, etc.
      ├── file1.txt
      ├── dir/
      │   └── file2.txt
      └── ...
```

### Conditional Operations

All import/export operations are **conditional** to prevent race conditions:

- **Store**: Only overwrites if current content matches expected
- **Retrieve**: Only downloads if content hasn't changed
- **Remove**: Only deletes if content matches expected
- **Check**: Verifies both presence and content identifier

## Common Workflows

### Workflow 1: Export-only (push to S3)

```bash
# Initialize export-only remote
git annex initremote s3backup \
    type=external externaltype=s5cmd \
    encryption=none s3url=s3://backup-bucket/ \
    exporttree=yes

# Export a snapshot
git annex export main --to s3backup
```

### Workflow 2: Import/Export (bidirectional sync)

```bash
# Initialize bidirectional remote
git annex initremote s3shared \
    type=external externaltype=s5cmd \
    encryption=none s3url=s3://shared-bucket/data/ \
    exporttree=yes importtree=yes

# Initial export
git annex export main --to s3shared

# Someone else modifies files in S3...

# Import their changes
git annex import main --from s3shared
git diff HEAD^

# Make your own changes
echo "new content" > newfile.txt
git annex add newfile.txt
git commit -m "Add new file"

# Export your changes
git annex export main --to s3shared
```

### Workflow 3: Import from external data source

```bash
# Initialize import remote for externally-managed S3 bucket
git annex initremote external-data \
    type=external externaltype=s5cmd \
    encryption=none s3url=s3://external-bucket/incoming/ \
    exporttree=yes importtree=yes

# Import external data
git annex import main --from external-data

# Work with the imported files
git annex get .
```

## Troubleshooting

### Issue: "content mismatch" errors

This means the S3 content changed between when git-annex checked it and when it tried to modify it.

**Solution**: Re-run the import or export command. The remote is designed to fail safely rather than overwrite unexpected changes.

```bash
# Re-import to get the latest state
git annex import main --from myremote
```

### Issue: ETag changes for multipart uploads

S3 ETags have different formats for single-part vs multipart uploads:
- Single-part: MD5 hash (e.g., `abc123`)
- Multipart: Compound hash with part count (e.g., `abc123-5`)

If you re-upload the same file using different upload methods, the ETag will change even though content is identical.

**Solution**: Use consistent upload methods, or let git-annex manage all uploads.

### Issue: Performance with large buckets

`LISTIMPORTABLECONTENTS` must list all objects under the prefix, which can be slow for large buckets.

**Solution**:
- Use a more specific prefix: `s3url=s3://bucket/specific/path/`
- Ensure s5cmd is up to date (it's optimized for large listings)

### Debugging

Enable debug output:

```bash
# See all protocol messages
git annex import main --from myremote --debug

# See s5cmd commands being executed
git annex import main --from myremote --debug 2>&1 | grep "s5cmd"
```

## Protocol Details

### Supported Protocol Messages

The remote implements the git-annex external special remote protocol v1 with these import-related messages:

**Queries**:
- `IMPORTSUPPORTED` → `IMPORTSUPPORTED-SUCCESS`
- `LISTIMPORTABLECONTENTS` → Lists all files with content identifiers

**Location Tracking**:
- `LOCATION <name>` - Set current file location
- `EXPECTED <contentid>` - Set expected content identifier
- `NOTHINGEXPECTED` - Mark location as expected to be empty

**Conditional Operations**:
- `RETRIEVEEXPORTEXPECTED <file>` - Download if content matches
- `STOREEXPORTEXPECTED <key> <file>` - Upload if content matches
- `CHECKPRESENTEXPORTEXPECTED <key>` - Check presence and content
- `REMOVEEXPORTEXPECTED <key>` - Delete if content matches
- `REMOVEEXPORTDIRECTORYWHENEMPTY <dir>` - Remove empty directory

### Backward Compatibility

Export-only remotes continue to work unchanged:

```bash
# Old configuration still works
git annex initremote old-remote \
    type=external externaltype=s5cmd \
    encryption=none s3url=s3://bucket/ \
    exporttree=yes
# (no importtree=yes)

# All export commands work as before
git annex export main --to old-remote
```

## Limitations

### Current Limitations

1. **No versioning support**: S3 versioning is not used; only latest version is tracked
2. **No IMPORTKEY support**: Cannot generate git-annex keys from S3 checksums
3. **No HISTORY support**: Historical versions are not tracked
4. **ETag format variance**: Multipart upload ETags differ from single-part

### Future Enhancements

Planned features:
- S3 versioning support via HISTORY protocol extension
- IMPORTKEY support for proper key generation
- Configurable content identifier strategy
- Pagination for very large buckets

## Reference

- [Git-annex external special remote protocol](https://git-annex.branchable.com/design/external_special_remote_protocol/)
- [Export and import appendix](https://git-annex.branchable.com/design/external_special_remote_protocol/export_and_import_appendix/)
- [s5cmd documentation](https://github.com/peak/s5cmd)

## Testing

### Unit Tests

```bash
cd go/git-annex-remote-s5cmd
go test -v
```

### Protocol Tests

```bash
./scripts/test-import-protocol.sh
```

### Integration Tests with git-annex

```bash
# Configure a test remote
git annex initremote test-remote \
    type=external externaltype=s5cmd \
    encryption=none s3url=s3://test-bucket/test-prefix/ \
    exporttree=yes importtree=yes

# Run git-annex's built-in remote tests
git annex testremote test-remote
```

## Examples

See [docs/examples/](../docs/examples/) for complete workflow examples:
- `import-export-workflow.md` - Full import/export workflow
- `external-data-import.md` - Importing external data
- `collaborative-editing.md` - Multi-user scenarios
