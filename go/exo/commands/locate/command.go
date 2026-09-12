package locate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/palette"
)

var command = commandutil.Command

type filterOpts struct {
	annex     bool
	git       bool
	untracked bool
	present   bool
	missing   bool
	locked    bool
	unlocked  bool
	anyFilter bool
}

func (f *filterOpts) shouldShow(fileType string, present bool, locked bool) bool {
	if !f.anyFilter {
		return true
	}
	if f.locked {
		if fileType != "annex" || !locked {
			return false
		}
		if f.present {
			return present
		}
		if f.missing {
			return !present
		}
		return true
	}
	if f.unlocked {
		if fileType != "annex" || locked {
			return false
		}
		if f.present {
			return present
		}
		if f.missing {
			return !present
		}
		return true
	}
	if f.present {
		return fileType == "annex" && present
	}
	if f.missing {
		return fileType == "annex" && !present
	}
	switch fileType {
	case "annex":
		return f.annex
	case "git":
		return f.git
	case "untracked":
		return f.untracked
	}
	return false
}

func NewCommand() *cobra.Command {
	var jsonOut bool
	var fast bool
	var filters filterOpts

	cmd := &cobra.Command{
		Use:   "locate [paths...]",
		Short: "Show where files are stored (git, annex, remotes)",
		Long: `Show storage location and metadata for files.

Reports whether each file is tracked by regular git or git-annex.
For annexed files, shows which remotes hold a copy (with remote type
from .exohub/remotes).

Use --fast for lightweight git/annex/untracked classification only.

Filter flags narrow output to specific file types:

  exo locate --annex              # only annexed files
  exo locate --missing            # annexed files not present locally
  exo locate --git --untracked    # everything except annex
  exo locate --present data/      # annexed files present locally in data/
  exo locate --locked             # only locked (symlink) annexed files
  exo locate --unlocked           # only unlocked (editable) annexed files
  exo locate --locked --present   # locked annexed files present locally

Multiple type flags combine as OR. --present and --missing imply --annex.
--locked and --unlocked imply --annex and compose with --present/--missing.`,
		Args: cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if filters.present && filters.missing {
				fmt.Fprintln(os.Stderr, "error: --present and --missing are mutually exclusive")
				os.Exit(1)
			}
			if filters.locked && filters.unlocked {
				fmt.Fprintln(os.Stderr, "error: --locked and --unlocked are mutually exclusive")
				os.Exit(1)
			}
			if (filters.present || filters.missing) && fast {
				fmt.Fprintln(os.Stderr, "error: --present/--missing requires full mode (remove --fast)")
				os.Exit(1)
			}
			filters.anyFilter = filters.annex || filters.git || filters.untracked || filters.present || filters.missing || filters.locked || filters.unlocked
			if err := requireBins("git-annex"); err != nil {
				os.Exit(127)
			}
			if err := requireAnnexInit(); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			if fast {
				runFast(args, jsonOut, &filters)
			} else {
				runFull(args, jsonOut, &filters)
			}
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output machine-readable JSON")
	cmd.Flags().BoolVar(&fast, "fast", false, "Only classify files as git/annex/untracked (no remote info)")
	cmd.Flags().BoolVar(&filters.annex, "annex", false, "Show only annexed files")
	cmd.Flags().BoolVar(&filters.git, "git", false, "Show only git-tracked files")
	cmd.Flags().BoolVar(&filters.untracked, "untracked", false, "Show only untracked files")
	cmd.Flags().BoolVar(&filters.present, "present", false, "Show only annexed files present locally")
	cmd.Flags().BoolVar(&filters.missing, "missing", false, "Show only annexed files not present locally")
	cmd.Flags().BoolVar(&filters.locked, "locked", false, "Show only locked annexed files")
	cmd.Flags().BoolVar(&filters.unlocked, "unlocked", false, "Show only unlocked annexed files")

	return cmd
}

type fileEntry struct {
	File    string   `json:"file"`
	Type    string   `json:"type"`
	Key     string   `json:"key,omitempty"`
	Size    int64    `json:"size,omitempty"`
	Present bool     `json:"present,omitempty"`
	Locked  bool     `json:"locked,omitempty"`
	Remotes []string `json:"remotes,omitempty"`
}

type whereisEntry struct {
	File      string          `json:"file"`
	Key       string          `json:"key"`
	Whereis   []whereisRemote `json:"whereis"`
	Untrusted []whereisRemote `json:"untrusted"`
}

type whereisRemote struct {
	UUID        string `json:"uuid"`
	Description string `json:"description"`
	Here        bool   `json:"here"`
}

func runFull(paths []string, jsonOut bool, filters *filterOpts) {
	remoteTypes := loadExohubRemotes()
	printer := newLinePrinter(jsonOut, remoteTypes)

	// Phase 1: build annex map from whereis (must complete before streaming)
	annexed := whereisFiles(paths)
	annexMap := make(map[string]fileEntry, len(annexed))
	for _, w := range annexed {
		_, size := parseAnnexKey(w.Key)
		present := false
		var remotes []string
		for _, r := range append(w.Whereis, w.Untrusted...) {
			if r.Here {
				present = true
				continue
			}
			name := extractRemoteName(r.Description)
			if name != "" {
				remotes = append(remotes, name)
			}
		}
		if present {
			remotes = append([]string{"here"}, remotes...)
		}
		annexMap[w.File] = fileEntry{
			File:    w.File,
			Type:    "annex",
			Key:     w.Key,
			Size:    size,
			Present: present,
			Locked:  isLockedAnnex(w.File),
			Remotes: remotes,
		}
	}

	// Phase 2: stream ls-files, classify each line immediately
	streamLsFiles(paths, func(file string) {
		if !fileExists(file) {
			return
		}
		if e, ok := annexMap[file]; ok {
			if filters.shouldShow(e.Type, e.Present, e.Locked) {
				printer.printEntry(e)
			}
		} else {
			if filters.shouldShow("git", false, false) {
				printer.printEntry(fileEntry{File: file, Type: "git"})
			}
		}
	})

	// Phase 3: stream untracked files
	if !filters.shouldShow("untracked", false, false) {
		return
	}
	if len(paths) > 0 && hasFileArgs(paths) {
		streamUntrackedExplicit(paths, annexMap, printer)
	} else {
		streamUntracked(paths, func(file string) {
			printer.printEntry(fileEntry{File: file, Type: "untracked"})
		})
	}
}

func runFast(paths []string, jsonOut bool, filters *filterOpts) {
	printer := newLinePrinter(jsonOut, nil)

	// Stream ls-files piped to lookupkey --batch
	streamLsFilesWithKeys(paths, func(file, key string) {
		if !fileExists(file) {
			return
		}
		if key != "" {
			locked := isLockedAnnex(file)
			if filters.shouldShow("annex", false, locked) {
				printer.printEntry(fileEntry{File: file, Type: "annex", Locked: locked})
			}
		} else {
			if filters.shouldShow("git", false, false) {
				printer.printEntry(fileEntry{File: file, Type: "git"})
			}
		}
	})

	// Stream untracked
	if !filters.shouldShow("untracked", false, false) {
		return
	}
	if len(paths) > 0 && hasFileArgs(paths) {
		streamUntrackedExplicitFast(paths, printer)
	} else {
		streamUntracked(paths, func(file string) {
			printer.printEntry(fileEntry{File: file, Type: "untracked"})
		})
	}
}

// streamLsFiles pipes git ls-files stdout line by line to the callback.
func streamLsFiles(paths []string, fn func(file string)) {
	args := []string{"git", "ls-files", "--"}
	if len(paths) > 0 {
		args = append(args, paths...)
	} else {
		args = append(args, ".")
	}

	cmd := command(args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			fn(line)
		}
	}
	_ = cmd.Wait()
}

// streamLsFilesWithKeys pipes ls-files into lookupkey --batch,
// streaming file+key pairs to the callback.
func streamLsFilesWithKeys(paths []string, fn func(file, key string)) {
	lsArgs := []string{"git", "ls-files", "--"}
	if len(paths) > 0 {
		lsArgs = append(lsArgs, paths...)
	} else {
		lsArgs = append(lsArgs, ".")
	}

	lsCmd := command(lsArgs[0], lsArgs[1:]...)
	lsStdout, err := lsCmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := lsCmd.Start(); err != nil {
		return
	}

	keyCmd := command("git", "annex", "lookupkey", "--batch")
	keyStdin, err := keyCmd.StdinPipe()
	if err != nil {
		_ = lsCmd.Wait()
		return
	}
	keyStdout, err := keyCmd.StdoutPipe()
	if err != nil {
		_ = lsCmd.Wait()
		return
	}
	if err := keyCmd.Start(); err != nil {
		_ = lsCmd.Wait()
		return
	}

	// Read ls-files and feed to lookupkey in a goroutine
	filesCh := make(chan string, 100)
	go func() {
		scanner := bufio.NewScanner(lsStdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				filesCh <- line
				fmt.Fprintln(keyStdin, line)
			}
		}
		close(filesCh)
		keyStdin.Close()
		_ = lsCmd.Wait()
	}()

	keyScanner := bufio.NewScanner(keyStdout)
	for file := range filesCh {
		key := ""
		if keyScanner.Scan() {
			key = strings.TrimSpace(keyScanner.Text())
		}
		fn(file, key)
	}
	_ = keyCmd.Wait()
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// streamUntracked pipes git ls-files --others stdout line by line.
func streamUntracked(paths []string, fn func(file string)) {
	args := []string{"git", "ls-files", "--others", "--exclude-standard", "--"}
	if len(paths) > 0 {
		args = append(args, paths...)
	} else {
		args = append(args, ".")
	}

	cmd := command(args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			fn(line)
		}
	}
	_ = cmd.Wait()
}

// streamUntrackedExplicit handles untracked detection for explicit file paths in full mode.
func streamUntrackedExplicit(paths []string, annexMap map[string]fileEntry, printer *linePrinter) {
	tracked := lsFiles(paths)
	trackedSet := make(map[string]bool, len(tracked))
	for _, f := range tracked {
		trackedSet[f] = true
	}
	for _, p := range expandPaths(paths) {
		if !trackedSet[p] && annexMap[p].File == "" {
			printer.printEntry(fileEntry{File: p, Type: "untracked"})
		}
	}
}

// streamUntrackedExplicitFast handles untracked detection for explicit file paths in fast mode.
func streamUntrackedExplicitFast(paths []string, printer *linePrinter) {
	tracked := lsFiles(paths)
	trackedSet := make(map[string]bool, len(tracked))
	for _, f := range tracked {
		trackedSet[f] = true
	}
	for _, p := range expandPaths(paths) {
		if !trackedSet[p] {
			printer.printEntry(fileEntry{File: p, Type: "untracked"})
		}
	}
}

// hasFileArgs returns true if any of the paths point to an existing file (not a directory).
func hasFileArgs(paths []string) bool {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			return true
		}
	}
	return false
}

// expandPaths returns only the existing non-directory paths from the input,
// converted to repo-relative paths. Non-existent paths produce a stderr error and are skipped.
func expandPaths(paths []string) []string {
	var result []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "locate: %s: no such file\n", p)
			continue
		}
		if !fi.IsDir() {
			result = append(result, toRepoRelative(p))
		}
	}
	return result
}

var repoRoot string

func getRepoRoot() string {
	if repoRoot != "" {
		return repoRoot
	}
	cmd := command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	repoRoot = strings.TrimSpace(string(out))
	return repoRoot
}

func toRepoRelative(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	root := getRepoRoot()
	if root == "" {
		return path
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}
	realPath := filepath.Join(realDir, base)
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return path
	}
	return rel
}

func isLockedAnnex(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func whereisFiles(paths []string) []whereisEntry {
	args := []string{"git", "annex", "whereis", "--json"}
	args = append(args, paths...)

	cmd := command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}

	var entries []whereisEntry
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry whereisEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry.File == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func gitUntrackedFiles(paths []string) []string {
	args := []string{"git", "ls-files", "--others", "--exclude-standard", "--"}
	if len(paths) > 0 {
		args = append(args, paths...)
	} else {
		args = append(args, ".")
	}

	cmd := command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func lsFiles(paths []string) []string {
	args := []string{"git", "ls-files", "--"}
	if len(paths) > 0 {
		args = append(args, paths...)
	} else {
		args = append(args, ".")
	}

	cmd := command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func lookupKeyBatch(files []string) []string {
	cmd := command("git", "annex", "lookupkey", "--batch")

	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookupkey pipe error: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "lookupkey start error: %v\n", err)
		os.Exit(1)
	}

	keys := make([]string, 0, len(files))
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		keys = append(keys, strings.TrimSpace(scanner.Text()))
	}

	_ = cmd.Wait()

	for len(keys) < len(files) {
		keys = append(keys, "")
	}
	return keys
}

// parseAnnexKey extracts backend and size from an annex key string.
// Key format: BACKEND-sNNNN--HASH.EXT (e.g., SHA256E-s12345--abc.csv)
func parseAnnexKey(key string) (backend string, size int64) {
	if key == "" {
		return "", 0
	}

	dashIdx := strings.Index(key, "-")
	if dashIdx < 0 {
		return key, 0
	}
	backend = key[:dashIdx]

	rest := key[dashIdx+1:]
	if strings.HasPrefix(rest, "s") {
		sizeEnd := strings.Index(rest, "-")
		if sizeEnd < 0 {
			sizeEnd = len(rest)
		}
		if n, err := strconv.ParseInt(rest[1:sizeEnd], 10, 64); err == nil {
			size = n
		}
	}
	return backend, size
}

func extractRemoteName(desc string) string {
	desc = strings.TrimSpace(desc)
	start := strings.LastIndex(desc, "[")
	end := strings.LastIndex(desc, "]")
	if start >= 0 && end > start {
		name := strings.TrimSpace(desc[start+1 : end])
		if name != "" && name != "here" {
			return name
		}
	}
	return ""
}

func formatRemoteWithEmoji(name string, remoteTypes map[string]string) string {
	if remoteTypes == nil {
		return name
	}
	if typ, ok := remoteTypes[name]; ok && typ != "" {
		return fmt.Sprintf("%s %s", commandutil.RemoteTypeEmoji(typ), name)
	}
	return name
}

func formatSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(tb))
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

type remoteConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type remotesConfigFile struct {
	Remotes []remoteConfig `yaml:"remotes"`
}

func loadExohubRemotes() map[string]string {
	path := filepath.Join(".exohub", "remotes")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var config remotesConfigFile
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil
	}

	remotes := make(map[string]string)
	for _, r := range config.Remotes {
		remotes[r.Name] = r.Type
	}
	return remotes
}

// linePrinter handles streaming output, one entry at a time (text or JSONL).
type linePrinter struct {
	jsonOut            bool
	remoteTypes        map[string]string
	typeGitStyle       lipgloss.Style
	typeAnnexStyle     lipgloss.Style
	typeUntrackedStyle lipgloss.Style
	remoteStyle        lipgloss.Style
	hereStyle          lipgloss.Style
}

func newLinePrinter(jsonOut bool, remoteTypes map[string]string) *linePrinter {
	p := &linePrinter{
		jsonOut:            jsonOut,
		remoteTypes:        remoteTypes,
		typeGitStyle:       lipgloss.NewStyle(),
		typeAnnexStyle:     lipgloss.NewStyle(),
		typeUntrackedStyle: lipgloss.NewStyle(),
		remoteStyle:        lipgloss.NewStyle(),
		hereStyle:          lipgloss.NewStyle(),
	}

	if !jsonOut && term.IsTerminal(int(os.Stdout.Fd())) {
		pp := palette.Current()
		p.typeGitStyle = p.typeGitStyle.Foreground(pp.Highlight.Adaptive())
		p.typeAnnexStyle = p.typeAnnexStyle.Foreground(pp.Label.Adaptive()).Bold(true)
		p.typeUntrackedStyle = p.typeUntrackedStyle.Foreground(pp.Dim.Adaptive())
		p.remoteStyle = p.remoteStyle.Foreground(pp.Dim.Adaptive())
		p.hereStyle = p.hereStyle.Foreground(pp.Highlight.Adaptive())
	}

	return p
}

const typeWidth = 9 // "untracked" is the longest label

func (p *linePrinter) printEntry(e fileEntry) {
	if p.jsonOut {
		data, err := json.Marshal(e)
		if err != nil {
			return
		}
		fmt.Println(string(data))
		return
	}

	var label string
	switch e.Type {
	case "git":
		label = p.typeGitStyle.Render(fmt.Sprintf("%-*s", typeWidth, "git"))
	case "untracked":
		label = p.typeUntrackedStyle.Render(fmt.Sprintf("%-*s", typeWidth, "untracked"))
	default:
		typeText := "annex"
		if e.Locked {
			typeText = "annex🔒"
		}
		displayWidth := lipgloss.Width(typeText)
		pad := typeWidth - displayWidth
		if pad < 0 {
			pad = 0
		}
		label = p.typeAnnexStyle.Render(typeText + strings.Repeat(" ", pad))
	}

	if e.Type == "annex" && len(e.Remotes) > 0 {
		var styledRemotes []string
		for _, r := range e.Remotes {
			if r == "here" {
				styledRemotes = append(styledRemotes, p.hereStyle.Render("*here"))
			} else {
				styledRemotes = append(styledRemotes, p.remoteStyle.Render(formatRemoteWithEmoji(r, p.remoteTypes)))
			}
		}
		fmt.Printf("%s  %s  %s\n", label, e.File, strings.Join(styledRemotes, p.remoteStyle.Render(", ")))
	} else {
		fmt.Printf("%s  %s\n", label, e.File)
	}
}

func requireBins(names ...string) error {
	missing := false
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			fmt.Fprintf(os.Stderr, "Required binary '%s' not found in PATH\n", name)
			missing = true
		}
	}
	if missing {
		return fmt.Errorf("missing binaries")
	}
	return nil
}

func requireAnnexInit() error {
	cmd := command("git", "config", "--get", "annex.uuid")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git-annex not initialized — run 'exo init' first")
	}
	return nil
}
