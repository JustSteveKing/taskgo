package tui

import (
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/agents"
	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

// Update is a pure function of (model, msg), so the whole interface can be
// driven without a terminal: send keys, run whatever command comes back, and
// look at what changed on disk.

func newTestModel(t *testing.T) (model, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := New(s, "test")
	m.width, m.height = 120, 40
	return m, s
}

// key sends one keystroke and returns the resulting model and command.
func key(t *testing.T, m model, k string) (model, tea.Cmd) {
	t.Helper()

	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}

	next, cmd := m.Update(msg)
	return next.(model), cmd
}

// typeText feeds a string into whichever input is open.
func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
}

// reload runs the load command and applies the result, the way the runtime
// would.
func reload(t *testing.T, m model) model {
	t.Helper()
	msg := m.load()()
	next, _ := m.Update(msg)
	return next.(model)
}

// run executes a command and feeds any message it produces back in, so that a
// key's effect on the store actually happens.
func run(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := m.Update(msg)
	return next.(model)
}

func seed(t *testing.T, s *store.Store, titles ...string) {
	t.Helper()
	for _, title := range titles {
		if _, err := s.Create(store.ActorHuman, store.NewTask{Title: title}); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}
}

func TestSpaceCompletesAndReopens(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Fix the thing")
	m = reload(t, m)

	m, cmd := key(t, m, " ")
	m = run(t, m, cmd)

	task, err := s.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != store.StatusDone {
		t.Fatalf("status = %s, want done", task.Status)
	}

	// Completing removes it from the default view, so reopening is done from
	// Done — the same key, which is why there is no separate reopen key.
	m = reload(t, m)
	if len(m.tasks) != 0 {
		t.Errorf("a completed task is still in the All view: %+v", titlesOf(m))
	}

	m = selectView(t, m, "Done")
	if len(m.tasks) != 1 {
		t.Fatalf("Done view shows %d tasks, want 1", len(m.tasks))
	}
	_, cmd = key(t, m, " ")
	run(t, m, cmd)

	task, _ = s.Get(1)
	if task.Status != store.StatusTodo {
		t.Errorf("status = %s after space in the Done view, want todo", task.Status)
	}
}

// selectView moves the Views panel onto a named view and reloads.
func selectView(t *testing.T, m model, name string) model {
	t.Helper()

	m, _ = key(t, m, "1")
	m, cmd := key(t, m, "g")
	m = run(t, m, cmd)
	for i := 0; i < len(views); i++ {
		if views[m.viewCursor].name == name {
			m, _ = key(t, m, "4")
			return reload(t, m)
		}
		m, cmd = key(t, m, "j")
		m = run(t, m, cmd)
	}
	t.Fatalf("no view named %q", name)
	return m
}

// The status cycle deliberately skips done: space is how a task is completed,
// and two keys that both mark done invite pressing the wrong one.
func TestStatusCycleSkipsDone(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Cycle me")
	m = reload(t, m)

	want := []store.Status{store.StatusDoing, store.StatusBlocked, store.StatusTodo}
	for i, expected := range want {
		var cmd tea.Cmd
		m, cmd = key(t, m, "s")
		m = run(t, m, cmd)
		m = reload(t, m)

		task, err := s.Get(1)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if task.Status != expected {
			t.Fatalf("press %d: status = %s, want %s", i+1, task.Status, expected)
		}
	}
}

func TestPriorityCycleWrapsAround(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Cycle me")
	m = reload(t, m)

	want := []store.Priority{
		store.PriorityHigh, store.PriorityUrgent, store.PriorityLow, store.PriorityNormal,
	}
	for i, expected := range want {
		var cmd tea.Cmd
		m, cmd = key(t, m, "p")
		m = run(t, m, cmd)
		m = reload(t, m)

		task, _ := s.Get(1)
		if task.Priority != expected {
			t.Fatalf("press %d: priority = %s, want %s", i+1, task.Priority, expected)
		}
	}
}

func TestNewTaskTakesTheSelectedProject(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.CreateProject(store.ActorHuman, "web", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	seed(t, s, "Existing")
	m = reload(t, m)

	// Focus the Projects panel and select the first real project.
	m, _ = key(t, m, "2")
	m, cmd := key(t, m, "j")
	m = run(t, m, cmd)

	m, _ = key(t, m, "n")
	if m.mode != modeNew {
		t.Fatalf("mode = %v, want modeNew", m.mode)
	}
	m = typeText(t, m, "Typed in the TUI")
	m, cmd = key(t, m, "enter")
	m = run(t, m, cmd)

	task, err := s.Get(2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Title != "Typed in the TUI" {
		t.Errorf("title = %q", task.Title)
	}
	if task.Project != "web" {
		t.Errorf("project = %q, want web from the selected panel", task.Project)
	}
}

// Shift-N is a separate key rather than a prompt, so the parent is decided
// before the title is typed rather than after.
func TestShiftNCreatesASubtaskOfTheSelection(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Parent")
	m = reload(t, m)

	m, _ = key(t, m, "N")
	if m.newParent != 1 {
		t.Fatalf("newParent = %d, want 1", m.newParent)
	}
	m = typeText(t, m, "A piece of it")
	m, cmd := key(t, m, "enter")
	run(t, m, cmd)

	child, err := s.Get(2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if child.Parent != 1 {
		t.Errorf("parent = %d, want 1", child.Parent)
	}
}

// n after N must not inherit the previous parent.
func TestPlainNewTaskAfterASubtaskIsTopLevel(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Parent")
	m = reload(t, m)

	m, _ = key(t, m, "N")
	m, _ = key(t, m, "esc")
	m, _ = key(t, m, "n")
	if m.newParent != 0 {
		t.Fatalf("newParent = %d after esc then n, want 0", m.newParent)
	}
	m = typeText(t, m, "Standalone")
	m, cmd := key(t, m, "enter")
	run(t, m, cmd)

	task, _ := s.Get(2)
	if task.Parent != 0 {
		t.Errorf("parent = %d, want top-level", task.Parent)
	}
}

func TestEmptyTitleCreatesNothing(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Only one")
	m = reload(t, m)

	m, _ = key(t, m, "n")
	m, cmd := key(t, m, "enter")
	run(t, m, cmd)

	all, err := s.List(store.Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("an empty title created a task: %+v", all)
	}
}

func TestEscapeAbandonsTheInput(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Only one")
	m = reload(t, m)

	m, _ = key(t, m, "n")
	m = typeText(t, m, "Never submitted")
	m, _ = key(t, m, "esc")

	if m.mode != modeNormal {
		t.Errorf("mode = %v after esc, want normal", m.mode)
	}
	all, _ := s.List(store.Filter{IncludeDone: true})
	if len(all) != 1 {
		t.Errorf("esc still created a task: %+v", all)
	}
}

func TestDeleteAsksFirst(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Delete me")
	m = reload(t, m)

	m, _ = key(t, m, "d")
	if m.mode != modeConfirmDelete {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}

	// Anything other than y backs out.
	m, cmd := key(t, m, "n")
	m = run(t, m, cmd)
	if _, err := s.Get(1); err != nil {
		t.Fatalf("task deleted without confirmation: %v", err)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after declining, want normal", m.mode)
	}

	m, _ = key(t, m, "d")
	m, cmd = key(t, m, "y")
	run(t, m, cmd)
	if _, err := s.Get(1); err == nil {
		t.Error("y did not delete the task")
	}
}

// Answering is the one destructive-ish action gated on there being something
// to answer, because pressing a on an ordinary task should say so rather than
// open an input that goes nowhere.
func TestAnswerKeyDoesNothingWithoutAQuestion(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Nothing pending")
	m = reload(t, m)

	m, cmd := key(t, m, "a")
	if m.mode == modeAnswer {
		t.Error("a opened an answer prompt on a task with no question")
	}
	m = run(t, m, cmd)
	if m.status == "" {
		t.Error("expected a status message explaining why nothing happened")
	}
}

func TestAnsweringClearsTheQuestion(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Ambiguous work")
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Flag it or ship it?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	m = reload(t, m)

	m, _ = key(t, m, "a")
	if m.mode != modeAnswer {
		t.Fatalf("mode = %v, want the answer prompt", m.mode)
	}
	m = typeText(t, m, "Ship it")
	m, cmd := key(t, m, "enter")
	run(t, m, cmd)

	task, err := s.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Question != "" {
		t.Errorf("question still pending: %q", task.Question)
	}
	if task.Answer != "Ship it" {
		t.Errorf("answer = %q", task.Answer)
	}
}

// Refreshing under someone's cursor while they type is worse than being two
// seconds stale, so the tick must not reload except in normal mode.
func TestTickDoesNotReloadWhileTyping(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Existing")
	m = reload(t, m)

	m, _ = key(t, m, "n")
	m = typeText(t, m, "Half-typed")

	before := m.input.Value()
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(model)

	if m.input.Value() != before {
		t.Errorf("the tick disturbed the input: %q became %q", before, m.input.Value())
	}
	if m.mode != modeNew {
		t.Errorf("the tick left input mode")
	}
}

func TestFilterNarrowsTheList(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Fix the login redirect", "Write the release notes")
	m = reload(t, m)

	m, _ = key(t, m, "/")
	m = typeText(t, m, "login")
	m, cmd := key(t, m, "enter")
	m = run(t, m, cmd)
	m = reload(t, m)

	if len(m.tasks) != 1 || m.tasks[0].Entry.Title != "Fix the login redirect" {
		t.Errorf("filter did not narrow the list: %+v", m.tasks)
	}
}

// Views and projects compose rather than override, which is the whole reason
// the side panels are separate.
func TestViewAndProjectCompose(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.CreateProject(store.ActorHuman, "web", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.Create(store.ActorHuman, store.NewTask{
		Title: "Blocked in web", Project: "web", Status: store.StatusBlocked,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(store.ActorHuman, store.NewTask{
		Title: "Blocked elsewhere", Status: store.StatusBlocked,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	m = reload(t, m)
	m, _ = key(t, m, "2")
	m, cmd := key(t, m, "j") // select "web"
	m = run(t, m, cmd)
	m = reload(t, m)

	// Then the Blocked view on top of it.
	m = selectView(t, m, "Blocked")

	if len(m.tasks) != 1 || m.tasks[0].Entry.Title != "Blocked in web" {
		t.Errorf("view and project did not compose: %+v", titlesOf(m))
	}
}

// Selecting an agent narrows the list to what it holds — the one filter the
// store knows nothing about, because claims are not its concern.
func TestSelectingAnAgentNarrowsToItsClaims(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "Held by the agent", "Nobody is on this")

	now := time.Now()
	agents.Register(s, "sess-1", "claude", now)
	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	m = reload(t, m)
	if len(m.agents) != 1 {
		t.Fatalf("want 1 connected agent, got %d", len(m.agents))
	}

	m, _ = key(t, m, "3")
	m, cmd := key(t, m, "j") // off "(all)" and onto the agent
	m = run(t, m, cmd)
	m = reload(t, m)

	if len(m.tasks) != 1 || m.tasks[0].Entry.ID != 1 {
		t.Errorf("agent narrowing failed: %+v", titlesOf(m))
	}
}

func TestPanelFocusKeys(t *testing.T) {
	m, _ := newTestModel(t)

	for k, want := range map[string]panel{
		"1": panelViews, "2": panelProjects, "3": panelAgents, "4": panelTasks,
	} {
		got, _ := key(t, m, k)
		if got.focus != want {
			t.Errorf("%s focused %v, want %v", k, got.focus, want)
		}
	}

	// h from the tasks goes to the side; l comes back.
	m, _ = key(t, m, "4")
	m, _ = key(t, m, "h")
	if m.focus != panelViews {
		t.Errorf("h from tasks focused %v", m.focus)
	}
	m, _ = key(t, m, "l")
	if m.focus != panelTasks {
		t.Errorf("l from a side panel focused %v", m.focus)
	}
}

func TestTabCyclesThroughEveryPanel(t *testing.T) {
	m, _ := newTestModel(t)
	m, _ = key(t, m, "1")

	seen := map[panel]bool{m.focus: true}
	for i := 0; i < int(panelCount); i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(model)
		seen[m.focus] = true
	}
	if len(seen) != int(panelCount) {
		t.Errorf("tab reached %d of %d panels", len(seen), panelCount)
	}
	if m.focus != panelViews {
		t.Errorf("a full cycle ended on %v, want back at views", m.focus)
	}
}

// The cursor must not run off either end of a list, and G/g are the fast way
// to each end.
func TestCursorStaysInBounds(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "One", "Two", "Three")
	m = reload(t, m)

	for i := 0; i < 10; i++ {
		var cmd tea.Cmd
		m, cmd = key(t, m, "j")
		m = run(t, m, cmd)
	}
	if m.taskCursor != 2 {
		t.Errorf("taskCursor = %d after running off the end, want 2", m.taskCursor)
	}

	m, _ = key(t, m, "g")
	if m.taskCursor != 0 {
		t.Errorf("g left the cursor at %d", m.taskCursor)
	}
	m, _ = key(t, m, "G")
	if m.taskCursor != 2 {
		t.Errorf("G left the cursor at %d", m.taskCursor)
	}
}

// A list that shrinks under the cursor — an agent completing what you were
// pointing at — must not leave the cursor past the end.
func TestCursorSurvivesTheListShrinking(t *testing.T) {
	m, s := newTestModel(t)
	seed(t, s, "One", "Two", "Three")
	m = reload(t, m)
	m, _ = key(t, m, "G")

	if err := s.Delete(store.ActorAgent, 3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete(store.ActorAgent, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	m = reload(t, m)

	if m.taskCursor >= len(m.tasks) {
		t.Errorf("cursor %d is past the end of a %d-task list", m.taskCursor, len(m.tasks))
	}
	if m.View() == "" {
		t.Error("view is empty after the list shrank")
	}
}

func TestHelpOpensAndAnyKeyCloses(t *testing.T) {
	m, _ := newTestModel(t)

	m, _ = key(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("mode = %v, want help", m.mode)
	}
	if m.View() == "" {
		t.Error("the help view rendered nothing")
	}

	m, _ = key(t, m, "j")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want a key to have closed help", m.mode)
	}
}

func TestQuitSetsQuit(t *testing.T) {
	m, _ := newTestModel(t)
	m, cmd := key(t, m, "q")
	if !m.quit {
		t.Error("q did not mark the model quit")
	}
	if cmd == nil {
		t.Error("q returned no command; the runtime needs tea.Quit")
	}
}

// A store error has to be visible rather than silently rendering an empty
// list, and the message should point at the repair.
func TestLoadErrorIsShown(t *testing.T) {
	m, _ := newTestModel(t)
	next, _ := m.Update(loadedMsg{err: errFake{}})
	m = next.(model)

	if m.err == nil {
		t.Fatal("the error was swallowed")
	}
	if view := m.View(); view == "" {
		t.Error("the error view rendered nothing")
	}
}

type errFake struct{}

func (errFake) Error() string { return "state.json is unreadable" }

func titlesOf(m model) []string {
	out := make([]string, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Entry.Title)
	}
	return out
}
