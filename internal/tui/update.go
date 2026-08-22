package tui

import (
	"fmt"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case loadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.tasks, m.projects, m.counts = msg.tasks, msg.projects, msg.counts
		m.taskCursor = clamp(m.taskCursor, 0, max(0, len(m.tasks)-1))
		m.projectCursor = clamp(m.projectCursor, 0, len(m.projects))
		return m, m.loadDetail()

	case detailMsg:
		if msg.err != nil {
			m.detail = nil
			return m, nil
		}
		m.detail = msg.task
		return m, nil

	case statusMsg:
		m.status, m.statusErr = msg.text, msg.isErr
		return m, tea.Batch(m.load(), clearStatusAfter())

	case clearStatusMsg:
		m.status, m.statusErr = "", false
		return m, nil

	case tickMsg:
		// Reload only when nothing modal is open. Refreshing under the cursor
		// while someone is typing is worse than being two seconds stale.
		if m.mode == modeNormal {
			return m, tea.Batch(m.load(), tick())
		}
		return m, tick()

	case editorDoneMsg:
		if msg.err != nil {
			return m, flash(msg.err.Error(), true)
		}
		// The file may have changed in ways the index does not know about.
		s := m.store
		return m, tea.Sequence(
			func() tea.Msg {
				if _, err := s.Reindex(); err != nil {
					return statusMsg{text: err.Error(), isErr: true}
				}
				return statusMsg{text: "reindexed after edit"}
			},
		)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeFilter, modeNew:
		return m.handleInputKey(msg)
	case modeConfirmDelete:
		return m.handleConfirmKey(msg)
	case modeHelp:
		m.mode = modeNormal
		return m, nil
	}
	return m.handleNormalKey(msg)
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		value := m.input.Value()
		mode := m.mode
		m.mode = modeNormal
		m.input.Blur()

		if mode == modeFilter {
			m.filterVal = value
			m.taskCursor = 0
			return m, m.load()
		}

		if value == "" {
			return m, nil
		}
		s := m.store
		project := m.selectedProject()
		return m, func() tea.Msg {
			task, err := s.Create(store.ActorHuman, store.NewTask{Title: value, Project: project})
			if err != nil {
				return statusMsg{text: err.Error(), isErr: true}
			}
			return statusMsg{text: fmt.Sprintf("added #%d", task.ID)}
		}

	case tea.KeyEsc:
		m.mode = modeNormal
		m.input.Blur()
		m.input.SetValue(m.filterVal)
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.mode = modeNormal
		entry, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		s := m.store
		id := entry.ID
		return m, func() tea.Msg {
			if err := s.Delete(store.ActorHuman, id); err != nil {
				return statusMsg{text: err.Error(), isErr: true}
			}
			return statusMsg{text: fmt.Sprintf("deleted #%d", id)}
		}
	default:
		m.mode = modeNormal
		return m, nil
	}
}

func (m model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "?":
		m.mode = modeHelp
		return m, nil

	// ---- panel focus, lazygit style
	case "1":
		m.focus = panelViews
		return m, nil
	case "2":
		m.focus = panelProjects
		return m, nil
	case "3":
		m.focus = panelTasks
		return m, nil
	case "tab":
		m.focus = (m.focus + 1) % panelCount
		return m, nil
	case "shift+tab":
		m.focus = (m.focus + panelCount - 1) % panelCount
		return m, nil

	// ---- movement, applied to whichever panel has focus
	case "j", "down", "ctrl+n":
		return m.move(1)
	case "k", "up", "ctrl+p":
		return m.move(-1)
	case "g", "home":
		return m.moveTo(0)
	case "G", "end":
		return m.moveTo(1 << 30)
	case "ctrl+d":
		return m.move(10)
	case "ctrl+u":
		return m.move(-10)

	// Moving right from a side panel lands on the tasks, which is the
	// direction attention flows in this layout.
	case "l", "right", "enter":
		if m.focus != panelTasks {
			m.focus = panelTasks
		}
		return m, nil
	case "h", "left":
		if m.focus == panelTasks {
			m.focus = panelViews
		}
		return m, nil
	}

	// The remaining keys act on the selected task regardless of which panel
	// has focus, because they are the reason the list is on screen.
	return m.handleTaskKey(msg)
}

func (m model) move(delta int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelViews:
		m.viewCursor = clamp(m.viewCursor+delta, 0, len(views)-1)
		m.taskCursor = 0
		return m, m.load()
	case panelProjects:
		m.projectCursor = clamp(m.projectCursor+delta, 0, len(m.projects))
		m.taskCursor = 0
		return m, m.load()
	default:
		m.taskCursor = clamp(m.taskCursor+delta, 0, max(0, len(m.tasks)-1))
		return m, m.loadDetail()
	}
}

func (m model) moveTo(pos int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelViews:
		m.viewCursor = clamp(pos, 0, len(views)-1)
		m.taskCursor = 0
		return m, m.load()
	case panelProjects:
		m.projectCursor = clamp(pos, 0, len(m.projects))
		m.taskCursor = 0
		return m, m.load()
	default:
		m.taskCursor = clamp(pos, 0, max(0, len(m.tasks)-1))
		return m, m.loadDetail()
	}
}

func (m model) handleTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "/":
		m.mode = modeFilter
		m.input.Placeholder = "filter by title…"
		m.input.Prompt = "/"
		m.input.SetValue(m.filterVal)
		m.input.Focus()
		m.input.CursorEnd()
		return m, textinput.Blink

	case "n":
		m.mode = modeNew
		m.input.Placeholder = "new task title…"
		m.input.Prompt = "> "
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink

	case "e":
		return m, m.openEditor(editorCommand())

	case "d":
		if _, ok := m.currentTask(); ok {
			m.mode = modeConfirmDelete
		}
		return m, nil

	case " ", "x":
		entry, ok := m.currentTask()
		if !ok {
			return m, nil
		}
		s := m.store
		id := entry.ID
		if entry.Status == store.StatusDone {
			return m, func() tea.Msg {
				if _, err := s.Reopen(store.ActorHuman, id); err != nil {
					return statusMsg{text: err.Error(), isErr: true}
				}
				return statusMsg{text: fmt.Sprintf("reopened #%d", id)}
			}
		}
		return m, func() tea.Msg {
			if _, err := s.Complete(store.ActorHuman, id); err != nil {
				return statusMsg{text: err.Error(), isErr: true}
			}
			return statusMsg{text: fmt.Sprintf("completed #%d", id)}
		}

	case "s":
		return m, m.cycle("status")
	case "p":
		return m, m.cycle("priority")

	case "r":
		return m, tea.Batch(m.load(), flash("reloaded", false))
	}

	return m, nil
}

// cycle steps status or priority forward on the selected task.
//
// The status cycle deliberately skips done: space is how a task is completed,
// and two keys that both mark done is an invitation to press the wrong one.
func (m model) cycle(field string) tea.Cmd {
	entry, ok := m.currentTask()
	if !ok {
		return nil
	}
	s := m.store
	id := entry.ID

	if field == "status" {
		order := []store.Status{store.StatusTodo, store.StatusDoing, store.StatusBlocked}
		next := order[0]
		for i, st := range order {
			if st == entry.Status {
				next = order[(i+1)%len(order)]
				break
			}
		}
		return func() tea.Msg {
			if _, err := s.Update(store.ActorHuman, id, store.Update{Status: &next}); err != nil {
				return statusMsg{text: err.Error(), isErr: true}
			}
			return statusMsg{text: fmt.Sprintf("#%d → %s", id, next)}
		}
	}

	order := []store.Priority{
		store.PriorityLow, store.PriorityNormal, store.PriorityHigh, store.PriorityUrgent,
	}
	next := order[0]
	for i, p := range order {
		if p == entry.Priority {
			next = order[(i+1)%len(order)]
			break
		}
	}
	return func() tea.Msg {
		if _, err := s.Update(store.ActorHuman, id, store.Update{Priority: &next}); err != nil {
			return statusMsg{text: err.Error(), isErr: true}
		}
		return statusMsg{text: fmt.Sprintf("#%d → %s", id, next)}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
