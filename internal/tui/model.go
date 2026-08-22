// Package tui is the interactive terminal interface.
//
// It reloads from the store on a timer as well as after its own edits, so a
// task an agent creates over MCP appears here without the user doing anything.
// That liveness is the point: the TUI is a view onto the files, not a cache of
// them.
package tui

import (
	"fmt"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// refreshEvery is how often the TUI re-reads the store to pick up changes
	// made elsewhere. Cheap: the index is one small JSON file.
	refreshEvery = 2 * time.Second

	// statusHold is how long a transient message stays on screen.
	statusHold = 4 * time.Second
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeDetail
)

type model struct {
	store *store.Store

	tasks  []store.IndexEntry
	cursor int
	mode   mode

	filter    textinput.Model
	filterVal string
	showDone  bool

	detail *store.Task

	status    string
	statusErr bool
	err       error

	width  int
	height int
	quit   bool
}

// Messages.
type tasksLoadedMsg struct {
	tasks []store.IndexEntry
	err   error
}
type detailLoadedMsg struct {
	task *store.Task
	err  error
}
type statusMsg struct {
	text  string
	isErr bool
}
type clearStatusMsg struct{}
type tickMsg time.Time

func New(s *store.Store) model {
	ti := textinput.New()
	ti.Placeholder = "filter by title…"
	ti.Prompt = "/"
	ti.CharLimit = 120

	return model{store: s, filter: ti}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.load(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// load reads the store. It is a command rather than an inline call so the UI
// never blocks on IO.
func (m model) load() tea.Cmd {
	filter := store.Filter{IncludeDone: m.showDone, Text: m.filterVal}
	s := m.store
	return func() tea.Msg {
		tasks, err := s.List(filter)
		return tasksLoadedMsg{tasks: tasks, err: err}
	}
}

func (m model) loadDetail(id int) tea.Cmd {
	s := m.store
	return func() tea.Msg {
		task, err := s.Get(id)
		return detailLoadedMsg{task: task, err: err}
	}
}

func flash(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, isErr: isErr} }
}

func clearStatusAfter() tea.Cmd {
	return tea.Tick(statusHold, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

// current returns the highlighted entry, if any.
func (m model) current() (store.IndexEntry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.tasks) {
		return store.IndexEntry{}, false
	}
	return m.tasks[m.cursor], true
}

// mutate wraps a store call, reloading afterwards so the list reflects the
// change immediately rather than waiting for the next tick.
func (m model) mutate(verb string, fn func(*store.Store, int) error) tea.Cmd {
	entry, ok := m.current()
	if !ok {
		return nil
	}
	s := m.store
	id := entry.ID
	return func() tea.Msg {
		if err := fn(s, id); err != nil {
			return statusMsg{text: err.Error(), isErr: true}
		}
		return statusMsg{text: fmt.Sprintf("%s #%d", verb, id)}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tasksLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.tasks = msg.tasks
		if m.cursor >= len(m.tasks) {
			m.cursor = max(0, len(m.tasks)-1)
		}
		return m, nil

	case detailLoadedMsg:
		if msg.err != nil {
			return m, flash(msg.err.Error(), true)
		}
		m.detail = msg.task
		m.mode = modeDetail
		return m, nil

	case statusMsg:
		m.status, m.statusErr = msg.text, msg.isErr
		return m, tea.Batch(m.load(), clearStatusAfter())

	case clearStatusMsg:
		m.status, m.statusErr = "", false
		return m, nil

	case tickMsg:
		// Reload only when idle. Refreshing under the cursor while someone is
		// typing a filter or reading a task is worse than being two seconds
		// stale.
		if m.mode == modeList {
			return m, tea.Batch(m.load(), tick())
		}
		return m, tick()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeFilter:
		switch msg.Type {
		case tea.KeyEnter:
			m.mode = modeList
			m.filterVal = m.filter.Value()
			return m, m.load()
		case tea.KeyEsc:
			m.mode = modeList
			m.filter.SetValue(m.filterVal)
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd

	case modeDetail:
		switch msg.String() {
		case "esc", "q", "enter":
			m.mode = modeList
			m.detail = nil
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "j", "down":
		if m.cursor < len(m.tasks)-1 {
			m.cursor++
		}
		return m, nil

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "g", "home":
		m.cursor = 0
		return m, nil

	case "G", "end":
		m.cursor = max(0, len(m.tasks)-1)
		return m, nil

	case "/":
		m.mode = modeFilter
		m.filter.Focus()
		return m, textinput.Blink

	case "a":
		m.showDone = !m.showDone
		label := "hiding done"
		if m.showDone {
			label = "showing done"
		}
		return m, tea.Batch(m.load(), flash(label, false))

	case "enter", "l":
		if entry, ok := m.current(); ok {
			return m, m.loadDetail(entry.ID)
		}
		return m, nil

	case " ", "x":
		entry, ok := m.current()
		if !ok {
			return m, nil
		}
		if entry.Status == store.StatusDone {
			return m, m.mutate("reopened", func(s *store.Store, id int) error {
				_, err := s.Reopen(store.ActorHuman, id)
				return err
			})
		}
		return m, m.mutate("completed", func(s *store.Store, id int) error {
			_, err := s.Complete(store.ActorHuman, id)
			return err
		})

	case "s":
		return m, m.cycleStatus()

	case "p":
		return m, m.cyclePriority()

	case "r":
		return m, tea.Batch(m.load(), flash("reloaded", false))

	case "?":
		return m, flash("j/k move · space done · s status · p priority · / filter · a all · enter detail · q quit", false)
	}

	return m, nil
}

// cycleStatus steps a task through the lifecycle, skipping done — completing
// is what space is for, and having two keys that both mark done invites
// pressing the wrong one.
func (m model) cycleStatus() tea.Cmd {
	entry, ok := m.current()
	if !ok {
		return nil
	}

	order := []store.Status{store.StatusTodo, store.StatusDoing, store.StatusBlocked}
	next := order[0]
	for i, st := range order {
		if st == entry.Status {
			next = order[(i+1)%len(order)]
			break
		}
	}

	s := m.store
	id := entry.ID
	return func() tea.Msg {
		if _, err := s.Update(store.ActorHuman, id, store.Update{Status: &next}); err != nil {
			return statusMsg{text: err.Error(), isErr: true}
		}
		return statusMsg{text: fmt.Sprintf("#%d → %s", id, next)}
	}
}

func (m model) cyclePriority() tea.Cmd {
	entry, ok := m.current()
	if !ok {
		return nil
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

	s := m.store
	id := entry.ID
	return func() tea.Msg {
		if _, err := s.Update(store.ActorHuman, id, store.Update{Priority: &next}); err != nil {
			return statusMsg{text: err.Error(), isErr: true}
		}
		return statusMsg{text: fmt.Sprintf("#%d → %s", id, next)}
	}
}

// Run starts the interactive program.
func Run(s *store.Store) error {
	p := tea.NewProgram(New(s), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
