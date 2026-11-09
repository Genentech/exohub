package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
)

// allCopyItem is one (ref, path, key) entry copied or previewed by `exo sync --all`.
type allCopyItem struct {
	Ref  string `json:"ref"`
	Path string `json:"path,omitempty"`
	Key  string `json:"key"`
}

// allRemoteReport is the per-remote outcome of `exo sync --all`.
type allRemoteReport struct {
	Remote    string        `json:"remote"`
	Copied    []allCopyItem `json:"copied,omitempty"`
	WouldCopy []allCopyItem `json:"would_copy,omitempty"`
	Failed    []allCopyItem `json:"failed,omitempty"`
}

// allReport is the stable --json schema for `exo sync --all`.
type allReport struct {
	RefsChecked []string          `json:"refs_checked"`
	DryRun      bool              `json:"dry_run"`
	Remotes     []allRemoteReport `json:"remotes"`
	Lost        []checkItem       `json:"lost,omitempty"`
}

// allTargetRemotes resolves the set of annex remotes `--all` copies to:
// writable, plain annex remotes only. Catalog, import, export, readonly and
// write-restricted grants remotes use different sync mechanisms and are
// skipped, matching the existing per-remote branches in runSync.
func allTargetRemotes(names []string, remotesConfig *initcmd.RemotesConfig) ([]string, error) {
	if len(names) == 0 {
		discovered, err := listAnnexRemotes()
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return nil, fmt.Errorf("discovering git-annex remotes: %w", err)
			}
			discovered = nil
		}
		names = discovered
	}
	var targets []string
	for _, name := range names {
		if isCatalogRemoteType(name, remotesConfig) || isImportRemote(name) || isExportRemote(name) {
			continue
		}
		if isReadonlyRemote(name, remotesConfig) || (remoteHasGrantsConfig(name) && !hasGrantsWriteAccess(name)) {
			continue
		}
		targets = append(targets, name)
	}
	sort.Strings(targets)
	return targets, nil
}

// runAllCommand implements `exo sync --all`: for every local branch and tag,
// upload annexed content to every writable, plain annex remote via
// `git annex copy --branch=<ref> [--want-get-by=<remote>] --to <remote>`,
// routing-aware (the remote's own preferred content expression governs what
// it receives, exactly as a normal exo sync does). After uploading, it
// reports what is still lost (present on no remote and not in this clone).
func runAllCommand(withRemotes []string, dryRun bool, outputJSON bool, jobs string) int {
	refs, err := resolveCheckRefs(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if len(refs) == 0 {
		fmt.Println("No branches or tags to sync")
		return 0
	}

	remotesConfig, err := initcmd.LoadRemotesConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load .exohub/remotes: %v\n", err)
		return 1
	}

	targets, err := allTargetRemotes(withRemotes, remotesConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if len(targets) == 0 {
		fmt.Println("No writable git-annex remotes found for --all")
		return 0
	}

	report := allReport{RefsChecked: refs, DryRun: dryRun}
	hadFailure := false

	for _, remote := range targets {
		remoteReport := allRemoteReport{Remote: remote}

		currentWanted, _ := runCommandOutput([]string{"git", "annex", "wanted", remote})
		currentWanted = strings.TrimSpace(currentWanted)
		// Any non-empty expression — including the literal "standard" — is a
		// real preferred-content expression that git-annex resolves itself
		// ("standard" is evaluated from the remote's group, e.g. archive,
		// backup, manual, transfer). Only a genuinely unconfigured remote
		// (empty string, never touched) must skip --want-get-by: passing it
		// then would make git-annex match nothing at all, silently breaking
		// --all for the common case of a remote with no routing configured.
		hasPreferredContent := currentWanted != ""

		if dryRun {
			// Preview via git-annex itself, not an approximation: the exact
			// same matcher (and the same --want-get-by condition) as the real
			// copy below, so the preview can never disagree with what --all
			// would actually transfer.
			for _, ref := range refs {
				args := []string{"git", "annex", "find", "--branch=" + ref, "--not", "--in", remote, "--json"}
				if hasPreferredContent {
					args = append(args, "--want-get-by", remote)
				}
				out, findErr := runCommandOutput(args)
				if findErr != nil && out == "" {
					fmt.Fprintf(os.Stderr, "git annex find --branch=%s --not --in %s: %v\n", ref, remote, findErr)
					return 1
				}
				for _, line := range strings.Split(out, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var res struct {
						File string `json:"file"`
						Key  string `json:"key"`
					}
					if json.Unmarshal([]byte(line), &res) != nil {
						continue
					}
					remoteReport.WouldCopy = append(remoteReport.WouldCopy, allCopyItem{Ref: ref, Path: res.File, Key: res.Key})
				}
			}
		} else {
			for _, ref := range refs {
				args := []string{"git", "annex", "copy", "--branch=" + ref, "--to", remote, "--jobs", jobs, "--json"}
				if hasPreferredContent {
					args = append(args, "--want-get-by", remote)
				}
				out, copyErr := runCommandOutput(args)
				for _, line := range strings.Split(out, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var res struct {
						File    string `json:"file"`
						Key     string `json:"key"`
						Success bool   `json:"success"`
					}
					if json.Unmarshal([]byte(line), &res) != nil {
						continue
					}
					item := allCopyItem{Ref: ref, Path: res.File, Key: res.Key}
					if res.Success {
						remoteReport.Copied = append(remoteReport.Copied, item)
					} else {
						remoteReport.Failed = append(remoteReport.Failed, item)
					}
				}
				if copyErr != nil && len(remoteReport.Failed) == 0 {
					// Non-zero exit with no explicit per-file failure parsed
					// (e.g. the remote was unreachable): surface it.
					fmt.Fprintf(os.Stderr, "git annex copy --branch=%s --to %s: %v\n", ref, remote, copyErr)
					hadFailure = true
				}
			}
			if len(remoteReport.Failed) > 0 {
				hadFailure = true
			}
		}

		report.Remotes = append(report.Remotes, remoteReport)
	}

	if !dryRun {
		engine, err := newCompletenessEngine(withRemotes)
		if err == nil {
			finalReport, err := runCompletenessCheck(refs, engine)
			if err == nil {
				for _, item := range finalReport.Items {
					if item.State == "lost" {
						report.Lost = append(report.Lost, item)
					}
				}
			}
		}
	}

	if outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
	} else {
		printAllReport(os.Stdout, report)
	}

	if hadFailure {
		return 1
	}
	if len(report.Lost) > 0 {
		return exitCheckLost
	}
	return 0
}

func printAllReport(w io.Writer, report allReport) {
	refLabel := strings.Join(report.RefsChecked, ", ")
	if refLabel == "" {
		refLabel = "(none)"
	}
	if report.DryRun {
		fmt.Fprintf(w, "Dry-run: would sync %d ref(s) to every writable remote: %s\n", len(report.RefsChecked), refLabel)
	} else {
		fmt.Fprintf(w, "Synced %d ref(s) to every writable remote: %s\n", len(report.RefsChecked), refLabel)
	}

	for _, r := range report.Remotes {
		if report.DryRun {
			fmt.Fprintf(w, "  %s: would copy %d file(s)\n", r.Remote, len(r.WouldCopy))
			for _, item := range r.WouldCopy {
				fmt.Fprintf(w, "    %s@%s\n", item.Path, item.Ref)
			}
			continue
		}
		fmt.Fprintf(w, "  %s: copied %d file(s)", r.Remote, len(r.Copied))
		if len(r.Failed) > 0 {
			fmt.Fprintf(w, ", %d failed", len(r.Failed))
		}
		fmt.Fprintln(w)
	}

	if len(report.Lost) > 0 {
		fmt.Fprintf(w, "\n%d file(s) are still lost (not on any remote, not in this clone); run 'exo sync --check --json' for details.\n", len(report.Lost))
	}
}
