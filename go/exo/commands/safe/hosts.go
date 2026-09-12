//go:build !roche

package safe

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/safe"
)

func safeHostOptions(entryType string, styleDim lipgloss.Style) (string, []huh.Option[string]) {
	if entryType == safe.TypeSSHKey {
		return "SSH git host", []huh.Option[string]{
			huh.NewOption("GitHub — github.com", "github.com"),
			huh.NewOption(styleDim.Render("Enter your own host..."), ":custom:"),
		}
	}
	return "Git provider API host", []huh.Option[string]{
		huh.NewOption("GitHub — api.github.com", "api.github.com"),
		huh.NewOption(styleDim.Render("Enter your own host..."), ":custom:"),
	}
}
