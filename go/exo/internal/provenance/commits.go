package provenance

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Commit is one commit touching a path, for exo provenance show's "commit
// history" section (design D9: shown as context, "clearly labelled
// 'committed by' and not presented as a PROV claim").
type Commit struct {
	SHA         string
	Author      string
	AuthorEmail string
	Date        string
	Subject     string
}

// commitHistorySep separates the fields of one `git log` record; it must
// not appear in any commit metadata, unlike a space or tab.
const commitHistorySep = "\x1f"

// CommitHistory returns the commits touching path up to and including ref,
// newest first (design D9). File paths use --follow, so a rename does not
// truncate the history (consistent with content-addressed identifiers
// keeping provenance across renames, design D5); folder paths do not, since
// --follow only supports a single file. It returns an empty slice, not an
// error, when path has no history at ref (for example a brand new file, or
// a repository with no commits yet).
func CommitHistory(root, ref, path string) ([]Commit, error) {
	arg := strings.TrimSuffix(path, "/")
	format := "--format=%H" + commitHistorySep + "%an" + commitHistorySep + "%ae" + commitHistorySep + "%aI" + commitHistorySep + "%s"
	args := []string{"log", ref, format}
	if !strings.HasSuffix(path, "/") {
		args = append(args, "--follow")
	}
	args = append(args, "--", arg)

	out, err := gitRun(root, args...)
	if err != nil {
		if looksLikeNoHistory(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list commits for %s: %w", path, err)
	}

	var commits []Commit
	for _, line := range splitLines(out) {
		fields := strings.Split(line, commitHistorySep)
		if len(fields) != 5 {
			continue
		}
		commits = append(commits, Commit{
			SHA:         fields[0],
			Author:      fields[1],
			AuthorEmail: fields[2],
			Date:        fields[3],
			Subject:     fields[4],
		})
	}
	return commits, nil
}

// looksLikeNoHistory reports whether err looks like `git log` failing
// because the repository has no commits yet, or ref does not exist, rather
// than a real failure exo provenance show should surface.
func looksLikeNoHistory(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not have any commits yet") ||
		strings.Contains(msg, "bad revision") ||
		strings.Contains(msg, "unknown revision or path not in the working tree")
}

// gitRun runs a git plumbing command in root, returning stdout. Lineage
// resolution and commit history (design D9) need to read git history
// directly, which the Reader's checked-state abstraction (design D15) does
// not expose, so these new files run git themselves rather than extending
// Reader's API.
func gitRun(root string, args ...string) (string, error) {
	return gitRunStdin(root, "", args...)
}

// gitRunStdin runs a git plumbing command in root with stdin piped in,
// returning stdout: used by batchTreeIDsAt to resolve many commits' tree
// ids in a single `git cat-file --batch-check` process.
func gitRunStdin(root, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// batchTreeIDsAt resolves the tree id of dir at each of shas in a single
// `git cat-file --batch-check` process fed "<sha>:<dir>" lines (review: "get
// <commit>:<dir> tree ids in one batch... git cat-file --batch-check with
// <sha>:<dir> lines"), rather than one `git rev-parse` per commit. Output
// lines are positional (one per input line, including a "<input> missing"
// line for a sha where dir did not exist), so results are zipped back to
// shas by index rather than by parsing the resolved object id out of the
// line.
func batchTreeIDsAt(root string, shas []string, dir string) (map[string]string, error) {
	var stdin strings.Builder
	for _, sha := range shas {
		stdin.WriteString(sha)
		stdin.WriteString(":")
		stdin.WriteString(dir)
		stdin.WriteString("\n")
	}
	out, err := gitRunStdin(root, stdin.String(), "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	lines := splitLines(out)
	ids := make(map[string]string, len(shas))
	for i, sha := range shas {
		if i >= len(lines) {
			break
		}
		fields := strings.Fields(lines[i])
		if len(fields) < 2 || fields[1] != "tree" {
			continue
		}
		ids[sha] = fields[0]
	}
	return ids, nil
}
