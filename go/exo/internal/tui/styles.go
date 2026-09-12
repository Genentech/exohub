package tui

import "github.com/charmbracelet/lipgloss"

// Colors.
var (
	normalDim      = lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"}
	gray           = lipgloss.AdaptiveColor{Light: "#909090", Dark: "#626262"}
	midGray        = lipgloss.AdaptiveColor{Light: "#B2B2B2", Dark: "#4A4A4A"}
	darkGray       = lipgloss.AdaptiveColor{Light: "#DDDADA", Dark: "#3C3C3C"}
	brightGray     = lipgloss.AdaptiveColor{Light: "#847A85", Dark: "#979797"}
	dimBrightGray  = lipgloss.AdaptiveColor{Light: "#C2B8C2", Dark: "#4D4D4D"}
	cream          = lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}
	yellowGreen    = lipgloss.AdaptiveColor{Light: "#04B575", Dark: "#ECFD65"}
	fuchsia        = lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"}
	dimFuchsia     = lipgloss.AdaptiveColor{Light: "#F1A8FF", Dark: "#99519E"}
	dullFuchsia    = lipgloss.AdaptiveColor{Dark: "#AD58B4", Light: "#F793FF"}
	dimDullFuchsia = lipgloss.AdaptiveColor{Light: "#F6C9FF", Dark: "#7B4380"}
	green          = lipgloss.Color("#04B575")
	red            = lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"}
	darkRed        = lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#FF1049"}
	semiDimGreen   = lipgloss.AdaptiveColor{Light: "#35D79C", Dark: "#036B46"}
	dimGreen       = lipgloss.AdaptiveColor{Light: "#72D2B0", Dark: "#0B5137"}

	// Styles for the MD/RAW toggle
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#04B575", Dark: "#ECFD65"}).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#909090", Dark: "#626262"}).
				Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			MarginLeft(2).
			Foreground(lipgloss.AdaptiveColor{Light: "#333333", Dark: "#DDDDDD"})

	modeToggleStyle = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#E6E6E6", Dark: "#242424"})

	// toolbarBtnStyle renders a clickable toolbar button with distinct background.
	toolbarBtnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#AAAAAA"}).
			Background(lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#383838"}).
			Padding(0, 1)

	// toolbarBtnKeyStyle renders the keyboard shortcut part of a toolbar button.
	toolbarBtnKeyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#E84393", Dark: "#E84393"}).
				Background(lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#383838"}).
				Bold(true)

	// Badge styles for project@version in search results.
	badgeProjectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8C42"))
	badgeAtStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7A7A7A"))
	badgeVersionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9B7A5A"))
	badgeStyle        = lipgloss.NewStyle()
)

// renderProjectBadge renders a project@version badge with distinct colors per part.
func renderProjectBadge(project, version string) string {
	inner := badgeProjectStyle.Render(project)
	if version != "" {
		inner += badgeAtStyle.Render("@") + badgeVersionStyle.Render(version)
	}
	return badgeStyle.Render(inner)
}

// renderToolbarBtn renders a toolbar button with a bold/colored key and plain label.
func renderToolbarBtn(key, label string) string {
	bg := lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#383838"}
	styledKey := toolbarBtnKeyStyle.Render(key)
	spacer := lipgloss.NewStyle().Background(bg).Render(" ")
	styledLabel := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#AAAAAA"}).
		Background(bg).
		Render(label)
	inner := styledKey + spacer + styledLabel
	return lipgloss.NewStyle().Background(bg).Padding(0, 1).Render(inner)
}

// Ulimately, we'll transition to named styles.
var (
	dimNormalFg      = lipgloss.NewStyle().Foreground(normalDim).Render
	brightGrayFg     = lipgloss.NewStyle().Foreground(brightGray).Render
	dimBrightGrayFg  = lipgloss.NewStyle().Foreground(dimBrightGray).Render
	grayFg           = lipgloss.NewStyle().Foreground(gray).Render
	codeFg           = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Background(lipgloss.Color("#2a2a2a")).Padding(0, 1).Render
	midGrayFg        = lipgloss.NewStyle().Foreground(midGray).Render
	darkGrayFg       = lipgloss.NewStyle().Foreground(darkGray)
	greenFg          = lipgloss.NewStyle().Foreground(green).Render
	semiDimGreenFg   = lipgloss.NewStyle().Foreground(semiDimGreen).Render
	dimGreenFg       = lipgloss.NewStyle().Foreground(dimGreen).Render
	yellowGreenFg    = lipgloss.NewStyle().Foreground(yellowGreen).Render
	fuchsiaFg        = lipgloss.NewStyle().Foreground(fuchsia).Render
	dimFuchsiaFg     = lipgloss.NewStyle().Foreground(dimFuchsia).Render
	dullFuchsiaFg    = lipgloss.NewStyle().Foreground(dullFuchsia).Render
	dimDullFuchsiaFg = lipgloss.NewStyle().Foreground(dimDullFuchsia).Render
	redFg            = lipgloss.NewStyle().Foreground(red).Render
	darkRedFg        = lipgloss.NewStyle().Foreground(darkRed).Render
	tabStyle         = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#909090", Dark: "#626262"})
	selectedTabStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#333333", Dark: "#979797"})
	errorTitleStyle  = lipgloss.NewStyle().Foreground(cream).Background(red).Padding(0, 1)
	subtleStyle      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"})
	paginationStyle  = subtleStyle
)
