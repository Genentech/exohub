package commandutil

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/palette"
)

// Legacy color vars for backward compatibility with package-level initializers.
// These provide reasonable defaults before palette.Init() runs.
// New code should use palette.Current() directly.
var (
	ColorOrange     = lipgloss.Color("209")
	ColorDarkOrange = lipgloss.Color("#FF6B35")
	ColorPink       = lipgloss.Color("205")
	ColorDim        = lipgloss.Color("242")
)

// RemoteTypeEmoji returns the emoji for a remote type (annex, export, import, exospace, artifactdb).
func RemoteTypeEmoji(remoteType string) string {
	switch remoteType {
	case "annex":
		return "📦"
	case "export":
		return "🔴"
	case "import":
		return "🟢"
	case "exospace":
		return "📁"
	case "artifactdb":
		return "💎"
	case "drive":
		return "☁️"
	default:
		return "  "
	}
}

// ExoTheme returns a huh theme with colors from the active palette.
func ExoTheme() *huh.Theme {
	p := palette.Current()
	t := huh.ThemeCharm()
	t.Focused.Title = t.Focused.Title.Foreground(p.Label.Adaptive()).Bold(true)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(p.Accent.Adaptive())
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(p.Accent.Adaptive())
	t.Focused.Option = t.Focused.Option.Foreground(p.Accent.Adaptive())
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(p.Dim.Adaptive())
	t.Blurred.Title = t.Blurred.Title.Foreground(p.Label.Adaptive())
	return t
}

// ExoKeyMap returns a huh keymap that adds Escape as an abort key
// (in addition to the default ctrl+c).
func ExoKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

// ExoFormOptions returns bubbletea program options for ExoHub forms.
// Uses the alternate screen buffer so each form renders on a clean screen.
func ExoFormOptions() []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithAltScreen()}
}
