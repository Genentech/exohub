package commandutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// ListGitFetchRemotes returns the names of git remotes that have a real fetch
// URL (http/https/git/ssh schemes). git-annex special remotes (S3, rsync…)
// are stored in git config with annex-specific keys but no remote.<name>.url,
// so they do not appear in this list.
func ListGitFetchRemotes() []string {
	out, err := exec.Command("git", "config", "--get-regexp", `remote\..*\.url`).Output()
	if err != nil {
		return nil
	}
	var names []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		// key is like remote.origin.url, value is the URL
		key, url := parts[0], parts[1]
		if !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".url") {
			continue
		}
		// Only count remotes whose URL looks like a git server (not s3://, not nourl)
		lowerURL := strings.ToLower(url)
		if !strings.HasPrefix(lowerURL, "http") &&
			!strings.HasPrefix(lowerURL, "git@") &&
			!strings.HasPrefix(lowerURL, "ssh://") &&
			!strings.HasPrefix(lowerURL, "git://") {
			continue
		}
		name := strings.TrimPrefix(key, "remote.")
		name = strings.TrimSuffix(name, ".url")
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// ResolveCloneURL returns the canonical clone URL for the current git
// repository: the "origin" remote's resolved URL when it is a usable clone
// endpoint, otherwise the first other configured remote whose resolved URL
// is usable. Resolution goes through `git remote get-url <name>` so
// url.<base>.insteadOf rewrites apply, unlike reading remote.<name>.url
// directly out of git config.
//
// Only http(s)://, ssh://, git://, and scp-style ([user@]host:path) URLs are
// considered usable; local paths and file:// URLs are rejected, since a
// catalog entry pointing at a local path on the publisher's machine is not
// reachable by anyone else.
func ResolveCloneURL() (string, error) {
	out, err := exec.Command("git", "remote").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list git remotes: %w", err)
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	if len(names) == 0 {
		return "", fmt.Errorf("no git remote configured")
	}

	// Try "origin" first, then the rest in listed order.
	ordered := make([]string, 0, len(names))
	for _, n := range names {
		if n == "origin" {
			ordered = append(ordered, n)
		}
	}
	for _, n := range names {
		if n != "origin" {
			ordered = append(ordered, n)
		}
	}

	var lastErr error
	for _, name := range ordered {
		urlOut, err := exec.Command("git", "remote", "get-url", name).Output()
		if err != nil {
			lastErr = fmt.Errorf("failed to resolve remote %q: %w", name, err)
			continue
		}
		url := strings.TrimSpace(string(urlOut))
		if url == "" || !isCloneableURL(url) {
			lastErr = fmt.Errorf("remote %q (%s) is not a usable clone URL", name, url)
			continue
		}
		return url, nil
	}
	return "", fmt.Errorf("no git remote has a usable clone URL: %w", lastErr)
}

// isCloneableURL reports whether url looks like something that can be
// cloned by someone other than the local machine: http(s)://, ssh://,
// git://, or scp-style [user@]host:path. Local paths and file:// URLs are
// rejected.
func isCloneableURL(url string) bool {
	lower := strings.ToLower(url)
	switch {
	case strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "ssh://"),
		strings.HasPrefix(lower, "git://"):
		return true
	case strings.HasPrefix(lower, "file://"):
		return false
	case strings.HasPrefix(url, "/"),
		strings.HasPrefix(url, "./"),
		strings.HasPrefix(url, "../"):
		return false
	default:
		// Reject any other explicit scheme (s3://, ftp://, rclone://, ...) or
		// git-annex/transport-helper syntax (annex::, datalad-annex::, ...)
		// before falling through to the scp-style check below — otherwise
		// they'd slip past as "host:path".
		if strings.Contains(url, "://") || strings.Contains(url, "::") {
			return false
		}
		// scp-style: [user@]host:path — must have a colon before any slash,
		// and the part before the colon must look like a host rather than a
		// single-letter Windows drive (e.g. "C:\Users\...").
		idx := strings.Index(url, ":")
		if idx <= 1 {
			return false
		}
		hostPart := url[:idx]
		if strings.ContainsAny(hostPart, "/\\") {
			return false
		}
		return true
	}
}
