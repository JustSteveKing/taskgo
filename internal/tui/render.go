package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/lipgloss"
)

// Layout proportions. The side column is fixed rather than proportional
// because its contents are short labels — giving it a share of a wide terminal
// would just produce a column of whitespace.
const (
	sideWidth  = 26
	detailFrac = 0.38
	minDetail  = 7
)

func (m model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 {
		return "" // first frame, before the size arrives
	}
	if m.err != nil {
		return m.errorView()
	}
	if m.mode == modeHelp {
		return m.helpView()
	}

	// One row for the footer, one for the status line.
	bodyHeight := m.height - 2
	if bodyHeight < 6 {
		bodyHeight = 6
	}

	side := m.sideColumn(bodyHeight)
	main := m.mainColumn(m.width-sideWidth, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, side, main)
	return body + "\n" + m.statusLine() + "\n" + m.footer()
}

// ------------------------------------------------------------- side column

// sideColumn splits the available height between the two side panels.
//
// The two heights must sum to exactly `height`, or the column overruns the
// terminal and pushes the footer off the bottom. Views would rather be its
// natural size, but on a short terminal it gives way and scrolls — adding a
// view must never be able to break the layout.
// sideColumn splits the available height between the three side panels.
//
// The heights must sum to exactly `height`, or the column overruns the
// terminal and pushes the footer off the bottom. Each panel would rather be
// its natural size; on a short terminal they give way in order of how much
// they can afford to, and scroll. Adding a view or an agent must never be able
// to break the layout.
func (m model) sideColumn(height int) string {
	const minPanel = 3

	// Agents is sized to its contents and hidden entirely when nothing is
	// connected — a permanently empty panel is a permanent question in the
	// reader's mind, and most of the time no agent is running.
	agentsHeight := 0
	if len(m.agents) > 0 {
		agentsHeight = len(m.agents) + 3 // rows, the "(all)" row, and borders
	}

	viewsHeight := len(views) + 2

	// Trim, worst-affordable first, until everything fits.
	for viewsHeight+agentsHeight+minPanel > height {
		switch {
		case agentsHeight > minPanel:
			agentsHeight--
		case viewsHeight > minPanel:
			viewsHeight--
		default:
			agentsHeight = 0
			viewsHeight = max(minPanel, height-minPanel)
		}
		if viewsHeight <= minPanel && agentsHeight == 0 {
			break
		}
	}

	projectsHeight := height - viewsHeight - agentsHeight
	if projectsHeight < minPanel {
		projectsHeight = minPanel
		viewsHeight = max(minPanel, height-projectsHeight-agentsHeight)
		projectsHeight = height - viewsHeight - agentsHeight
	}

	panels := []string{
		box("1 Views", sideWidth, viewsHeight, m.focus == panelViews, m.viewsContent(viewsHeight-2)),
		box("2 Projects", sideWidth, projectsHeight, m.focus == panelProjects, m.projectsContent(projectsHeight-2)),
	}
	if agentsHeight > 0 {
		panels = append(panels, box(agentsTitle(len(m.agents)), sideWidth, agentsHeight,
			m.focus == panelAgents, m.agentsContent(agentsHeight-2)))
	}

	return lipgloss.JoinVertical(lipgloss.Left, panels...)
}

func agentsTitle(n int) string {
	if n == 1 {
		return "3 Agents (1)"
	}
	return fmt.Sprintf("3 Agents (%d)", n)
}

// agentsContent lists connected agents, what each holds, and how long it has
// been quiet. An idle agent is still worth showing: it is here, and it may be
// waiting on you.
func (m model) agentsContent(visible int) string {
	if visible < 1 {
		return ""
	}
	now := time.Now()

	rows := make([]string, 0, len(m.agents)+1)
	rows = append(rows, " (all)")

	for _, sess := range m.agents {
		holding := 0
		for _, c := range m.claims {
			if c.Session == sess.ID {
				holding++
			}
		}

		state := fmt.Sprintf("%d held", holding)
		if holding == 0 {
			state = "idle " + shortAge(sess.Idle(now))
		}
		name := truncate(sess.Name, sideWidth-len(state)-5)
		rows = append(rows, fmt.Sprintf(" %s %s", pad(name, sideWidth-len(state)-4), state))
	}

	start := 0
	if m.agentCursor >= visible {
		start = m.agentCursor - visible + 1
	}

	var b strings.Builder
	for i := start; i < len(rows) && i-start < visible; i++ {
		switch {
		case i == m.agentCursor:
			b.WriteString(m.selectStyle(panelAgents).Render(pad(rows[i], sideWidth-2)))
		case i == 0:
			b.WriteString(styleDim.Render(rows[i]))
		default:
			b.WriteString(styleAgent.Render(rows[i]))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func (m model) viewsContent(visible int) string {
	if visible < 1 {
		return ""
	}

	start := 0
	if m.viewCursor >= visible {
		start = m.viewCursor - visible + 1
	}

	var b strings.Builder
	for i := start; i < len(views) && i-start < visible; i++ {
		line := " " + views[i].name
		if i == m.viewCursor {
			b.WriteString(m.selectStyle(panelViews).Render(pad(line, sideWidth-2)))
		} else if views[i].filter.NeedsInput {
			// The one view that is about something being stuck stays visible
			// as such even when it is not selected.
			b.WriteString(styleWaiting.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) projectsContent(visible int) string {
	// Row 0 is "(all)", so the project list is offset by one throughout.
	rows := make([]string, 0, len(m.projects)+1)
	rows = append(rows, " (all)")
	for _, p := range m.projects {
		rows = append(rows, fmt.Sprintf(" %-*s %d", sideWidth-8, truncate(p.Name, sideWidth-8), p.Open))
	}

	start := 0
	if m.projectCursor >= visible {
		start = m.projectCursor - visible + 1
	}

	var b strings.Builder
	for i := start; i < len(rows) && i-start < visible; i++ {
		if i == m.projectCursor {
			b.WriteString(m.selectStyle(panelProjects).Render(pad(rows[i], sideWidth-2)))
		} else if i == 0 {
			b.WriteString(styleDim.Render(rows[i]))
		} else {
			b.WriteString(styleProject.Render(rows[i]))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// selectStyle dims the selection bar on unfocused panels, so at a glance it is
// obvious which cursor the keyboard is driving.
func (m model) selectStyle(p panel) lipgloss.Style {
	if m.focus == p {
		return styleSelected
	}
	return styleSelectedBlurred
}

// -------------------------------------------------------------- main column

func (m model) mainColumn(width, height int) string {
	detailHeight := int(float64(height) * detailFrac)
	if detailHeight < minDetail {
		detailHeight = minDetail
	}
	tasksHeight := height - detailHeight
	if tasksHeight < 5 {
		tasksHeight, detailHeight = 5, height-5
	}

	title := fmt.Sprintf("4 Tasks (%d)", len(m.tasks))
	if m.filterVal != "" {
		title += fmt.Sprintf(" /%s", m.filterVal)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		box(title, width, tasksHeight, m.focus == panelTasks, m.tasksContent(width-2, tasksHeight-2)),
		box(m.detailTitle(), width, detailHeight, false, m.detailContent(width-2, detailHeight-2)),
	)
}

func (m model) tasksContent(width, visible int) string {
	if len(m.tasks) == 0 {
		return "\n " + styleDim.Render("Nothing here. Press n to add a task.")
	}

	start := 0
	if m.taskCursor >= visible {
		start = m.taskCursor - visible + 1
	}
	end := min(len(m.tasks), start+visible)

	now := time.Now()
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(m.taskRow(m.tasks[i], i == m.taskCursor, width, now))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) taskRow(e store.IndexEntry, selected bool, width int, now time.Time) string {
	waiting := e.Question != ""

	mark := " "
	switch {
	case waiting:
		// Takes the status column outright: nothing is progressing on a task
		// an agent has stopped and asked about.
		mark = "?"
	case e.Status == store.StatusDone:
		mark = "✓"
	case e.Status == store.StatusDoing:
		mark = "▸"
	case e.Status == store.StatusBlocked:
		mark = "✗"
	}

	// A task an agent is actively holding gets a marker of its own. This is
	// deliberately not the same signal as "an agent last touched it", which
	// the activity log already carries — the point is who is working on it
	// now.
	held, agentHeld := m.claims.Get(e.ID)

	agentMark := " "
	if agentHeld {
		agentMark = "◆"
	}

	// Built twice: plain for the selection bar, styled otherwise. A colour
	// reset inside the bar would punch a hole in its background.
	prefix := fmt.Sprintf(" %s%s %s %-4d ", agentMark, priorityPlain(e.Priority), mark, e.ID)

	var plain, styled []string
	if e.Project != "" {
		plain = append(plain, "@"+e.Project)
		styled = append(styled, styleProject.Render("@"+e.Project))
	}
	if e.Due != nil {
		plain = append(plain, duePlain(*e.Due, now))
		styled = append(styled, dueStyled(*e.Due, e.Status, now))
	}
	for _, tag := range e.Tags {
		plain = append(plain, "#"+tag)
		styled = append(styled, styleTag.Render("#"+tag))
	}

	if agentHeld {
		plain = append(plain, held.By)
		styled = append(styled, styleAgent.Render(held.By))
	}

	if selected {
		line := prefix + e.Title
		if len(plain) > 0 {
			line += "  " + strings.Join(plain, " ")
		}
		return m.selectStyle(panelTasks).Render(pad(truncate(line, width), width))
	}

	title := e.Title
	switch {
	case waiting:
		title = styleWaiting.Render(title)
	case e.Status == store.StatusDone:
		title = styleDoneText.Render(title)
	case agentHeld:
		// The title itself changes colour, not just a marker: a glyph in the
		// margin is easy to miss when scanning, and "an agent is on this" is
		// the thing you most want to notice before touching it yourself.
		title = styleAgent.Render(title)
	}

	glyph := " "
	if agentHeld {
		glyph = styleAgent.Render("◆")
	}
	statusGlyph := markStyled(e.Status, mark)
	if waiting {
		statusGlyph = styleWaiting.Render(mark)
	}
	line := fmt.Sprintf(" %s%s %s %-4d %s", glyph, priorityGlyph(e.Priority), statusGlyph, e.ID, title)
	if len(styled) > 0 {
		line += "  " + strings.Join(styled, " ")
	}
	return truncate(line, width)
}

func markStyled(s store.Status, mark string) string {
	switch s {
	case store.StatusDone:
		return styleOK.Render(mark)
	case store.StatusDoing:
		return styleAccent.Render(mark)
	case store.StatusBlocked:
		return styleUrgent.Render(mark)
	default:
		return mark
	}
}

func priorityPlain(p store.Priority) string {
	switch p {
	case store.PriorityUrgent:
		return "!!"
	case store.PriorityHigh:
		return " !"
	case store.PriorityLow:
		return " ·"
	default:
		return "  "
	}
}

func priorityGlyph(p store.Priority) string {
	switch p {
	case store.PriorityUrgent:
		return styleUrgent.Render("!!")
	case store.PriorityHigh:
		return styleWarn.Render(" !")
	case store.PriorityLow:
		return styleDim.Render(" ·")
	default:
		return "  "
	}
}

func duePlain(due store.DueDate, now time.Time) string {
	days := int(due.Time().Sub(store.DueOnDay(now).Time()).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("%dd late", -days)
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

func dueStyled(due store.DueDate, status store.Status, now time.Time) string {
	label := duePlain(due, now)
	days := int(due.Time().Sub(store.DueOnDay(now).Time()).Hours() / 24)

	// A finished task is never chasing you, so its date is never red.
	switch {
	case status == store.StatusDone:
		return styleDim.Render(label)
	case days < 0:
		return styleUrgent.Render(label)
	case days <= 1:
		return styleWarn.Render(label)
	default:
		return styleDim.Render(label)
	}
}

// -------------------------------------------------------------------- detail

func (m model) detailTitle() string {
	if m.detail == nil {
		return "Detail"
	}
	return fmt.Sprintf("Detail — #%d", m.detail.ID)
}

func (m model) detailContent(width, height int) string {
	t := m.detail
	if t == nil {
		return "\n " + styleDim.Render("No task selected.")
	}
	now := time.Now()

	var b strings.Builder
	b.WriteString(" " + styleBold.Render(truncate(t.Title, width-1)) + "\n")

	meta := []string{string(t.Status), string(t.Priority)}
	if t.Due != nil {
		meta = append(meta, dueStyled(*t.Due, t.Status, now))
	}
	if t.Project != "" {
		meta = append(meta, styleProject.Render("@"+t.Project))
	}
	for _, tag := range t.Tags {
		meta = append(meta, styleTag.Render("#"+tag))
	}
	if t.Parent != 0 {
		meta = append(meta, styleDim.Render(fmt.Sprintf("subtask of #%d", t.Parent)))
	}
	b.WriteString(" " + styleDim.Render(strings.Join(meta, " · ")) + "\n")

	if t.AwaitingAnswer() {
		asker := t.AskedBy
		if asker == "" {
			asker = "an agent"
		}
		b.WriteString("\n " + styleWaiting.Render("? "+asker+" is waiting on you:") + "\n")
		for _, line := range wrapText(t.Question, width-3) {
			b.WriteString("   " + styleWaitingText.Render(line) + "\n")
		}
		b.WriteString(" " + styleDim.Render("press a to answer") + "\n")
	} else if t.Answer != "" {
		b.WriteString("\n " + styleDim.Render("last answer: ") + truncate(t.Answer, width-15) + "\n")
	}

	if held, ok := m.claims.Get(t.ID); ok {
		kind := "working on this"
		if !held.Explicit {
			// An implicit lease means the agent wrote to the task without
			// announcing itself, which is weaker evidence and should read
			// that way.
			kind = "recently active here"
		}
		b.WriteString(" " + styleAgent.Render(fmt.Sprintf("◆ %s %s, %s",
			held.By, kind, humanDuration(held.Held(now)))) + "\n")
	}

	if t.Notes != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(t.Notes, "\n") {
			b.WriteString(" " + truncate(line, width-1) + "\n")
		}
	}
	return b.String()
}

// ------------------------------------------------------------ chrome

func (m model) statusLine() string {
	// A transient message displaces the counts, then the counts come back.
	if m.status != "" {
		if m.statusErr {
			return styleUrgent.Render(" " + m.status)
		}
		return styleOK.Render(" " + m.status)
	}
	if m.mode == modeFilter || m.mode == modeNew {
		return " " + m.input.View()
	}
	if m.mode == modeAnswer {
		return " " + m.input.View()
	}
	if m.mode == modeConfirmDelete {
		entry, _ := m.currentTask()
		return styleUrgent.Render(fmt.Sprintf(" Delete #%d %q? [y/N]", entry.ID, entry.Title))
	}

	parts := []string{
		fmt.Sprintf("%d open", m.counts.open),
		fmt.Sprintf("%d total", m.counts.total),
	}
	if m.counts.overdue > 0 {
		parts = append(parts, styleUrgent.Render(fmt.Sprintf("%d overdue", m.counts.overdue)))
	}
	if m.counts.today > 0 {
		parts = append(parts, styleWarn.Render(fmt.Sprintf("%d today", m.counts.today)))
	}
	if m.counts.waiting > 0 {
		parts = append(parts, styleWaiting.Render(fmt.Sprintf("%d waiting on you", m.counts.waiting)))
	}
	return " " + styleDim.Render(strings.Join(parts, " · "))
}

// footer lists the keys that apply to the focused panel. Showing every key at
// all times is how a footer becomes wallpaper.
func (m model) footer() string {
	var keys [][2]string

	switch m.focus {
	case panelViews:
		keys = [][2]string{{"j/k", "view"}, {"l", "tasks"}, {"tab", "panel"}}
	case panelProjects:
		keys = [][2]string{{"j/k", "project"}, {"l", "tasks"}, {"tab", "panel"}}
	case panelAgents:
		keys = [][2]string{{"j/k", "agent"}, {"l", "tasks"}, {"tab", "panel"}}
	default:
		keys = [][2]string{
			{"j/k", "move"}, {"space", "done"}, {"a", "answer"}, {"n", "new"},
			{"e", "edit"}, {"s", "status"}, {"p", "priority"}, {"/", "filter"},
		}
	}
	keys = append(keys, [2]string{"?", "help"}, [2]string{"q", "quit"})

	var parts []string
	for _, k := range keys {
		parts = append(parts, styleKey.Render(k[0])+styleHint.Render(" "+k[1]))
	}

	line := " " + strings.Join(parts, styleHint.Render(" · "))
	brand := styleHint.Render("lazytask " + m.version + " ")

	gap := m.width - lipgloss.Width(line) - lipgloss.Width(brand)
	if gap < 1 {
		return truncate(line, m.width)
	}
	return line + strings.Repeat(" ", gap) + brand
}

func (m model) helpView() string {
	sections := []struct {
		title string
		keys  [][2]string
	}{
		{"Panels", [][2]string{
			{"1 / 2 / 3 / 4", "focus views, projects, agents, tasks"},
			{"tab / shift+tab", "cycle panels"},
			{"h / l", "move between side panels and tasks"},
		}},
		{"Moving", [][2]string{
			{"j / k", "down / up"},
			{"ctrl+d / ctrl+u", "half page"},
			{"g / G", "first / last"},
		}},
		{"Tasks", [][2]string{
			{"space", "complete or reopen"},
			{"a", "answer an agent's question"},
			{"s", "cycle status (todo → doing → blocked)"},
			{"p", "cycle priority"},
			{"n", "new task, in the selected project"},
			{"e", "open the Markdown file in $EDITOR"},
			{"d", "delete, with confirmation"},
			{"/", "filter by title"},
			{"r", "reload now"},
		}},
	}

	var b strings.Builder
	b.WriteString("\n")
	for _, s := range sections {
		b.WriteString("  " + styleBold.Render(s.title) + "\n")
		for _, k := range s.keys {
			b.WriteString(fmt.Sprintf("    %s  %s\n",
				styleKey.Render(fmt.Sprintf("%-16s", k[0])), styleHint.Render(k[1])))
		}
		b.WriteString("\n")
	}
	b.WriteString("  " + styleDim.Render("Tasks are Markdown files under "+m.store.Root()) + "\n")
	b.WriteString("  " + styleDim.Render("The list refreshes on its own, so changes made by an agent appear here.") + "\n")

	return box("Help", m.width, m.height-1, true, b.String()) + "\n" +
		styleHint.Render(" any key to close")
}

func (m model) errorView() string {
	body := "\n " + styleUrgent.Render(m.err.Error()) +
		"\n\n " + styleDim.Render("If state.json is damaged, quit and run: taskgo reindex")
	return box("Error", m.width, m.height-1, true, body) + "\n" +
		styleHint.Render(" q to quit")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// humanDuration renders a lease age the way someone glancing at it would say
// it out loud.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("for %dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("for %dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// wrapText breaks a question across the detail pane's width. A question is the
// one thing here worth showing in full rather than eliding — a truncated
// question cannot be answered.
func wrapText(text string, width int) []string {
	if width < 10 {
		width = 10
	}

	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if lipgloss.Width(line)+1+lipgloss.Width(w) > width {
				lines = append(lines, line)
				line = w
				continue
			}
			line += " " + w
		}
		lines = append(lines, line)
	}
	return lines
}
