package provenance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	lib "github.com/Genentech/exohub/go/exo/internal/provenance"
)

type showOptions struct {
	ref        string
	depth      int
	maxCommits int
	jsonOutput bool
}

// newShowCommand returns `exo provenance show` (design D9).
func newShowCommand() *cobra.Command {
	var opts showOptions

	cmd := &cobra.Command{
		Use:   "show <path>",
		Short: "Show an artifact's provenance lineage",
		Long: `Resolve <path> to its content identifier at a ref (default HEAD) and show
its upstream lineage: generating activities, used and derived-from
entities, plans, agents (including actedOnBehalfOf), timestamps and the
record that asserted each statement. Every repository entity is
shown with its path at the ref, not just its identifier.

A path is covered by its own provenance only when it is the subject of a
generation, derivation, attribution or revision statement; a
path that is only used as an input falls back the same way an uncovered
path does, folded into a "used as an input by" list.

When the path has no provenance of its own, or lies under a declared
provenance.collections folder, show falls back to the nearest enclosing
folder collection with provenance and says so.

The walk follows --depth upstream hops (default unlimited) and detects
cycles. A separate "Commit history" section shows the path's commits as
context, capped at --max-commits (default 10, 0 = unlimited); it is not a
provenance claim. --json emits the lineage subgraph as JSON-LD, with
exo:path set to each entity's path at the ref.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(cmd, args[0], opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.ref, "ref", "HEAD", "show lineage as of this git ref")
	flags.IntVar(&opts.depth, "depth", 0, "maximum number of upstream hops to walk (0 = unlimited)")
	flags.IntVar(&opts.maxCommits, "max-commits", 10, "cap the commit history section to this many most recent commits (0 = unlimited)")
	flags.BoolVar(&opts.jsonOutput, "json", false, "emit the lineage subgraph as JSON-LD")

	return cmd
}

func runShow(cmd *cobra.Command, pathArg string, opts showOptions) error {
	if opts.depth < 0 {
		return fmt.Errorf("--depth must not be negative")
	}
	if opts.maxCommits < 0 {
		return fmt.Errorf("--max-commits must not be negative")
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	relPath, err := normalizeRepoPath(root, pathArg)
	if err != nil {
		return err
	}

	reader := lib.NewRefReader(root, opts.ref)
	lineage, err := lib.BuildLineage(reader, relPath, opts.depth)
	if err != nil {
		return err
	}

	if opts.jsonOutput {
		doc, err := lib.BuildLineageDocument(lineage)
		if err != nil {
			return err
		}
		enc, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("encode lineage: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(enc))
		return nil
	}

	commits, err := lib.CommitHistory(root, opts.ref, relPath)
	if err != nil {
		return err
	}
	printLineage(cmd, relPath, lineage, commits, opts.maxCommits)
	return nil
}

func printLineage(cmd *cobra.Command, path string, lineage *lib.Lineage, commits []lib.Commit, maxCommits int) {
	subjectDisplay := path
	if lineage.Subject.Source != lib.LineageSourceOwn {
		subjectDisplay = lineage.Subject.FolderPath + "/"
	}

	switch lineage.Subject.Source {
	case lib.LineageSourceFolderFallback:
		fmt.Fprintf(cmd.OutOrStdout(), "%s has no provenance of its own; showing the lineage of the enclosing folder collection %s (%s).\n\n",
			path, subjectDisplay, lineage.Subject.ID)
	case lib.LineageSourceDeclaredCollection:
		fmt.Fprintf(cmd.OutOrStdout(), "%s is covered only through the declared collection %s; showing its lineage (%s).\n\n",
			path, subjectDisplay, lineage.Subject.ID)
	}

	if lineage.Hint != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Note: the current tree id has no provenance, but an earlier tree id of %s/ does (%s, commit %s). This can happen when annexed files switch between locked and unlocked mode.\n\n",
			lineage.Hint.FolderPath, lineage.Hint.TreeID, lineage.Hint.CommitSHA)
	}

	if !lineage.Covered {
		fmt.Fprintf(cmd.OutOrStdout(), "No provenance recorded for %s (%s).\n", subjectDisplay, lineage.Subject.ID)
		if len(lineage.UsedByActivities) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "It is used as an input by:")
			for _, actID := range lineage.UsedByActivities {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", displayRef(lineage, actID))
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Lineage for %s (%s):\n\n", subjectDisplay, lineage.Subject.ID)
		printNodes(cmd, lineage)
		if len(lineage.Cycles) > 0 {
			sorted := append([]string(nil), lineage.Cycles...)
			sort.Strings(sorted)
			fmt.Fprintf(cmd.OutOrStdout(), "Cycle detected, involving: %s\n\n", strings.Join(sorted, ", "))
		}
		if lineage.DepthReached {
			fmt.Fprintln(cmd.OutOrStdout(), "(--depth reached; some upstream nodes were not walked)")
			fmt.Fprintln(cmd.OutOrStdout())
		}
	}

	printCommitHistory(cmd, commits, maxCommits)
}

func printCommitHistory(cmd *cobra.Command, commits []lib.Commit, maxCommits int) {
	fmt.Fprintln(cmd.OutOrStdout(), "Commit history (not a provenance claim):")
	if len(commits) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  no commits found")
		return
	}
	shown := commits
	if maxCommits > 0 && len(commits) > maxCommits {
		shown = commits[:maxCommits]
	}
	for _, c := range shown {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s committed by %s <%s> on %s: %s\n", shortSHA(c.SHA), c.Author, c.AuthorEmail, c.Date, c.Subject)
	}
	if more := len(commits) - len(shown); more > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  … %d more (use --max-commits=0 for all)\n", more)
	}
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// displayRef renders id for human output: its current path at the ref when
// one resolves (review: "print its path at the ref"), else its recorded
// exo:path marked as historical, else its label (for agents), else the bare
// id — always alongside the id itself so it stays unambiguous.
func displayRef(lineage *lib.Lineage, id string) string {
	if path, ok := lineage.Paths.Path(id); ok {
		return fmt.Sprintf("%s (%s)", path, id)
	}
	if node := lineage.Graph.Nodes[id]; node != nil {
		if label, ok := node.Node["label"].(string); ok && label != "" {
			return fmt.Sprintf("%s (%s)", label, id)
		}
		if recorded, ok := node.Node["exo:path"].(string); ok && recorded != "" {
			return fmt.Sprintf("%s [recorded path, not present at %s] (%s)", recorded, lineage.Ref, id)
		}
	}
	return id
}

// edgesByProperty groups a node's outgoing lineage edges by property name,
// each list of targets sorted for deterministic output.
func edgesByProperty(lineage *lib.Lineage, nodeID string) map[string][]string {
	byProp := map[string][]string{}
	for _, e := range lineage.Edges {
		if e.From != nodeID {
			continue
		}
		byProp[e.Property] = append(byProp[e.Property], e.To)
	}
	for _, ids := range byProp {
		sort.Strings(ids)
	}
	return byProp
}

func printNodes(cmd *cobra.Command, lineage *lib.Lineage) {
	for _, id := range lineage.Visited {
		node := lineage.Graph.Nodes[id]
		if node == nil {
			continue
		}
		types := node.Types()
		kind := "Node"
		if len(types) > 0 {
			kind = strings.Join(types, "/")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "- [%s] %s\n", kind, displayRef(lineage, id))

		byProp := edgesByProperty(lineage, id)
		for _, prop := range sortedKeys(byProp) {
			targets := make([]string, len(byProp[prop]))
			for i, t := range byProp[prop] {
				targets[i] = displayRef(lineage, t)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "    %s: %s\n", prop, strings.Join(targets, ", "))
		}
		for _, t := range []string{"startedAtTime", "endedAtTime", "generatedAtTime"} {
			if v, ok := node.Node[t].(string); ok && v != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "    %s: %s\n", t, v)
			}
		}
		if len(node.FromRecords) > 0 {
			sources := append([]string(nil), node.FromRecords...)
			sort.Strings(sources)
			for _, recID := range sources {
				meta, ok := lineage.Graph.Records[recID]
				if !ok {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "    source: %s by %s (%s)\n", meta.Assertion, meta.AssertedBy, meta.Path)
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
