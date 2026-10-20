package add

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"

	initcmd "github.com/Genentech/exohub/go/exo/commands/init"
	"github.com/Genentech/exohub/go/exo/internal/pathglob"
)

// Backend identifies where a file's content is stored.
type Backend int

const (
	Git Backend = iota
	Annex
)

func (b Backend) String() string {
	if b == Annex {
		return "annex"
	}
	return "git"
}

// RuleKind identifies which part of the evaluation order (design D3)
// produced a Decision.
type RuleKind int

const (
	// KindProtected is a built-in protected rule (.exohub/**, .gitignore,
	// .gitattributes); it always wins and is never overridable.
	KindProtected RuleKind = iota
	// KindUser is a user-configured annex.routing rule.
	KindUser
	// KindDefault is the built-in fallback that applies when no protected
	// or user rule matched.
	KindDefault
)

// MatchedRule describes why a routing Decision was made: a user rule
// (Kind == KindUser, Index > 0, e.g. "rule 2: **/*.csv larger_than 1KB"),
// a built-in protected rule (e.g. "protected: .exohub/**"), or the default
// fallback (e.g. "default: binary"). LargeTextFallback is set only for the
// specific default-fallback case design D3/task 2.3 keep a TTY prompt for:
// text content at or above the fallback threshold with no matching rule.
type MatchedRule struct {
	Kind              RuleKind
	Index             int
	Description       string
	LargeTextFallback bool
}

func (m MatchedRule) String() string {
	if m.Kind == KindUser {
		return fmt.Sprintf("rule %d: %s", m.Index, m.Description)
	}
	return m.Description
}

// Decision is the outcome of routing a single file.
type Decision struct {
	Backend Backend
	Rule    MatchedRule
}

// protectedPatterns are evaluated before any user rule and always route to
// git; user rules cannot override them (design D3).
var protectedPatterns = []string{".exohub/**", ".gitignore", ".gitattributes"}

// IsProtectedPath reports whether relPath matches one of the built-in
// protected patterns (design D3). Protected paths always route to git and
// are never moved by `exo add --reroute` (design D6), regardless of their
// current backend.
func IsProtectedPath(relPath string) (bool, error) {
	relPath = filepath.ToSlash(relPath)
	for _, pattern := range protectedPatterns {
		ok, err := pathglob.Match(pattern, relPath)
		if err != nil {
			return false, fmt.Errorf("protected pattern %q: %w", pattern, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// compiledRule is a RoutingRule with its sizes and destination pre-parsed
// by Validate, so Route never fails because of a malformed rule: all
// validation happens once, at NewRouter time. raw is kept so rule
// descriptions (and, later, --explain) echo the config's own strings
// (e.g. "1KB") rather than a re-rendered size.
type compiledRule struct {
	index       int
	raw         initcmd.RoutingRule
	hasLarger   bool
	largerThan  uint64
	hasSmaller  bool
	smallerThan uint64
	to          Backend
}

func (c compiledRule) description() string {
	var parts []string
	if c.raw.Path != "" {
		parts = append(parts, c.raw.Path)
	}
	if c.raw.LargerThan != "" {
		parts = append(parts, fmt.Sprintf("larger_than %s", c.raw.LargerThan))
	}
	if c.raw.SmallerThan != "" {
		parts = append(parts, fmt.Sprintf("smaller_than %s", c.raw.SmallerThan))
	}
	if c.raw.Content != "" {
		parts = append(parts, fmt.Sprintf("content %s", c.raw.Content))
	}
	return strings.Join(parts, " ")
}

// matches evaluates all match keys present on the rule (logical AND).
// content, if present, is read lazily via sniffContent so files that no
// rule cares about are never opened.
func (c compiledRule) matches(relPath string, info fs.FileInfo, sniffContent func() (string, error)) (bool, error) {
	if c.raw.Path != "" {
		ok, err := pathglob.Match(c.raw.Path, relPath)
		if err != nil {
			return false, fmt.Errorf("annex.routing rule %d: %w", c.index, err)
		}
		if !ok {
			return false, nil
		}
	}
	if info == nil && (c.hasLarger || c.hasSmaller) {
		return false, fmt.Errorf("file size unavailable")
	}
	var size uint64
	if info != nil {
		size = uint64(info.Size())
	}
	if c.hasLarger && size < c.largerThan {
		return false, nil
	}
	if c.hasSmaller && size >= c.smallerThan {
		return false, nil
	}
	if c.raw.Content != "" {
		got, err := sniffContent()
		if err != nil {
			return false, err
		}
		if got != c.raw.Content {
			return false, nil
		}
	}
	return true, nil
}

// Router evaluates annex.routing rules, the built-in protected rules, and
// the default fallback to decide whether an untracked file should be added
// to git or to git-annex (design D3/D4).
type Router struct {
	rules     []compiledRule
	threshold uint64
	// root is the repository root content sniffing is resolved against.
	// Route's relPath is repo-relative (design D2) for glob matching, but
	// opening the file for a content sniff needs an actual filesystem
	// path. If root is empty, relPath is opened as-is (relative to the
	// current directory), which only works when the caller's cwd is
	// already the repo root.
	root string
}

// NewRouter builds a Router from a loaded .exohub/config (nil, or a nil
// Annex, is treated as "no rules configured"), a fallback threshold
// override, and the repository root (used to resolve relPath to a real
// filesystem path for content sniffing; see Route). thresholdOverride,
// typically the --large-text-threshold flag value, takes precedence over
// EXOHUB_LARGE_TEXT_THRESHOLD, which takes precedence over the built-in
// 5 MiB default. The override affects only the default fallback (design
// D3); it never changes the outcome of an explicit rule.
func NewRouter(cfg *initcmd.ExohubConfig, thresholdOverride string, repoRoot string) (*Router, error) {
	threshold, err := resolveThreshold(thresholdOverride)
	if err != nil {
		return nil, err
	}

	var rules []compiledRule
	if cfg != nil && cfg.Annex != nil {
		for i, rr := range cfg.Annex.Routing {
			cr, err := compileRule(i+1, rr)
			if err != nil {
				return nil, err
			}
			rules = append(rules, cr)
		}
	}

	return &Router{rules: rules, threshold: threshold, root: repoRoot}, nil
}

func compileRule(index int, rr initcmd.RoutingRule) (compiledRule, error) {
	parsed, err := rr.Validate()
	if err != nil {
		return compiledRule{}, fmt.Errorf("annex.routing rule %d: %w", index, err)
	}

	cr := compiledRule{
		index:       index,
		raw:         rr,
		hasLarger:   parsed.HasLarger,
		largerThan:  parsed.LargerThan,
		hasSmaller:  parsed.HasSmaller,
		smallerThan: parsed.SmallerThan,
	}
	switch rr.To {
	case "git":
		cr.to = Git
	case "annex":
		cr.to = Annex
	}
	return cr, nil
}

// resolveThreshold implements the fallback threshold precedence from
// design D3: override flag > EXOHUB_LARGE_TEXT_THRESHOLD > built-in default.
func resolveThreshold(override string) (uint64, error) {
	if override != "" {
		v, err := humanize.ParseBytes(override)
		if err != nil {
			return 0, fmt.Errorf("invalid --large-text-threshold value %q: %w", override, err)
		}
		return v, nil
	}
	if env := os.Getenv("EXOHUB_LARGE_TEXT_THRESHOLD"); env != "" {
		v, err := humanize.ParseBytes(env)
		if err != nil {
			return 0, fmt.Errorf("invalid EXOHUB_LARGE_TEXT_THRESHOLD value %q: %w", env, err)
		}
		return v, nil
	}
	return uint64(largeTextFileThreshold), nil
}

// sniffIsText reads the first 512 bytes of path and reports whether
// http.DetectContentType classifies it as text/*. It mirrors
// detectMimeType/classifySingleFile's sniff (empty files are text).
func sniffIsText(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if n == 0 {
		return true, nil
	}
	mime := http.DetectContentType(buf[:n])
	return strings.HasPrefix(mime, "text/"), nil
}

// Route decides whether relPath should be added to git or to git-annex,
// evaluating in order: built-in protected rules, user rules (first match
// wins), then the default fallback (design D3). relPath must be
// repo-relative, since that's what glob patterns are matched against
// (design D2); it is resolved to an openable filesystem path by joining
// it with the Router's root (see NewRouter) only when content needs to be
// sniffed. info is used for size checks; content is sniffed lazily, only
// if a rule or the fallback needs it.
func (rt *Router) Route(relPath string, info fs.FileInfo) (Decision, error) {
	return rt.routeContent(relPath, info, nil)
}

// routeContent preserves lazy evaluation when annex metadata is available but
// local content is not. unavailable is returned only if a sniff is needed.
func (rt *Router) routeContent(relPath string, info fs.FileInfo, unavailable error) (Decision, error) {
	relPath = filepath.ToSlash(relPath)

	for _, pattern := range protectedPatterns {
		ok, err := pathglob.Match(pattern, relPath)
		if err != nil {
			return Decision{}, fmt.Errorf("protected pattern %q: %w", pattern, err)
		}
		if ok {
			return Decision{Backend: Git, Rule: MatchedRule{Kind: KindProtected, Description: "protected: " + pattern}}, nil
		}
	}

	openPath := relPath
	if rt.root != "" {
		openPath = filepath.Join(rt.root, relPath)
	}

	var sniffed bool
	var isText bool
	sniffContent := func() (string, error) {
		if !sniffed {
			if unavailable != nil {
				return "", unavailable
			}
			t, err := sniffIsText(openPath)
			if err != nil {
				return "", err
			}
			isText, sniffed = t, true
		}
		if isText {
			return "text", nil
		}
		return "binary", nil
	}

	for _, r := range rt.rules {
		ok, err := r.matches(relPath, info, sniffContent)
		if err != nil {
			return Decision{}, err
		}
		if ok {
			return Decision{Backend: r.to, Rule: MatchedRule{Kind: KindUser, Index: r.index, Description: r.description()}}, nil
		}
	}

	content, err := sniffContent()
	if err != nil {
		return Decision{}, err
	}
	if content == "binary" {
		return Decision{Backend: Annex, Rule: MatchedRule{Kind: KindDefault, Description: "default: binary"}}, nil
	}
	if info == nil {
		return Decision{}, fmt.Errorf("file size unavailable")
	}
	if uint64(info.Size()) >= rt.threshold {
		return Decision{Backend: Annex, Rule: MatchedRule{Kind: KindDefault, Description: "default: text at or above the fallback threshold", LargeTextFallback: true}}, nil
	}
	return Decision{Backend: Git, Rule: MatchedRule{Kind: KindDefault, Description: "default: text"}}, nil
}
