// Package tui is the interactive terminal interface.
//
// The layout follows the lazygit family: numbered side panels on the left
// narrowing what the main panel shows, a detail pane under it, and a footer
// whose keys change with the focused panel. Focus is the organising idea —
// every key means something in the context of one panel.
//
// It reloads from the store on a timer as well as after its own edits, so a
// task an agent creates over MCP appears without the user doing anything. The
// TUI is a view onto the files, not a cache of them.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// refreshEvery is how often the store is re-read to pick up outside
	// changes. Cheap: the index is one small JSON file.
	refreshEvery = 2 * time.Second
	statusHold   = 4 * time.Second
)

// panel identifies a focusable region. The detail pane is not focusable; it
// follows the task cursor, which is what makes it feel like a preview rather
// than another thing to manage.
type panel int

const (
	panelViews panel = iota
	panelProjects
	panelTasks
	panelCount
)

func (p panel) title() string {
	switch p {
	case panelViews:
		return "Views"
	case panelProjects:
		return "Projects"
	default:
		return "Tasks"
	}
}

// mode is the modal state layered over the panels.
type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeNew
	modeConfirmDelete
	modeAnswer
	modeHelp
)

// view is a saved query in the Views panel.
type view struct {
	name string
	// apply narrows a filter; today and overdue need the store's own helpers
	// so they are flagged rather than expressed as a filter.
	filter  store.Filter
	special string // "", "today", "overdue"
}

var views = []view{
	{name: "All", filter: store.Filter{}},
	// First after All, because a task waiting on you is the one thing here
	// that has actually stopped.
	{name: "Needs you", filter: store.Filter{NeedsInput: true, IncludeDone: true}},
	{name: "Today", special: "today"},
	{name: "Overdue", special: "overdue"},
	{name: "Doing", filter: store.Filter{Status: store.StatusDoing}},
	{name: "Blocked", filter: store.Filter{Status: store.StatusBlocked}},
	{name: "Done", filter: store.Filter{Status: store.StatusDone}},
}

type model struct {
	store   *store.Store
	version string

	focus panel
	mode  mode

	tasks    []store.IndexEntry
	projects []store.ProjectSummary
	counts   summary
	claims   claim.Set

	viewCursor    int
	projectCursor int
	taskCursor    int

	detail *store.Task

	input     textinput.Model
	filterVal string

	status    string
	statusErr bool
	err       error

	width, height int
	quit          bool
}

type summary struct {
	total, open, overdue, today, waiting int
}

// ------------------------------------------------------------------ messages

type loadedMsg struct {
	tasks    []store.IndexEntry
	projects []store.ProjectSummary
	counts   summary
	claims   claim.Set
	err      error
}
type detailMsg struct {
	task *store.Task
	err  error
}
type statusMsg struct {
	text  string
	isErr bool
}
type clearStatusMsg struct{}
type tickMsg time.Time
type editorDoneMsg struct{ err error }

func New(s *store.Store, version string) model {
	ti := textinput.New()
	ti.CharLimit = 200

	return model{store: s, version: version, focus: panelTasks, input: ti}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.load(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// currentFilter combines the selected view, the selected project and any text
// filter. They compose rather than override, so "Overdue" plus a project shows
// that project's overdue work.
func (m model) currentFilter() (store.Filter, string) {
	v := views[m.viewCursor]
	f := v.filter
	f.Text = m.filterVal

	if p := m.selectedProject(); p != "" {
		f.Project = p
	}
	return f, v.special
}

func (m model) selectedProject() string {
	// Index 0 of the Projects panel is "(all)".
	if m.projectCursor <= 0 || m.projectCursor-1 >= len(m.projects) {
		return ""
	}
	return m.projects[m.projectCursor-1].Name
}

func (m model) load() tea.Cmd {
	s := m.store
	filter, special := m.currentFilter()

	return func() tea.Msg {
		now := time.Now()

		var (
			tasks []store.IndexEntry
			err   error
		)
		switch special {
		case "today":
			tasks, err = s.Today(now)
		case "overdue":
			tasks, err = s.Overdue(now)
		default:
			tasks, err = s.List(filter)
		}
		if err != nil {
			return loadedMsg{err: err}
		}

		// The special views come from helpers that do not take a filter, so
		// the project narrowing is applied here instead.
		if special != "" && filter.Project != "" {
			var kept []store.IndexEntry
			for _, t := range tasks {
				if strings.EqualFold(t.Project, filter.Project) {
					kept = append(kept, t)
				}
			}
			tasks = kept
		}

		projects, err := s.ListProjects()
		if err != nil {
			return loadedMsg{err: err}
		}

		all, err := s.List(store.Filter{IncludeDone: true})
		if err != nil {
			return loadedMsg{err: err}
		}
		overdue, err := s.Overdue(now)
		if err != nil {
			return loadedMsg{err: err}
		}
		today, err := s.Today(now)
		if err != nil {
			return loadedMsg{err: err}
		}

		open, waiting := 0, 0
		for _, t := range all {
			if t.Status != store.StatusDone {
				open++
			}
			if t.Question != "" {
				waiting++
			}
		}

		// Claims are ephemeral and read lock-free, so a failure here degrades
		// the display rather than the whole load.
		claims, _ := claim.Load(s, now)

		return loadedMsg{
			tasks:    tasks,
			projects: projects,
			claims:   claims,
			counts:   summary{total: len(all), open: open, overdue: len(overdue), today: len(today), waiting: waiting},
		}
	}
}

func (m model) loadDetail() tea.Cmd {
	entry, ok := m.currentTask()
	if !ok {
		return func() tea.Msg { return detailMsg{} }
	}
	s := m.store
	id := entry.ID
	return func() tea.Msg {
		task, err := s.Get(id)
		return detailMsg{task: task, err: err}
	}
}

func (m model) currentTask() (store.IndexEntry, bool) {
	if m.taskCursor < 0 || m.taskCursor >= len(m.tasks) {
		return store.IndexEntry{}, false
	}
	return m.tasks[m.taskCursor], true
}

func flash(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, isErr: isErr} }
}

func clearStatusAfter() tea.Cmd {
	return tea.Tick(statusHold, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

// Run starts the interactive program.
func Run(s *store.Store, version string) error {
	p := tea.NewProgram(New(s, version), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// openEditor suspends the TUI and hands the terminal to $EDITOR, which is the
// only honest way to edit a task whose real form is a Markdown file.
func (m model) openEditor(editor string) tea.Cmd {
	entry, ok := m.currentTask()
	if !ok {
		return nil
	}
	path := filepath.Join(m.store.Root(), "tasks", fmt.Sprintf("%d.md", entry.ID))

	c := exec.Command("sh", "-c", editor+" "+shellQuote(path))
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{err: err} })
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func editorCommand() string {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return "vi"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
