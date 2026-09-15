package context

import (
	"sync"

	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/palette"
)

var initOnce sync.Once

func initStyles() {
	initOnce.Do(func() {
		p := palette.Current()
		TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(p.LabelAlt.Adaptive()).
			MarginBottom(1)

		SelectedStyle = lipgloss.NewStyle().
			Foreground(p.LabelAlt.Adaptive()).
			Bold(true).
			PaddingLeft(2)

		NormalStyle = lipgloss.NewStyle().
			Foreground(p.Dim.Adaptive()).
			PaddingLeft(4)

		DescStyle = lipgloss.NewStyle().
			Foreground(p.Dim.Adaptive()).
			PaddingLeft(4)

		ErrorStyle = lipgloss.NewStyle().
			Foreground(p.Error.Adaptive()).
			Bold(true)

		SuccessStyle = lipgloss.NewStyle().
			Foreground(p.Success.Adaptive()).
			Bold(true)

		HelpStyle = lipgloss.NewStyle().
			Foreground(p.Dim.Adaptive()).
			Italic(true).
			MarginTop(1)
	})
}

// These vars hold ANSI fallback values at init time, then get overwritten
// with palette values on first use via initStyles().
var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("170")).
			MarginBottom(1)

	SelectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("170")).
			Bold(true).
			PaddingLeft(2)

	NormalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			PaddingLeft(4)

	DescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			PaddingLeft(4)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")).
			Bold(true)

	HelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true).
			MarginTop(1)
)
