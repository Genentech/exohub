package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/palette"
)

// Theme allows consumers to customize the TUI's color scheme.
// Zero values mean "use default".
type Theme struct {
	Primary        palette.ColorPair
	PrimaryDim     palette.ColorPair
	PrimaryDull    palette.ColorPair
	PrimaryDimDull palette.ColorPair

	Success        palette.ColorPair
	SuccessDim     palette.ColorPair
	SuccessVeryDim palette.ColorPair

	Highlight palette.ColorPair

	LogoBg palette.ColorPair
	LogoFg palette.ColorPair

	StatusBarAccent   palette.ColorPair
	StatusBarAccentBg palette.ColorPair

	// HighlightBg/Fg are single values for glamour (no adaptive support).
	HighlightBg string
	HighlightFg string

	BadgeFg palette.ColorPair
}

// themeHighlightBg and themeHighlightFg store custom highlight colors
// for use in GlamourStyle.
var (
	themeHighlightBg string
	themeHighlightFg string
)

// ThemeFromPalette converts a palette.Palette to a TUI Theme.
func ThemeFromPalette(p *palette.Palette) Theme {
	highlightBg := p.SearchHighlightBg.Dark
	highlightFg := p.SearchHighlightFg.Dark
	if palette.CurrentMode() == palette.ModeLight {
		highlightBg = p.SearchHighlightBg.Light
		highlightFg = p.SearchHighlightFg.Light
	}
	return Theme{
		Primary:        p.Accent,
		PrimaryDim:     p.AccentDim,
		PrimaryDull:    p.AccentDull,
		PrimaryDimDull: p.AccentDull, // reuse AccentDull as dimmest
		Success:        p.Success,
		SuccessDim:     p.SuccessDim,
		SuccessVeryDim: p.SuccessDim, // reuse SuccessDim
		Highlight:      p.Highlight,
		LogoBg:         p.LogoBg,
		LogoFg:         p.LogoFg,
		StatusBarAccent:   p.StatusBarFg,
		StatusBarAccentBg: p.StatusBarBg,
		HighlightBg:    highlightBg,
		HighlightFg:    highlightFg,
		BadgeFg:        p.Badge,
	}
}

func isSet(c palette.ColorPair) bool {
	return c.Light != "" || c.Dark != ""
}

// ApplyTheme overrides the package-level style variables with the given theme.
// Call this before NewProgram if you want custom branding.
func ApplyTheme(t Theme) {
	if isSet(t.Primary) {
		fuchsia = t.Primary.Adaptive()
		fuchsiaFg = lipgloss.NewStyle().Foreground(fuchsia).Render
	}
	if isSet(t.PrimaryDim) {
		dimFuchsia = t.PrimaryDim.Adaptive()
		dimFuchsiaFg = lipgloss.NewStyle().Foreground(dimFuchsia).Render
	}
	if isSet(t.PrimaryDull) {
		dullFuchsia = t.PrimaryDull.Adaptive()
		dullFuchsiaFg = lipgloss.NewStyle().Foreground(dullFuchsia).Render
	}
	if isSet(t.PrimaryDimDull) {
		dimDullFuchsia = t.PrimaryDimDull.Adaptive()
		dimDullFuchsiaFg = lipgloss.NewStyle().Foreground(dimDullFuchsia).Render
	}
	if isSet(t.Success) {
		green = t.Success.Color()
		greenFg = lipgloss.NewStyle().Foreground(green).Render
	}
	if isSet(t.SuccessDim) {
		semiDimGreen = t.SuccessDim.Adaptive()
		semiDimGreenFg = lipgloss.NewStyle().Foreground(semiDimGreen).Render
	}
	if isSet(t.SuccessVeryDim) {
		dimGreen = t.SuccessVeryDim.Adaptive()
		dimGreenFg = lipgloss.NewStyle().Foreground(dimGreen).Render
	}
	if isSet(t.Highlight) {
		yellowGreen = t.Highlight.Adaptive()
		yellowGreenFg = lipgloss.NewStyle().Foreground(yellowGreen).Render
		activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Highlight.Adaptive()).
			Padding(0, 1)
	}

	// Logo and title badge
	if isSet(t.LogoBg) && isSet(t.LogoFg) {
		logoStyle = lipgloss.NewStyle().
			Foreground(t.LogoFg.Adaptive()).
			Background(t.LogoBg.Adaptive()).
			Bold(true)
	}

	// Pager status bar message styles
	if isSet(t.StatusBarAccent) && isSet(t.StatusBarAccentBg) {
		mintGreen = t.StatusBarAccent.Adaptive()
		darkGreen = t.StatusBarAccentBg.Adaptive()
		statusBarMessageStyle = lipgloss.NewStyle().
			Foreground(mintGreen).
			Background(darkGreen).
			Render
		statusBarMessageScrollPosStyle = lipgloss.NewStyle().
			Foreground(mintGreen).
			Background(darkGreen).
			Render
		statusBarMessageHelpStyle = lipgloss.NewStyle().
			Foreground(t.StatusBarAccent.Adaptive()).
			Background(green).
			Render
	}

	// Rebuild dependent styles that reference the base colors

	// Search input prompt and cursor
	stashInputPromptStyle = lipgloss.NewStyle().
		Foreground(green).
		MarginRight(1)
	stashInputCursorStyle = lipgloss.NewStyle().
		Foreground(fuchsia).
		MarginRight(1)

	// Search form title uses logo colors
	searchFormTitleStyle = lipgloss.NewStyle().
		Foreground(logoStyle.GetForeground()).
		Background(logoStyle.GetBackground()).
		Bold(true).
		Padding(0, 1)

	// Info title in pager
	infoTitleStyle = lipgloss.NewStyle().
		Foreground(logoStyle.GetForeground()).
		Bold(true)
	if isSet(t.Highlight) {
		infoTitleStyle = lipgloss.NewStyle().
			Foreground(t.Highlight.Adaptive()).
			Bold(true)
	}

	// Project@version badge
	if isSet(t.BadgeFg) {
		badgeProjectStyle = lipgloss.NewStyle().Foreground(t.BadgeFg.Adaptive())
		badgeVersionStyle = lipgloss.NewStyle().Foreground(t.BadgeFg.Adaptive())
	}

	// Search highlight colors in glamour-rendered content
	if t.HighlightBg != "" {
		themeHighlightBg = t.HighlightBg
	}
	if t.HighlightFg != "" {
		themeHighlightFg = t.HighlightFg
	}

	// Palette-derived styles
	p := palette.Current()
	if p != nil {
		badgeAtStyle = lipgloss.NewStyle().Foreground(p.Dim.Adaptive())

		codeFg = lipgloss.NewStyle().
			Foreground(p.CodeFg.Adaptive()).
			Background(p.CodeBg.Adaptive()).
			Padding(0, 1).Render

		toolbarBtnKeyStyle = lipgloss.NewStyle().
			Foreground(t.Primary.Adaptive()).
			Background(lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#383838"}).
			Bold(true)

		searchInputBgStyle = lipgloss.NewStyle().
			Background(p.CodeBg.Adaptive())

		schemaSelectedItemStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			Foreground(p.LabelAlt.Adaptive())

		dropdownLabelStyle = lipgloss.NewStyle().
			Foreground(p.Dim.Adaptive()).
			MarginRight(1)
		dropdownFocusedLabelStyle = lipgloss.NewStyle().
			Foreground(p.Accent.Adaptive()).
			MarginRight(1).
			Bold(true)

		searchFormSubtitleStyle = lipgloss.NewStyle().
			Foreground(p.Dim.Adaptive())

		// Download pane styles
		paneHeaderStyle = lipgloss.NewStyle().
			Foreground(p.Highlight.Adaptive()).
			Bold(true)
		paneItemDone = lipgloss.NewStyle().Foreground(p.Success.Adaptive())
		paneItemFailed = lipgloss.NewStyle().Foreground(p.Error.Adaptive())
		paneItemActive = lipgloss.NewStyle().Foreground(fuchsia).Bold(true)
		paneBarStyle = lipgloss.NewStyle().Foreground(p.Highlight.Adaptive())
		paneDimStyle = lipgloss.NewStyle().Foreground(p.Dim.Adaptive())

		// UI list styles
		selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(p.LabelAlt.Adaptive())
	}
}
