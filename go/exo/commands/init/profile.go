package init

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/Genentech/exohub/go/exo/branding"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/internal/defaults"
)

const (
	// profileBodyLimit is the maximum response size for a single profile (10 MiB).
	profileBodyLimit = 10 << 20
	// profileListBodyLimit is the maximum response size for the profiles list (1 MiB).
	profileListBodyLimit = 1 << 20
)

// ProfileSpec is the server-side profile definition returned by GET /api/profiles/{name}.
type ProfileSpec struct {
	Name      string            `json:"name"`
	Vars      map[string]string `json:"vars"`
	Prompts   []PromptSpec      `json:"prompts"`
	Templates map[string]string `json:"templates"`
	Actions   []ActionSpec      `json:"actions"`
}

// ProfileSummary is a single entry in GET /api/profiles.
type ProfileSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PromptSpec defines a single prompt entry in the profile.
// Field names match the server API: key/question/type.
type PromptSpec struct {
	Name    string          `json:"key"`      // server field: "key"
	Kind    string          `json:"type"`     // server field: "type" — "bool", "string", "choice"
	Label   string          `json:"question"` // server field: "question"
	Default json.RawMessage `json:"default"`  // may be JSON bool, string, number, or null
	Choices []string        `json:"choices"`  // only for kind=choice
}

// defaultString returns the Default field as a string regardless of its JSON type.
// JSON booleans → "true"/"false"; JSON strings → unquoted; null/missing → "".
func (p PromptSpec) defaultString() string {
	if len(p.Default) == 0 {
		return ""
	}
	// Try bool first (handles JSON false/true without quotes)
	var b bool
	if err := json.Unmarshal(p.Default, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}
	// Try string
	var s string
	if err := json.Unmarshal(p.Default, &s); err == nil {
		return s
	}
	// Fallback: strip quotes if present
	return strings.Trim(string(p.Default), `"`)
}

// ActionSpec defines a single action in the profile.
type ActionSpec struct {
	Type    string `json:"type"`     // e.g. "create_repo"
	When    string `json:"when"`     // variable name or Go template expression; empty = always run
	RepoURL string `json:"repo_url"` // for create_repo
}

// profileTplData is the flat map passed to all Go templates in a profile.
// It contains: UnixID, Folder, all Vars.* keys, all Env.* keys, and all
// prompt answers — all at the top level so templates can use .UnixID,
// .read_access, .create_repo etc. directly without a namespace prefix.
type profileTplData map[string]interface{}

// buildTplData assembles the flat template data map.
func buildTplData(spec *ProfileSpec, unixID, folder string, env map[string]string, answers map[string]interface{}) profileTplData {
	d := make(profileTplData)
	// Built-ins
	d["UnixID"] = unixID
	d["Folder"] = folder
	// Profile vars
	for k, v := range spec.Vars {
		d[k] = v
	}
	// EXOHUB_TPL_* env vars
	for k, v := range env {
		d[k] = v
	}
	// Prompt answers (override vars if same key)
	for k, v := range answers {
		d[k] = v
	}
	return d
}

// resolveUnixID returns the JWT preferred_username if available, else OS username.
func resolveUnixID() string {
	if name := usernameFromToken(); name != "" {
		return name
	}
	u, err := user.Current()
	if err == nil {
		return u.Username
	}
	return ""
}

// resolveEnv returns a map of EXOHUB_TPL_<NAME> → value from the environment.
func resolveEnv() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		const prefix = "EXOHUB_TPL_"
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		env[kv[len(prefix):idx]] = kv[idx+1:]
	}
	return env
}

// runProfilePrompts executes the profile prompts block.
// Answers are written into the answers map keyed by prompt Name (server field "key").
// If skipInteractive is true (--yes), defaults are used without prompting.
func runProfilePrompts(spec *ProfileSpec, answers map[string]interface{}, skipInteractive bool) error {
	for _, p := range spec.Prompts {
		switch p.Kind {
		case "bool":
			def := p.defaultString() == "true"
			if skipInteractive {
				answers[p.Name] = def
				continue
			}
			val := def
			f := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title(p.Label).
					Value(&val),
			)).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
			if err := f.Run(); err != nil {
				return fmt.Errorf("prompt %q: %w", p.Name, err)
			}
			answers[p.Name] = val

		case "string":
			val := p.defaultString()
			if skipInteractive {
				answers[p.Name] = val
				continue
			}
			f := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title(p.Label).
					Value(&val),
			)).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
			if err := f.Run(); err != nil {
				return fmt.Errorf("prompt %q: %w", p.Name, err)
			}
			answers[p.Name] = val

		case "choice":
			if len(p.Choices) == 0 {
				return fmt.Errorf("prompt %q: choice kind requires at least one choice", p.Name)
			}
			chosen := p.defaultString()
			if chosen == "" {
				chosen = p.Choices[0]
			}
			if skipInteractive {
				if !isValidChoice(chosen, p.Choices) {
					return fmt.Errorf("prompt %q: default %q is not a valid choice", p.Name, chosen)
				}
				answers[p.Name] = chosen
				continue
			}
			opts := make([]huh.Option[string], len(p.Choices))
			for i, c := range p.Choices {
				opts[i] = huh.NewOption(c, c)
			}
			f := huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title(p.Label).
					Options(opts...).
					Value(&chosen).
					Validate(func(v string) error {
						if !isValidChoice(v, p.Choices) {
							return fmt.Errorf("%q is not a valid choice", v)
						}
						return nil
					}),
			)).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
			if err := f.Run(); err != nil {
				return fmt.Errorf("prompt %q: %w", p.Name, err)
			}
			answers[p.Name] = chosen

		default:
			return fmt.Errorf("prompt %q: unknown kind %q (must be bool, string, or choice)", p.Name, p.Kind)
		}
	}
	return nil
}

func isValidChoice(val string, choices []string) bool {
	for _, c := range choices {
		if c == val {
			return true
		}
	}
	return false
}

// renderTemplate renders a Go text/template string with the flat data map.
func renderTemplate(name, tmplText string, data profileTplData) (string, error) {
	t, err := template.New(name).Option("missingkey=zero").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("template %q parse error: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template %q render error: %w", name, err)
	}
	return buf.String(), nil
}

// validateRenderedFile attempts to parse rendered content as valid YAML.
// For known .exohub/* files it also validates the structure.
func validateRenderedFile(filename, content string) error {
	base := filepath.Base(filename)
	switch base {
	case "remotes":
		// Server may send a list ([]RemoteConfig) or a map (RemotesConfig)
		var raw interface{}
		if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
			return fmt.Errorf("rendered %q is not valid YAML: %w", filename, err)
		}
	case "permissions":
		var cfg permissionsConfig
		if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
			return fmt.Errorf("rendered %q does not parse as permissionsConfig: %w", filename, err)
		}
	case "config":
		var cfg ExohubConfig
		if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
			return fmt.Errorf("rendered %q does not parse as ExohubConfig: %w", filename, err)
		}
	default:
		var raw interface{}
		if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
			return fmt.Errorf("rendered %q is not valid YAML: %w", filename, err)
		}
	}
	return nil
}

// renderAndWrite renders all profile templates and writes them to .exohub/.
// dryRun prints content without writing; force bypasses the .exohub/ existence guard.
func renderAndWrite(spec *ProfileSpec, data profileTplData, dryRun, force bool) error {
	if len(spec.Templates) == 0 {
		return nil
	}

	if _, err := os.Stat(".exohub"); err == nil && !force {
		return fmt.Errorf(".exohub/ already exists; use --force to overwrite")
	}

	rendered := make(map[string]string, len(spec.Templates))
	for filename, tmplText := range spec.Templates {
		content, err := renderTemplate(filename, tmplText, data)
		if err != nil {
			return err
		}
		if err := validateRenderedFile(filename, content); err != nil {
			return err
		}
		rendered[filename] = content
	}

	if dryRun {
		fmt.Println("==> Dry-run: rendered files (not written)")
		for filename, content := range rendered {
			fmt.Printf("\n--- %s ---\n%s", filename, content)
		}
		return nil
	}

	if err := os.MkdirAll(".exohub", 0755); err != nil {
		return fmt.Errorf("failed to create .exohub/: %w", err)
	}
	for filename, content := range rendered {
		dest := filepath.Join(".exohub", filepath.Base(filename))
		if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", dest, err)
		}
		fmt.Printf("✅ Wrote %s\n", dest)
	}
	return nil
}

// evalWhenGuard evaluates a when guard.
// The guard can be a bare variable name (e.g. "create_repo") or a Go template
// boolean expression (e.g. ".create_repo"). Empty guard always returns true.
func evalWhenGuard(when string, data profileTplData) (bool, error) {
	if when == "" {
		return true, nil
	}
	// Support bare variable names without leading dot (server convention)
	expr := when
	if !strings.HasPrefix(expr, ".") && !strings.ContainsAny(expr, " \t(}") {
		expr = "." + expr
	}
	src := `{{if ` + expr + `}}true{{else}}false{{end}}`
	t, err := template.New("when").Option("missingkey=zero").Parse(src)
	if err != nil {
		return false, fmt.Errorf("when guard %q parse error: %w", when, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("when guard %q eval error: %w", when, err)
	}
	result := buf.String()
	// A bool false value renders as "false"; anything truthy (non-empty, non-zero) is true
	return result == "true", nil
}

// runProfileActions executes the profile's actions in order.
func runProfileActions(spec *ProfileSpec, data profileTplData, dryRun bool) error {
	for _, action := range spec.Actions {
		run, err := evalWhenGuard(action.When, data)
		if err != nil {
			return fmt.Errorf("action %q when guard: %w", action.Type, err)
		}
		if !run {
			fmt.Printf("⏭️  Skipping action %q (when guard is false)\n", action.Type)
			continue
		}

		switch action.Type {
		case "create_repo":
			repoURL := action.RepoURL
			if repoURL != "" {
				rendered, err := renderTemplate("repo_url", repoURL, data)
				if err != nil {
					return fmt.Errorf("action create_repo repo_url: %w", err)
				}
				repoURL = rendered
			}
			if dryRun {
				fmt.Printf("==> Dry-run: would run create_repo action (url=%s)\n", repoURL)
				continue
			}
			fmt.Printf("==> Running action: create_repo (url=%s)\n", repoURL)
			var urlArgs []string
			if repoURL != "" {
				urlArgs = []string{repoURL}
			}
			exohubConfig, err := LoadExohubConfig()
			if err != nil {
				return err
			}
			remotesConfig, err := LoadRemotesConfig()
			if err != nil {
				return err
			}
			if err := handleRepoCreation(exohubConfig, remotesConfig, urlArgs); err != nil {
				return fmt.Errorf("action create_repo: %w", err)
			}
		default:
			return fmt.Errorf("unknown action type %q", action.Type)
		}
	}
	return nil
}

// fetchProfile fetches a named profile from the ExoHub API.
func fetchProfile(name string) (*ProfileSpec, error) {
	apiBase := strings.TrimSuffix(defaults.APIBase(), "/")
	reqURL := fmt.Sprintf("%s/profiles/%s", apiBase, url.PathEscape(name))
	commandutil.DebugHTTP("GET", reqURL)

	resp, err := http.Get(reqURL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("fetch profile %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch profile %q: server returned %s", name, resp.Status)
	}

	var spec ProfileSpec
	if err := json.NewDecoder(io.LimitReader(resp.Body, profileBodyLimit)).Decode(&spec); err != nil {
		return nil, fmt.Errorf("fetch profile %q: decode: %w", name, err)
	}
	return &spec, nil
}

// listProfiles fetches all available profiles from the ExoHub API.
// Returns nil slice (not an error) if the server returns 404 or the list is empty.
func listProfiles() ([]ProfileSummary, error) {
	apiBase := strings.TrimSuffix(defaults.APIBase(), "/")
	reqURL := fmt.Sprintf("%s/profiles", apiBase)
	commandutil.DebugHTTP("GET", reqURL)

	resp, err := http.Get(reqURL) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list profiles: server returned %s", resp.Status)
	}

	var summaries []ProfileSummary
	if err := json.NewDecoder(io.LimitReader(resp.Body, profileListBodyLimit)).Decode(&summaries); err != nil {
		return nil, fmt.Errorf("list profiles: decode: %w", err)
	}
	return summaries, nil
}

var profileNameStyle = lipgloss.NewStyle().Foreground(branding.GradientOrange).Bold(true)

// selectProfileInteractive shows an interactive picker and returns the chosen name.
// Profile names are rendered in ExoHub orange; descriptions use the default color.
func selectProfileInteractive(summaries []ProfileSummary) (string, error) {
	opts := make([]huh.Option[string], len(summaries))
	for i, s := range summaries {
		// Build label with ANSI-styled name. huh renders option labels as plain
		// strings so lipgloss ANSI codes pass through to the terminal.
		var label string
		if s.Description != "" {
			label = profileNameStyle.Render(s.Name) + " — " + s.Description
		} else {
			label = profileNameStyle.Render(s.Name)
		}
		opts[i] = huh.NewOption(label, s.Name)
	}
	var chosen string
	f := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Select a profile").
			Options(opts...).
			Value(&chosen),
	)).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := f.Run(); err != nil {
		return "", err
	}
	return chosen, nil
}

// runProfileInit is the main entry point for the --profile / --profile-select flow.
// If profileName is empty, shows an interactive picker (--profile-select).
// summaries may be pre-fetched to avoid a double HTTP call.
func runProfileInit(profileName string, args []string, summaries []ProfileSummary) error {
	var spec *ProfileSpec

	if profileName == "" {
		// --profile-select: show interactive picker
		var err error
		if summaries == nil {
			summaries, err = listProfiles()
			if err != nil {
				return err
			}
		}
		if len(summaries) == 0 {
			return fmt.Errorf("no profiles available")
		}
		var chosen string
		if flagYes {
			chosen = summaries[0].Name
			fmt.Printf("==> Auto-selecting profile %q (--yes)\n", chosen)
		} else {
			chosen, err = selectProfileInteractive(summaries)
			if err != nil {
				return err
			}
		}
		spec, err = fetchProfile(chosen)
		if err != nil {
			return err
		}
	} else {
		fetched, err := fetchProfile(profileName)
		if err != nil {
			// Provide helpful error listing available profiles
			var list []ProfileSummary
			if summaries != nil {
				list = summaries
			} else {
				list, _ = listProfiles()
			}
			if len(list) > 0 {
				names := make([]string, len(list))
				for i, s := range list {
					names[i] = s.Name
				}
				return fmt.Errorf("%w\nAvailable profiles: %s", err, strings.Join(names, ", "))
			}
			return err
		}
		spec = fetched
	}

	fmt.Printf("==> Profile: %s\n", spec.Name)

	// Resolve identity and environment
	unixID := resolveUnixID()
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("could not determine working directory: %w", err)
	}
	folder := filepath.Base(cwd)
	env := resolveEnv()

	// Run prompts; collect answers
	answers := make(map[string]interface{})
	if err := runProfilePrompts(spec, answers, flagYes); err != nil {
		return err
	}

	// Build flat template data map
	data := buildTplData(spec, unixID, folder, env, answers)

	// Render and write files
	if err := renderAndWrite(spec, data, flagDryRun, flagForce); err != nil {
		return err
	}

	// Run actions
	if err := runProfileActions(spec, data, flagDryRun); err != nil {
		return err
	}

	return nil
}
