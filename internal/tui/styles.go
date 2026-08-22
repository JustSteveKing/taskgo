package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Colours are the 16 ANSI slots rather than hex, so the interface inherits
// whatever theme the terminal is set to instead of fighting it.
var (
	colAccent = lipgloss.Color("4")
	colDim    = lipgloss.Color("8")
	colUrgent = lipgloss.Color("1")
	colWarn   = lipgloss.Color("3")
	colOK     = lipgloss.Color("2")
	colTag    = lipgloss.Color("5")
	colProj   = lipgloss.Color("6")
	// Agent activity gets its own hue so it is never confused with a project
	// or a tag, both of which are also coloured.
	colAgent = lipgloss.Color("13")

	styleDim      = lipgloss.NewStyle().Foreground(colDim)
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleAccent   = lipgloss.NewStyle().Foreground(colAccent)
	styleUrgent   = lipgloss.NewStyle().Foreground(colUrgent).Bold(true)
	styleWarn     = lipgloss.NewStyle().Foreground(colWarn)
	styleOK       = lipgloss.NewStyle().Foreground(colOK)
	styleTag      = lipgloss.NewStyle().Foreground(colTag)
	styleProject  = lipgloss.NewStyle().Foreground(colProj)
	styleDoneText = lipgloss.NewStyle().Foreground(colDim).Strikethrough(true)
	styleAgent    = lipgloss.NewStyle().Foreground(colAgent).Bold(true)

	// The selection bar. Rows under it are rendered as plain text and styled
	// once, because a colour reset mid-line would punch a hole in the bar.
	styleSelected        = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colAccent).Bold(true)
	styleSelectedBlurred = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colDim)

	styleKey  = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleHint = lipgloss.NewStyle().Foreground(colDim)
)

// box draws a rounded panel with its title set into the top border, the way
// lazygit and its siblings do. lipgloss has no title-in-border option, so the
// top edge is assembled by hand.
//
// w and h are the outer dimensions, borders included.
func box(title string, w, h int, focused bool, content string) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}

	border := lipgloss.NewStyle().Foreground(colDim)
	if focused {
		border = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	}

	inner := w - 2

	// ╭─ Title ──────────╮
	label := ""
	if title != "" {
		label = " " + title + " "
		if lipgloss.Width(label) > inner-2 {
			label = truncate(label, inner-2)
		}
	}
	fill := inner - 1 - lipgloss.Width(label)
	if fill < 0 {
		fill = 0
	}
	top := border.Render("╭─") + titleStyle(focused).Render(label) +
		border.Render(strings.Repeat("─", fill)+"╮")

	left := border.Render("│")
	right := border.Render("│")
	bottom := border.Render("╰" + strings.Repeat("─", inner) + "╯")

	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.WriteString(top + "\n")

	for i := 0; i < h-2; i++ {
		var line string
		if i < len(lines) {
			line = truncate(lines[i], inner)
		}
		b.WriteString(left + pad(line, inner) + right + "\n")
	}
	b.WriteString(bottom)

	return b.String()
}

func titleStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colDim)
}

// pad and truncate measure rendered cells rather than bytes, so styled text
// and multi-byte characters do not throw the layout off.
func pad(s string, width int) string {
	gap := width - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}
