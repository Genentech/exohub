package branding

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ExoHub branding gradient colors (Orange → Pink → Purple)
// These colors are derived from the ExoHub brand identity and
// should be used consistently across all terminal output.
var (
	// GradientOrange is the warm orange from the ExoHub gradient (#FF6B35)
	GradientOrange = lipgloss.Color("#FF6B35")

	// GradientPink is the vibrant pink from the ExoHub gradient (#E84393)
	GradientPink = lipgloss.Color("#E84393")

	// GradientPurple is the deep purple from the ExoHub gradient (#9B59B6)
	GradientPurple = lipgloss.Color("#9B59B6")
)

// Style helpers using branding colors
var (
	// HeaderStyle creates a styled header with the orange branding color
	HeaderStyle = lipgloss.NewStyle().
		Foreground(GradientOrange).
		Bold(true)

	// AccentStyle creates an accented style with the pink branding color
	AccentStyle = lipgloss.NewStyle().
		Foreground(GradientPink)

	// SubtleStyle creates a subtle/faded style with the purple branding color
	SubtleStyle = lipgloss.NewStyle().
		Foreground(GradientPurple).
		Faint(true)
)

// ExoHubBanner returns a big ASCII art "ExoHub" with straight, bold lines
func ExoHubBanner(version string) string {
	var b strings.Builder

	// Styles
	orangeStyle := lipgloss.NewStyle().Foreground(GradientOrange).Bold(true)
	purpleStyle := lipgloss.NewStyle().Foreground(GradientPurple).Bold(true)
	grayStyle := lipgloss.NewStyle().Foreground(GradientPink).Bold(true)

	// ASCII art with straight, bold lines - "Exo" in orange, "Hub" in purple
	line1 := orangeStyle.Render("███████╗                 ") + purpleStyle.Render("██╗  ██╗       ██╗   ")
	line2 := orangeStyle.Render("██╔════╝██╗  ██╗ ██████╗ ") + purpleStyle.Render("██║  ██║██╗ ██╗██║   ")
	line3 := orangeStyle.Render("█████╗  ╚██╗██╔╝██╔═══██╗") + purpleStyle.Render("███████║██║ ██║██████╗ ")
	line4 := orangeStyle.Render("██╔══╝  ██╔╝╚██╗██║   ██║") + purpleStyle.Render("██╔══██║██║ ██║██╔══██╗")
	line5 := orangeStyle.Render("███████╗██║  ██║╚██████╔╝") + purpleStyle.Render("██║  ██║╚████╔╝██████╔╝")
	line6 := orangeStyle.Render("╚══════╝╚═╝  ╚═╝ ╚═════╝ ") + purpleStyle.Render("╚═╝  ╚═╝ ╚═══╝ ╚═════╝ ")

	b.WriteString("\n")
	b.WriteString(line1 + "\n")
	b.WriteString(line2 + "\n")
	b.WriteString(line3 + "\n")
	b.WriteString(line4 + "\n")
	b.WriteString(line5 + "\n")
	b.WriteString(line6 + "\n")
	b.WriteString(grayStyle.Render(version) + "\n")

	return b.String()
}

// Logo returns the ExoHub ASCII art logo with branded colors and version
func Logo(version string) string {
	var b strings.Builder

	// Styles - all bold
	orangeStyle := lipgloss.NewStyle().Foreground(GradientOrange).Bold(true)
	purpleStyle := lipgloss.NewStyle().Foreground(GradientPurple).Bold(true)
	pinkStyle := lipgloss.NewStyle().Foreground(GradientPink).Bold(true)
	grayStyle := lipgloss.NewStyle().Foreground(GradientPink).Bold(true)

	// Logo components - clean outline letters with colors, no space between Exo and Hub
	logoLine1 := pinkStyle.Render("   ___") + "        " + orangeStyle.Render("___") + "           " + purpleStyle.Render("_  _") + "       " + purpleStyle.Render("_")
	logoLine2 := pinkStyle.Render("  /   \\") + "      " + orangeStyle.Render("| __|_ __ ___") + " " + purpleStyle.Render("| || |_  _ | |__")
	logoLine3 := pinkStyle.Render(" /") + " " + orangeStyle.Render("▲ ▲") + " " + pinkStyle.Render("\\") + "     " + orangeStyle.Render("| _| \\ \\ / _ \\") + purpleStyle.Render("| __ | || | '_ \\")
	logoLine4 := pinkStyle.Render(" \\") + " " + orangeStyle.Render("▼ ▼") + " " + pinkStyle.Render("/") + "     " + orangeStyle.Render("|___|_\\_\\___/") + purpleStyle.Render("|_||_|\\_,_|_.__/")
	logoLine5 := pinkStyle.Render("  \\___/") + "      " + grayStyle.Render(version)

	b.WriteString(logoLine1 + "\n")
	b.WriteString(logoLine2 + "\n")
	b.WriteString(logoLine3 + "\n")
	b.WriteString(logoLine4 + "\n")
	b.WriteString(logoLine5 + "\n")

	return b.String()
}
