package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.quit {
		return ""
	}
	if m.err != nil {
		return styleErr.Render("taskgo: "+m.err.Error()) +
			"\n\n" + styleDim.Render("If state.json is damaged, run: taskgo reindex") +
			"\n" + styleDim.Render("q to quit") + "\n"
	}
	if m.mode == modeDetail && m.detail != nil {
		return m.detailView()
	}
	return m.listView()
}

func (m model) listView() string {
	var b strings.Builder
	now := time.Now()

	open := 0
	for _, t := range m.tasks {
		if t.Status != store.StatusDone {
			open++
		}
	}

	title := fmt.Sprintf("taskgo — %d shown, %d open", len(m.tasks), open)
	if m.filterVal != "" {
		title += fmt.Sprintf(" — filter %q", m.filterVal)
	}
	if m.showDone {
		title += " — including done"
	}
	b.WriteString(styleTitle.Render(title) + "\n\n")

	if len(m.tasks) == 0 {
		b.WriteString(styleDim.Render("Nothing here. Add one with: taskgo add <title>") + "\n")
	} else {
		// Reserve rows for the chrome so the list never scrolls the footer off.
		visible := m.height - 7
		if visible < 3 {
			visible = 3
		}
		start := 0
		if m.cursor >= visible {
			start = m.cursor - visible + 1
		}
		end := min(len(m.tasks), start+visible)

		for i := start; i < end; i++ {
			b.WriteString(m.renderRow(m.tasks[i], i == m.cursor, now) + "\n")
		}
		if end < len(m.tasks) {
			b.WriteString(styleDim.Render(fmt.Sprintf("  … %d more", len(m.tasks)-end)) + "\n")
		}
	}

	b.WriteString("\n")
	if m.mode == modeFilter {
		b.WriteString(m.filter.View() + "\n")
	} else if m.status != "" {
		style := styleDim
		if m.statusErr {
			style = styleErr
		}
		b.WriteString(style.Render(m.status) + "\n")
	} else {
		b.WriteString(styleFooter.Render("j/k move · space done · s status · p priority · / filter · a all · enter detail · ? help · q quit") + "\n")
	}

	return b.String()
}

func (m model) renderRow(e store.IndexEntry, selected bool, now time.Time) string {
	mark := " "
	switch e.Status {
	case store.StatusDone:
		mark = "x"
	case store.StatusDoing:
		mark = ">"
	case store.StatusBlocked:
		mark = "!"
	}

	// The row is assembled twice: once as plain text and once styled.
	//
	// A selected row cannot reuse the styled version, because the per-part
	// colours below emit their own ANSI resets, and a reset in the middle of
	// the line drops the highlight background for everything after it. The
	// highlight then stops halfway across the row. Rendering the selected row
	// from plain text and applying one style over the whole thing is the only
	// way to get a solid bar.
	prefix := fmt.Sprintf(" %s [%s] %-4d ", priorityPlain(e.Priority), mark, e.ID)

	var plainTrail, styledTrail []string
	if e.Project != "" {
		plainTrail = append(plainTrail, "@"+e.Project)
		styledTrail = append(styledTrail, styleProject.Render("@"+e.Project))
	}
	if e.Due != nil {
		plainTrail = append(plainTrail, duePlain(*e.Due, now))
		styledTrail = append(styledTrail, dueChip(*e.Due, e.Status, now))
	}
	for _, tag := range e.Tags {
		plainTrail = append(plainTrail, "#"+tag)
		styledTrail = append(styledTrail, styleTag.Render("#"+tag))
	}

	if selected {
		line := prefix + e.Title
		if len(plainTrail) > 0 {
			line += "  " + strings.Join(plainTrail, " ")
		}
		width := m.width
		if width <= 0 {
			width = lipgloss.Width(line)
		}
		return styleSelected.Render(padTo(line, width))
	}

	title := e.Title
	if e.Status == store.StatusDone {
		title = styleDone.Render(title)
	}
	line := fmt.Sprintf(" %s [%s] %-4d %s", priorityGlyph(e.Priority), mark, e.ID, title)
	if len(styledTrail) > 0 {
		line += "  " + strings.Join(styledTrail, " ")
	}
	return line
}

// priorityPlain is the unstyled twin of priorityGlyph, for selected rows.
func priorityPlain(p store.Priority) string {
	switch p {
	case store.PriorityUrgent:
		return "!!"
	case store.PriorityHigh:
		return " !"
	case store.PriorityLow:
		return " ."
	default:
		return "  "
	}
}

// duePlain is the unstyled twin of dueChip.
func duePlain(due store.DueDate, now time.Time) string {
	days := int(due.Time().Sub(store.DueOnDay(now).Time()).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("overdue %dd", -days)
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days <= 7:
		return fmt.Sprintf("in %dd", days)
	default:
		return due.String()
	}
}

func priorityGlyph(p store.Priority) string {
	switch p {
	case store.PriorityUrgent:
		return styleUrgent.Render("!!")
	case store.PriorityHigh:
		return styleHigh.Render(" !")
	case store.PriorityLow:
		return styleDim.Render(" .")
	default:
		return "  "
	}
}

func dueChip(due store.DueDate, status store.Status, now time.Time) string {
	label := duePlain(due, now)
	days := int(due.Time().Sub(store.DueOnDay(now).Time()).Hours() / 24)

	// A finished task is never chasing you, so its date is never red.
	switch {
	case status == store.StatusDone:
		return styleDim.Render(label)
	case days < 0:
		return styleOverdue.Render(label)
	case days <= 1:
		return styleDue.Render(label)
	default:
		return styleDim.Render(label)
	}
}

func (m model) detailView() string {
	t := m.detail
	now := time.Now()

	var b strings.Builder
	b.WriteString(styleHeader.Render(fmt.Sprintf("#%d  %s", t.ID, t.Title)) + "\n\n")

	// Pad the label BEFORE styling it. Padding afterwards counts the ANSI
	// escape bytes as width, so the columns come out ragged.
	field := func(label, value string) {
		if value != "" {
			b.WriteString(styleDim.Render(fmt.Sprintf("%-9s", label)) + " " + value + "\n")
		}
	}

	field("status", string(t.Status))
	field("priority", string(t.Priority))
	if t.Due != nil {
		field("due", t.Due.String()+"  "+dueChip(*t.Due, t.Status, now))
	}
	if t.Project != "" {
		field("project", styleProject.Render(t.Project))
	}
	if len(t.Tags) > 0 {
		field("tags", styleTag.Render("#"+strings.Join(t.Tags, " #")))
	}
	if t.Parent != 0 {
		field("parent", fmt.Sprintf("#%d", t.Parent))
	}
	field("created", t.Created.Local().Format("2006-01-02 15:04"))
	field("updated", t.Updated.Local().Format("2006-01-02 15:04"))

	if t.Notes != "" {
		b.WriteString("\n" + t.Notes + "\n")
	}

	b.WriteString("\n" + styleFooter.Render("esc back · q back") + "\n")
	return b.String()
}

// padTo pads to a display width, counting rendered cells rather than bytes so
// styled text and multi-byte characters do not throw the highlight off.
func padTo(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
