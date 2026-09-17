package palette

import (
	"os"
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// ColorPair holds hex color values for light and dark terminal backgrounds.
type ColorPair struct {
	Light string
	Dark  string
}

// Adaptive returns a lipgloss.AdaptiveColor that automatically picks
// the right variant based on detected terminal background.
func (c ColorPair) Adaptive() lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: c.Light, Dark: c.Dark}
}

// Color returns a single lipgloss.Color for the currently resolved mode.
func (c ColorPair) Color() lipgloss.Color {
	if currentMode == ModeDark {
		return lipgloss.Color(c.Dark)
	}
	return lipgloss.Color(c.Light)
}

// Mode represents the terminal background color scheme.
type Mode int

const (
	ModeDark Mode = iota
	ModeLight
)

// Palette defines the full set of semantic color roles for a theme.
type Palette struct {
	Name        string
	Description string

	Accent    ColorPair // Primary accent (selection, cursor, active items)
	AccentDim ColorPair // Muted accent for secondary emphasis
	AccentDull ColorPair // Subdued accent for tertiary emphasis

	Success    ColorPair // Positive status
	SuccessDim ColorPair // Muted success

	Error   ColorPair // Error/failure states
	Warning ColorPair // Caution states

	Highlight ColorPair // Active/highlighted text, active tabs

	Label    ColorPair // Section/field labels
	LabelAlt ColorPair // Alternate label color (secondary sections)

	Dim ColorPair // De-emphasized text (metadata, timestamps, help)

	Badge ColorPair // Inline badge text (project@version)

	ProgressFilled ColorPair // Filled progress indicator
	ProgressEmpty  ColorPair // Empty progress indicator

	CodeFg ColorPair // Inline code foreground
	CodeBg ColorPair // Inline code background

	LogoBg ColorPair // Instance badge background
	LogoFg ColorPair // Instance badge foreground

	StatusBarFg ColorPair // Status bar foreground
	StatusBarBg ColorPair // Status bar background

	SearchHighlightFg ColorPair // Search term highlight foreground
	SearchHighlightBg ColorPair // Search term highlight background
}

var (
	current     *Palette
	currentMode Mode
)

// Registry maps theme names to palettes.
var Registry = map[string]*Palette{
	"exohub":  &ExoHub,
	"patsica": &Patsica,
	"mono":    &Mono,
}

// Init resolves the active theme and mode. Call once from PersistentPreRunE
// before any rendering. Safe to call multiple times.
func Init() {
	current = Resolve()
	currentMode = resolveMode()
}

// Current returns the resolved palette. Falls back to ExoHub if Init was not called.
func Current() *Palette {
	if current == nil {
		return &ExoHub
	}
	return current
}

// CurrentMode returns the resolved mode (dark or light).
func CurrentMode() Mode {
	return currentMode
}

// Resolve determines the active palette from environment and preferences.
// Precedence: EXO_THEME env > preferences file > "exohub".
func Resolve() *Palette {
	name := os.Getenv("EXO_THEME")
	if name == "" {
		name = loadThemePreference()
	}
	if name == "" {
		name = "exohub"
	}
	p := Get(name)
	if p == nil {
		return &ExoHub
	}
	return p
}

// resolveMode determines dark/light from environment, preferences, and terminal detection.
// Precedence: EXO_THEME_MODE env > preferences > auto-detect.
func resolveMode() Mode {
	modeStr := os.Getenv("EXO_THEME_MODE")
	if modeStr == "" {
		modeStr = loadThemeModePreference()
	}
	switch modeStr {
	case "dark":
		return ModeDark
	case "light":
		return ModeLight
	default:
		if lipgloss.HasDarkBackground() {
			return ModeDark
		}
		return ModeLight
	}
}

// loadThemePreference reads the theme from the preferences file.
var loadThemePreference = func() string { return "" }

// loadThemeModePreference reads the theme mode from the preferences file.
var loadThemeModePreference = func() string { return "" }

// SetThemeLoader allows the preferences package to register its loader
// without creating an import cycle.
func SetThemeLoader(fn func() string) {
	loadThemePreference = fn
}

// SetThemeModeLoader allows the preferences package to register its mode loader.
func SetThemeModeLoader(fn func() string) {
	loadThemeModePreference = fn
}

// ThemeNames returns sorted names of all registered themes.
func ThemeNames() []string {
	names := make([]string, 0, len(Registry))
	for name := range Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns the palette for a theme name, or nil if unknown.
func Get(name string) *Palette {
	return Registry[name]
}
