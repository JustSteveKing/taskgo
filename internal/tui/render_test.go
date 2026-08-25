package tui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/charmbracelet/lipgloss"
)

// The rendering tests assert on the text that reaches the screen with styling
// stripped, because the styling is a choice that will change and the words are
// the contract.
func plain(s string) string {
	return lipgloss.NewStyle().Render(stripANSI(s))
}

func stripANSI(s string) string {
	var b strings.Builder
	skipping := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skipping = true
		case skipping && r == 'm':
			skipping = false
		case !skipping:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestDuePlainReadsAsAnInstruction(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		due  string
		want string
	}{
		{"2026-08-22", "3d late"},
		{"2026-08-24", "1d late"},
		{"2026-08-25", "today"},
		{"2026-08-26", "tomorrow"},
		{"2026-08-30", "in 5d"},
		// Past a week the relative form stops helping and the date is better.
		{"2026-10-01", "2026-10-01"},
	}
	for _, tc := range cases {
		if got := duePlain(store.DueDate(tc.due), now); got != tc.want {
			t.Errorf("duePlain(%s) = %q, want %q", tc.due, got, tc.want)
		}
	}
}

// dueStyled picks a colour per status but must never change the words. Colour
// itself is not asserted: lipgloss emits no escape codes without a terminal,
// so under `go test` every style renders identically. The branch that matters
// — a completed task is never chasing you, so its date is never red — is a
// colour decision, and duePlain above covers the text.
func TestDueStyledLeavesTheLabelAlone(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	late := store.DueDate("2026-01-01")

	for _, status := range []store.Status{
		store.StatusTodo, store.StatusDoing, store.StatusBlocked, store.StatusDone,
	} {
		got := stripANSI(dueStyled(late, status, now))
		if want := duePlain(late, now); got != want {
			t.Errorf("dueStyled(%s) = %q, want the plain label %q", status, got, want)
		}
	}

	// And the same for the dates that take the other branches.
	for _, due := range []string{"2026-08-25", "2026-08-26", "2026-09-30"} {
		d := store.DueDate(due)
		if got, want := stripANSI(dueStyled(d, store.StatusTodo, now)), duePlain(d, now); got != want {
			t.Errorf("dueStyled(%s) = %q, want %q", due, got, want)
		}
	}
}

func TestDetailPaneWithNothingSelected(t *testing.T) {
	m, _ := newTestModel(t)
	if got := stripANSI(m.detailContent(60, 20)); !strings.Contains(got, "No task selected") {
		t.Errorf("got %q", got)
	}
}

func TestDetailPaneShowsTheTaskAndItsMetadata(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.Create(store.ActorHuman, store.NewTask{
		Title: "Fix the login redirect", Project: "web", Tags: []string{"auth"},
		Priority: store.PriorityHigh, Status: store.StatusDoing,
		Due: ptrDue("2026-12-25"), Notes: "the redirect loops on Safari",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m = reload(t, m)
	m = run(t, m, m.loadDetail())

	got := stripANSI(m.detailContent(70, 20))
	for _, want := range []string{
		"Fix the login redirect", "doing", "high", "@web", "#auth", "Safari",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail pane missing %q:\n%s", want, got)
		}
	}
}

func TestDetailPaneShowsSubtaskProgressAndParentage(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Parent"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Child", Parent: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m = reload(t, m)
	m = run(t, m, m.loadDetail())

	if got := stripANSI(m.detailContent(70, 20)); !strings.Contains(got, "0/1 subtasks done") {
		t.Errorf("parent progress missing:\n%s", got)
	}

	// Move to the child and it should say whose child it is.
	m, cmd := key(t, m, "j")
	m = run(t, m, cmd)
	m = run(t, m, m.loadDetail())
	if got := stripANSI(m.detailContent(70, 20)); !strings.Contains(got, "subtask of #1") {
		t.Errorf("parentage missing:\n%s", got)
	}
}

// A question is the one thing worth showing in full rather than eliding: a
// truncated question cannot be answered.
func TestDetailPaneShowsAPendingQuestionInFull(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Ambiguous"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	question := "Should the old cookies keep working, or should everyone be forced " +
		"to log in again the next time they visit?"
	if _, err := s.Ask(store.ActorAgent, 1, "claude", question); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	m = reload(t, m)
	m = run(t, m, m.loadDetail())

	got := stripANSI(m.detailContent(50, 20))
	if !strings.Contains(got, "claude is waiting on you") {
		t.Errorf("the asker is not named:\n%s", got)
	}
	if !strings.Contains(got, "press a to answer") {
		t.Errorf("the way out is not offered:\n%s", got)
	}

	// Wrapped across lines, but every word still present.
	flat := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(flat, question) {
		t.Errorf("the question was truncated:\n%s", got)
	}
}

func TestDetailPaneShowsTheLastAnswer(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Answered"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if _, err := s.Answer(store.ActorHuman, 1, "Ship it"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	m = reload(t, m)
	m = run(t, m, m.loadDetail())

	got := stripANSI(m.detailContent(70, 20))
	if !strings.Contains(got, "last answer") || !strings.Contains(got, "Ship it") {
		t.Errorf("the answer is not shown:\n%s", got)
	}
	if strings.Contains(got, "waiting on you") {
		t.Errorf("an answered task still reads as waiting:\n%s", got)
	}
}

// An implicit lease is weaker evidence than one the agent asked for, and the
// detail pane should read that way rather than claiming more than it knows.
func TestDetailPaneDistinguishesImplicitClaims(t *testing.T) {
	m, s := newTestModel(t)
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Held"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now()

	// Two tasks rather than one lease downgraded: Take deliberately keeps a
	// claim explicit once it is (existing.Explicit || explicit), so a later
	// write cannot weaken a lease the agent asked for.
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Touched"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := claim.Take(s, 1, "claude", "sess", claim.ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if _, err := claim.Take(s, 2, "claude", "sess", claim.DefaultTTL, false, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	m = reload(t, m)
	m = run(t, m, m.loadDetail())
	if got := stripANSI(m.detailContent(70, 20)); !strings.Contains(got, "working on this") {
		t.Errorf("explicit claim not shown:\n%s", got)
	}

	m, cmd := key(t, m, "j")
	m = run(t, m, cmd)
	m = run(t, m, m.loadDetail())
	if got := stripANSI(m.detailContent(70, 20)); !strings.Contains(got, "recently active here") {
		t.Errorf("implicit claim reads as strongly as an explicit one:\n%s", got)
	}
}

func TestStatusLineCounts(t *testing.T) {
	m, s := newTestModel(t)
	for _, title := range []string{"One", "Two", "Three"} {
		if _, err := s.Create(store.ActorHuman, store.NewTask{Title: title}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := s.Complete(store.ActorHuman, 3); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	m = reload(t, m)

	got := stripANSI(m.statusLine())
	for _, want := range []string{"2 open", "3 total", "1 waiting on you"} {
		if !strings.Contains(got, want) {
			t.Errorf("status line missing %q: %q", want, got)
		}
	}
}

func TestWrapTextKeepsEveryWord(t *testing.T) {
	text := "Should the old cookies keep working, or should everyone be forced to log in again?"

	lines := wrapText(text, 20)
	if len(lines) < 2 {
		t.Fatalf("nothing wrapped: %v", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 20 {
			t.Errorf("line exceeds the width: %q", line)
		}
	}
	if got := strings.Join(strings.Fields(strings.Join(lines, " ")), " "); got != text {
		t.Errorf("wrapping changed the text:\n got %q\nwant %q", got, text)
	}

	// A single word longer than the width cannot be broken, and must not be
	// dropped trying.
	long := strings.Repeat("x", 40)
	if lines := wrapText(long, 10); len(lines) != 1 || lines[0] != long {
		t.Errorf("an unbreakable word was mangled: %v", lines)
	}

	// Paragraph breaks survive, because notes are Markdown.
	if lines := wrapText("one\n\ntwo", 20); len(lines) != 3 || lines[1] != "" {
		t.Errorf("paragraph break lost: %v", lines)
	}
}

func TestHumanDurationReadsOutLoud(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "for 5m"},
		{90 * time.Minute, "for 1h30m"},
	}
	for _, tc := range cases {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHelpAndErrorViewsRender(t *testing.T) {
	m, _ := newTestModel(t)

	m.mode = modeHelp
	help := stripANSI(m.View())
	for _, want := range []string{"Panels", "Moving", "Tasks", "answer an agent's question"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}

	m.mode = modeNormal
	m.err = errFake{}
	shown := stripANSI(m.View())
	if !strings.Contains(shown, "state.json is unreadable") {
		t.Errorf("the error itself is not shown:\n%s", shown)
	}
	// The repair is the useful half of an error message.
	if !strings.Contains(shown, "taskgo reindex") {
		t.Errorf("the error view does not name the fix:\n%s", shown)
	}
}

func TestPanelTitles(t *testing.T) {
	for p, want := range map[panel]string{
		panelViews: "Views", panelProjects: "Projects",
		panelAgents: "Agents", panelTasks: "Tasks",
	} {
		if got := p.title(); got != want {
			t.Errorf("panel %d title = %q, want %q", p, got, want)
		}
	}
}

// The TUI keeps its own copy of shellQuote, so it needs its own proof: the
// path reaches the editor as one argument whatever is in it.
func TestTUIShellQuoteSurvivesTheShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, path := range []string{
		"/home/me/tasks/1.md",
		"/home/steve's/1.md",
		"/tmp/x; echo INJECTED",
		"/tmp/$(echo INJECTED)/1.md",
	} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+shellQuote(path)).Output()
		if err != nil {
			t.Errorf("sh rejected %q: %v", path, err)
			continue
		}
		if string(out) != path {
			t.Errorf("round trip changed %q into %q", path, out)
		}
	}
}

func TestEditorCommandPrefersVisual(t *testing.T) {
	t.Setenv("VISUAL", "code")
	t.Setenv("EDITOR", "nano")
	if got := editorCommand(); got != "code" {
		t.Errorf("editorCommand() = %q, want code", got)
	}

	t.Setenv("VISUAL", "")
	if got := editorCommand(); got != "nano" {
		t.Errorf("editorCommand() = %q, want nano", got)
	}

	t.Setenv("EDITOR", "")
	if got := editorCommand(); got != "vi" {
		t.Errorf("editorCommand() = %q, want vi", got)
	}
}

func ptrDue(s string) *store.DueDate {
	d := store.DueDate(s)
	return &d
}
