package init

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/Genentech/exohub/go/exo/branding"
	"github.com/Genentech/exohub/go/exo/commandutil"
	"github.com/Genentech/exohub/go/exo/palette"
)

// askYesNoTUI shows an interactive yes/no confirmation using huh.
func askYesNoTUI(prompt string, defaultYes bool) (bool, error) {
	confirm := defaultYes
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(prompt).
				Affirmative("Yes").
				Negative("No").
				Value(&confirm),
		),
	).WithTheme(commandutil.ExoTheme()).WithKeyMap(commandutil.ExoKeyMap())
	if err := form.Run(); err != nil {
		return false, err
	}
	return confirm, nil
}

// uuidInputModel is a Bubble Tea model for UUID confirmation input
type uuidInputModel struct {
	prompt       string
	expectedUUID string
	textInput    textinput.Model
	err          string
	confirmed    bool
	aborted      bool
}

func (m uuidInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m uuidInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.aborted = true
			return m, tea.Quit
		case tea.KeyEnter:
			input := strings.TrimSpace(m.textInput.Value())
			if input == m.expectedUUID {
				m.confirmed = true
				return m, tea.Quit
			}
			m.err = "UUID does not match. Please try again or press Esc to cancel."
			return m, nil
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m uuidInputModel) View() string {
	if m.confirmed || m.aborted {
		return ""
	}

	var b strings.Builder

	// Show prompt with branding color
	promptStyle := lipgloss.NewStyle().Foreground(branding.GradientOrange).Bold(true)
	b.WriteString(promptStyle.Render(m.prompt) + "\n\n")

	// Show text input
	b.WriteString(m.textInput.View())
	b.WriteString("\n\n")

	// Show error if any
	if m.err != "" {
		errorStyle := lipgloss.NewStyle().Foreground(palette.Current().Error.Adaptive()).Bold(true)
		b.WriteString(errorStyle.Render("✗ " + m.err))
		b.WriteString("\n\n")
	}

	// Show help
	helpStyle := lipgloss.NewStyle().Foreground(palette.Current().Dim.Adaptive())
	b.WriteString(helpStyle.Render("(press enter to confirm, esc to cancel)"))

	return b.String()
}

// askUUIDConfirmationTUI prompts the user to enter a UUID for confirmation
func askUUIDConfirmationTUI(prompt, expectedUUID string) (string, error) {
	ti := textinput.New()
	ti.Placeholder = expectedUUID
	ti.Focus()
	ti.CharLimit = 36
	ti.Width = 40

	m := uuidInputModel{
		prompt:       prompt,
		expectedUUID: expectedUUID,
		textInput:    ti,
	}

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}

	final := finalModel.(uuidInputModel)
	if final.aborted {
		return "", fmt.Errorf("cancelled by user")
	}

	return final.expectedUUID, nil
}

// progressModel is a Bubble Tea model for showing progress during operations
type progressModel struct {
	spinner  spinner.Model
	message  string
	done     bool
	err      error
	doneChan chan error
}

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForCompletion())
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case completionMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	}

	return m, nil
}

func (m progressModel) View() string {
	if m.done {
		if m.err != nil {
			return ErrorStyle.Render("✗ ") + m.message + " failed\n"
		}
		return SuccessStyle.Render("✓ ") + m.message + " complete\n"
	}

	return m.spinner.View() + " " + m.message + "...\n"
}

type completionMsg struct {
	err error
}

func (m progressModel) waitForCompletion() tea.Cmd {
	return func() tea.Msg {
		err := <-m.doneChan
		return completionMsg{err: err}
	}
}

// showProgress displays a spinner while an operation is running
func showProgress(message string, operation func() error) error {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(palette.Current().Accent.Adaptive())

	doneChan := make(chan error, 1)

	m := progressModel{
		spinner:  s,
		message:  message,
		doneChan: doneChan,
	}

	// Run operation in background
	go func() {
		doneChan <- operation()
	}()

	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	final := finalModel.(progressModel)
	return final.err
}
