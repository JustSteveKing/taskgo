package tui

import "github.com/charmbracelet/lipgloss"

// Colours are the 16 ANSI slots rather than hex, so the TUI inherits whatever
// theme the user's terminal is set to instead of fighting it.
var (
	styleTitle = lipgloss.NewStyle().Bold(true)
	styleDim   = lipgloss.NewStyle().Faint(true)

	styleSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("4"))

	styleDone    = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	styleOverdue = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleDue     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleTag     = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleProject = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleUrgent  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleHigh    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleAgent   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	styleHeader = lipgloss.NewStyle().Bold(true).Underline(true)
	styleFooter = lipgloss.NewStyle().Faint(true)
)
