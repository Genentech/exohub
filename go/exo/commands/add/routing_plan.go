package add

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// PlannedFile records an auto-add destination and its explanation.
type PlannedFile struct {
	Path              string  `json:"path"`
	Size              int64   `json:"size"`
	Backend           Backend `json:"-"`
	Reason            string  `json:"reason"`
	LargeTextFallback bool    `json:"-"`

	// Reroute decisions are separate from the file's unchanged current backend.
	rerouteTo    Backend
	rerouteRule  string
	rerouteKnown bool
	rerouteError string
	annexKey     string
	tracked      bool

	// repoRel is the repo-root-relative path (forward-slash separated),
	// kept alongside the cwd-relative Path so RerouteCandidates can check
	// IsProtectedPath without re-deriving it from the repo root.
	repoRel string
}

// trackedReason renders the WHY column for a tracked file: it keeps its
// current backend unconditionally (design D5), but hints at what
// `exo add --reroute` would do if the router's decision differs and that
// decision is known (see PlannedFile.rerouteKnown).
func trackedReason(current Backend, wouldRoute Backend, rerouteKnown bool) string {
	reason := "tracked (" + current.String() + ") — unchanged"
	if rerouteKnown && wouldRoute != current {
		reason += "; --reroute would move to " + wouldRoute.String()
	}
	return reason
}

// PlanOptions controls optional, non-default behaviour of PlanAutoAdd.
type PlanOptions struct {
	// RerouteHints enables computing the "--reroute would move to …" hint
	// (design D8) for tracked files, by also running the router over them.
	// It must stay false for plain `exo add` and the MCP add tool (design
	// D5: the router is not consulted for tracked files there) and is only
	// set by `exo add --explain`. Even when true, the hint is best-effort:
	// it is silently omitted (never an error) when content is unavailable, or
	// if routing the file fails for any other reason (e.g. its content was
	// dropped), since guessing from a dangling symlink or an unreadable
	// file would be misleading or crash the command.
	RerouteHints bool
	reroute      bool
}

// ReroutePlan records authoritative moves and files whose policy could not be
// evaluated. Skipped files must be reported, not mistaken for a no-op.
type ReroutePlan struct {
	Moves   []PlannedFile
	Skipped []PlannedFile
}

func PlanReroute(dir string, paths []string, threshold string) (ReroutePlan, error) {
	files, err := PlanAutoAddWithOptions(dir, paths, threshold, PlanOptions{reroute: true})
	if err != nil {
		return ReroutePlan{}, err
	}
	var plan ReroutePlan
	for _, file := range files {
		if !file.tracked {
			continue
		}
		protected, err := IsProtectedPath(file.repoRel)
		if err != nil {
			return ReroutePlan{}, err
		}
		if protected {
			continue
		}
		if file.rerouteError != "" {
			plan.Skipped = append(plan.Skipped, file)
		} else if file.rerouteKnown && file.rerouteTo != file.Backend {
			plan.Moves = append(plan.Moves, file)
		}
	}
	return plan, nil
}

// RerouteCandidates retains the moves-only API, but must not hide skipped files.
func RerouteCandidates(dir string, paths []string, threshold string) ([]PlannedFile, error) {
	plan, err := PlanReroute(dir, paths, threshold)
	if err != nil {
		return nil, err
	}
	if len(plan.Skipped) > 0 {
		return nil, fmt.Errorf("%s: %s (%d file(s) skipped)", plan.Skipped[0].Path, plan.Skipped[0].rerouteError, len(plan.Skipped))
	}
	return plan.Moves, nil
}

// annexKeySize accepts the optional s<size> field before the key's -- separator.
func annexKeySize(key string) (int64, bool) {
	metadata, _, ok := strings.Cut(key, "--")
	if !ok {
		return 0, false
	}
	for _, field := range strings.Split(metadata, "-")[1:] {
		if strings.HasPrefix(field, "s") {
			digits := strings.TrimPrefix(field, "s")
			if digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				return 0, false
			}
			size, err := strconv.ParseInt(digits, 10, 64)
			return size, err == nil
		}
	}
	return 0, false
}

type sizedInfo struct {
	fs.FileInfo
	size int64
}

func (i sizedInfo) Size() int64 { return i.size }

// trackedContentInfo never uses a locked symlink's length or an unlocked
// annex pointer's bytes as content. Missing content still permits metadata rules.
func trackedContentInfo(path, key string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	linkInfo, _ := os.Lstat(path)
	if err == nil && key != "" && info.Mode().IsRegular() && linkInfo != nil && linkInfo.Mode()&os.ModeSymlink == 0 {
		f, openErr := os.Open(path)
		if openErr != nil {
			return info, fmt.Errorf("content unavailable (run exo get %q): %w", path, openErr)
		}
		pointer := "/annex/objects/" + key + "\n"
		prefix := make([]byte, len(pointer))
		n, readErr := io.ReadFull(f, prefix)
		f.Close()
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return info, fmt.Errorf("content unavailable (run exo get %q): %w", path, readErr)
		}
		if info.Size() == int64(len(pointer)) && string(prefix[:n]) == pointer {
			err = os.ErrNotExist
		}
	}
	if err == nil {
		return info, nil
	}
	unavailable := fmt.Errorf("content unavailable (run exo get %q): %w", path, err)
	if key != "" {
		if size, ok := annexKeySize(key); ok {
			return sizedInfo{size: size}, unavailable
		}
	}
	return nil, unavailable
}

// PlanAutoAdd validates and plans all local files without changing the
// index, with PlanOptions left at its default (no reroute hints; see
// PlanOptions.RerouteHints). This is the entry point plain `exo add` and
// the MCP add tool must use, so the router is never consulted for tracked
// files (design D5).
func PlanAutoAdd(dir string, paths []string, threshold string) ([]PlannedFile, error) {
	return PlanAutoAddWithOptions(dir, paths, threshold, PlanOptions{})
}

// PlanAutoAddWithOptions is PlanAutoAdd with explicit PlanOptions. Only
// `exo add --explain` sets RerouteHints (design D8).
// Paths in the result are relative to dir; rules always match root-relative paths.
func PlanAutoAddWithOptions(dir string, paths []string, threshold string, opts PlanOptions) ([]PlannedFile, error) {
	cwd, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
	out, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root := strings.TrimSuffix(string(out), "\n")
	cfg, err := initcmd.LoadExohubConfigAt(root)
	if err != nil {
		return nil, err
	}
	router, err := NewRouter(cfg, threshold, root)
	if err != nil {
		return nil, err
	}
	out, err = run("ls-files", "--cached", "--full-name", "-z")
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool)
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}

	// lookupkey works for both locked symlinks and unlocked annex pointers.
	out, err = run("rev-parse", "--git-path", "annex")
	if err != nil {
		return nil, err
	}
	annexDir := strings.TrimSuffix(string(out), "\n")
	if !filepath.IsAbs(annexDir) {
		annexDir = filepath.Join(cwd, annexDir)
	}
	_, statErr := os.Stat(annexDir)
	hasAnnex := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}

	var result []PlannedFile
	var trackedIndices []int
	seen := make(map[string]bool)

	covered := make(map[string]bool)
	for start := 0; start < len(paths); {
		end, bytes := start, 0
		for end < len(paths) && end-start < maxFilesPerBatch {
			size := len(paths[end]) + 1
			if size > 64*1024 {
				return nil, fmt.Errorf("path argument exceeds 64 KiB")
			}
			if bytes+size > 64*1024 {
				break
			}
			bytes += size
			end++
		}
		args := append([]string{"ls-files", "-co", "--exclude-standard", "--full-name", "-z", "--"}, paths[start:end]...)
		out, err := run(args...)
		if err != nil {
			return nil, err
		}
		for _, rel := range strings.Split(string(out), "\x00") {
			if rel == "" || seen[rel] {
				continue
			}
			seen[rel] = true
			for prefix := rel; ; prefix = filepath.ToSlash(filepath.Dir(prefix)) {
				covered[prefix] = true
				if prefix == "." {
					break
				}
			}
			local, err := filepath.Rel(cwd, filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			if opts.reroute && !tracked[rel] {
				continue
			}
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil && !(tracked[rel] && (opts.reroute || opts.RerouteHints)) {
				return nil, err
			}
			file := PlannedFile{Path: local, repoRel: rel}
			if info != nil {
				file.Size = info.Size()
			}
			if tracked[rel] {
				// The router is never consulted for tracked files outside
				// explain mode (design D5): plain `exo add` and MCP add
				// must not Lstat-and-sniff every tracked file under the
				// pathspec, which is needless work and can fail outright
				// on a locked file whose content was dropped.
				file.Backend = Git
				file.tracked = true
				trackedIndices = append(trackedIndices, len(result))

				file.Reason = trackedReason(file.Backend, file.rerouteTo, file.rerouteKnown)
			} else {
				decision, err := router.Route(rel, info)
				if err != nil {
					return nil, err
				}
				file.Backend = decision.Backend
				file.Reason = decision.Rule.String()
				file.LargeTextFallback = decision.Rule.LargeTextFallback
			}
			result = append(result, file)
		}
		start = end
	}
	// Only unmatched inputs need individual error checks. Literal files and
	// directory prefixes are covered by the batched output above.
	for _, input := range paths {
		absolute := input
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(cwd, input)
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, err
		}
		if covered[filepath.ToSlash(rel)] {
			continue
		}
		if strings.ContainsAny(input, "*?[") || strings.HasPrefix(input, ":") {
			out, err := run("ls-files", "-co", "--exclude-standard", "--full-name", "-z", "--", input)
			if err != nil {
				return nil, err
			}
			if len(out) > 0 {
				continue
			}
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return nil, fmt.Errorf("%s: %w", input, statErr)
		}
		ignore := exec.Command("git", "check-ignore", "-q", "--", input)
		ignore.Dir = cwd
		ignoreErr := ignore.Run()
		if !info.IsDir() || ignoreErr == nil {
			return nil, fmt.Errorf("%s is ignored or outside the repository", input)
		}
		if exit, ok := ignoreErr.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("check-ignore %s: %w", input, ignoreErr)
		}
	}

	if hasAnnex {
		var input strings.Builder
		for _, i := range trackedIndices {
			input.WriteString(result[i].Path)
			input.WriteByte(0)
		}
		if len(trackedIndices) > 0 {
			cmd := exec.Command("git", "annex", "lookupkey", "--batch", "-z")
			cmd.Dir = cwd
			cmd.Stdin = strings.NewReader(input.String())
			out, err := cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("git annex lookupkey: %w", err)
			}
			keys := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			if len(keys) != len(trackedIndices) {
				return nil, fmt.Errorf("git annex lookupkey returned %d results for %d files", len(keys), len(trackedIndices))
			}
			for n, i := range trackedIndices {
				if keys[n] != "" {
					result[i].Backend = Annex
					result[i].annexKey = keys[n]
				}
				result[i].Reason = trackedReason(result[i].Backend, result[i].rerouteTo, result[i].rerouteKnown)
			}
		}
	}

	for _, i := range trackedIndices {
		file := &result[i]
		// Normal add/MCP do not consult routing or inspect tracked content.
		if !opts.RerouteHints && !opts.reroute {
			continue
		}
		protected, err := IsProtectedPath(file.repoRel)
		if err != nil {
			return nil, err
		}
		if opts.reroute && protected {
			continue
		}
		info, unavailable := trackedContentInfo(filepath.Join(root, filepath.FromSlash(file.repoRel)), file.annexKey)
		if info != nil {
			file.Size = info.Size()
		} else {
			file.Size = 0
		}
		decision, routeErr := router.routeContent(file.repoRel, info, unavailable)
		if routeErr == nil {
			file.rerouteTo, file.rerouteRule, file.rerouteKnown = decision.Backend, decision.Rule.String(), true
		} else if opts.reroute {
			file.rerouteError = routeErr.Error()
		}
		file.Reason = trackedReason(file.Backend, file.rerouteTo, file.rerouteKnown)
	}

	return result, nil
}
