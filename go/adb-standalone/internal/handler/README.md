# handler — known limitations

## Authentication / authorisation (current slice)

There is **no auth middleware** in this slice. The FTS search and scroll handlers
hard-filter `_extra.permissions.read_access = 'public'` in every SurrealQL query,
so restricted documents are silently excluded rather than returning 401/403.

**Implications:**
- Publicly readable documents are served to any caller without a token.
- Restricted documents (`read_access = 'authenticated'` or owner-scoped) are
  invisible to all callers — they will not appear in search or be fetchable via
  `/files/{id}/metadata` (which does no permission check at all today).
- There is no per-tenant isolation beyond the URL path segment.

**Planned fix (not this slice):** add an auth middleware that validates a Bearer
token, resolves the caller's identity, and passes it to query helpers so they can
apply per-caller permission filters matching the reference ArtifactDB contract.

This limitation is intentional for the current development slice and must be
addressed before any non-local deployment.
