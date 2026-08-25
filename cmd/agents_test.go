package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/agents"
	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
)

// openStore is what the commands use, so tests that need to set up agent state
// go through the same door rather than reaching past it.
func testStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestAgentsCommandWhenNobodyIsConnected(t *testing.T) {
	dir := t.TempDir()

	out := mustRun(t, dir, "agents")
	if !strings.Contains(out, "No agents connected") {
		t.Errorf("got %q", out)
	}

	out = mustRun(t, dir, "agents", "--json")
	var sessions []agents.Session
	decode(t, out, &sessions)
	if len(sessions) != 0 {
		t.Errorf("want an empty array, got %+v", sessions)
	}
}

func TestAgentsCommandListsConnectedAgentsAndWhatTheyHold(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Held task")

	now := time.Now()
	agents.Register(s, "sess-1", "claude", now.Add(-30*time.Minute))
	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	out := mustRun(t, dir, "agents")
	for _, want := range []string{"claude", "#1", "holding"} {
		if !strings.Contains(out, want) {
			t.Errorf("agents output missing %q:\n%s", want, out)
		}
	}

	// An agent holding nothing is still listed — being connected and being
	// busy are different questions.
	agents.Register(s, "sess-2", "idle-agent", now)
	out = mustRun(t, dir, "agents")
	if !strings.Contains(out, "idle-agent") || !strings.Contains(out, "idle") {
		t.Errorf("an idle agent was not shown as connected:\n%s", out)
	}
}

func TestClaimsCommand(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Being worked on")

	out := mustRun(t, dir, "claims")
	if !strings.Contains(out, "No agent is working on anything") {
		t.Errorf("got %q", out)
	}

	// Held recently enough that the lease is still live: a claim older than
	// its TTL has expired, and an expired claim is not a claim.
	now := time.Now()
	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.ExplicitTTL, true, now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("Take: %v", err)
	}

	out = mustRun(t, dir, "claims")
	for _, want := range []string{"#1", "Being worked on", "claude", "5m"} {
		if !strings.Contains(out, want) {
			t.Errorf("claims output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(implicit)") {
		t.Errorf("an explicit claim was labelled implicit:\n%s", out)
	}
}

// A claim taken automatically on a write is weaker evidence than one the agent
// asked for, and the output says so.
func TestClaimsMarksImplicitLeases(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Touched by an agent")

	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.DefaultTTL, false, time.Now()); err != nil {
		t.Fatalf("Take: %v", err)
	}

	out := mustRun(t, dir, "claims")
	if !strings.Contains(out, "(implicit)") {
		t.Errorf("implicit claim not marked:\n%s", out)
	}
}

func TestClaimsJSON(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Task")
	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.ExplicitTTL, true, time.Now()); err != nil {
		t.Fatalf("Take: %v", err)
	}

	var held []claim.Claim
	decode(t, mustRun(t, dir, "claims", "--json"), &held)
	if len(held) != 1 || held[0].TaskID != 1 || held[0].By != "claude" {
		t.Errorf("got %+v", held)
	}
}

func TestQuestionsAndAnswer(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Migrate the session store")

	out := mustRun(t, dir, "questions")
	if !strings.Contains(out, "Nothing is waiting on you") {
		t.Errorf("got %q", out)
	}

	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Keep the old cookies working?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	out = mustRun(t, dir, "questions")
	for _, want := range []string{"#1", "claude asks", "Keep the old cookies working?", "taskgo answer 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("questions output missing %q:\n%s", want, out)
		}
	}

	// The alias exists because "asks" is what people type.
	if out := mustRun(t, dir, "asks"); !strings.Contains(out, "claude asks") {
		t.Errorf("the asks alias did not work:\n%s", out)
	}

	out = mustRun(t, dir, "answer", "1", "Force", "a", "fresh", "login")
	if !strings.Contains(out, "Answered #1") {
		t.Errorf("got %q", out)
	}

	task, err := s.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Question != "" {
		t.Errorf("question still pending: %q", task.Question)
	}
	if task.Answer != "Force a fresh login" {
		t.Errorf("answer = %q, want the bare words joined", task.Answer)
	}
	// The exchange is the durable record, so it lands in the notes.
	if !strings.Contains(task.Notes, "Force a fresh login") {
		t.Errorf("the answer is not in the notes:\n%s", task.Notes)
	}

	if out := mustRun(t, dir, "questions"); !strings.Contains(out, "Nothing is waiting on you") {
		t.Errorf("still waiting after an answer:\n%s", out)
	}
}

// An answer resolves its task the same way every other command does, so a
// title works where an id would.
func TestAnswerResolvesByTitle(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Migrate the session store")
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	mustRun(t, dir, "answer", "session", "Go ahead")

	task, _ := s.Get(1)
	if task.Answer != "Go ahead" {
		t.Errorf("answer = %q", task.Answer)
	}
}

func TestAnsweringSomethingNobodyAskedIsAnError(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Nothing pending")

	if _, err := run(t, dir, "answer", "1", "an answer to nothing"); err == nil {
		t.Error("expected an error answering a task with no question")
	}
}

func TestQuestionsJSONCarriesTheQuestion(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Ask about me")
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Which way?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	var waiting []store.IndexEntry
	decode(t, mustRun(t, dir, "questions", "--json"), &waiting)
	if len(waiting) != 1 {
		t.Fatalf("got %+v", waiting)
	}
	if waiting[0].Question != "Which way?" || waiting[0].AskedBy != "claude" {
		t.Errorf("got %+v", waiting[0])
	}
}

// A task an agent is stuck on takes the status column, because nothing is
// progressing until it is answered.
func TestWaitingTasksAreMarkedInTheList(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Stuck")
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	out := mustRun(t, dir, "list")
	line := lineContaining(t, out, "Stuck")
	if !strings.Contains(line, "?") {
		t.Errorf("waiting task not marked with ?: %q", line)
	}

	// And --waiting narrows to exactly those.
	mustRun(t, dir, "add", "Not stuck")
	out = mustRun(t, dir, "list", "--waiting")
	if strings.Contains(out, "Not stuck") {
		t.Errorf("--waiting showed an unrelated task:\n%s", out)
	}
}

func TestReopenPutsATaskBack(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Finish me")
	mustRun(t, dir, "done", "1")

	out := mustRun(t, dir, "reopen", "1")
	if !strings.Contains(out, "#1") {
		t.Errorf("got %q", out)
	}

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)
	if len(tasks) != 1 || tasks[0].Status != store.StatusTodo {
		t.Errorf("task not reopened: %+v", tasks)
	}
}

func TestShowRendersOneTaskInFull(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Fix the login redirect",
		"--project", "web", "--tag", "auth", "--priority", "high", "--due", "2026-12-25")
	mustRun(t, dir, "note", "1", "the redirect loops on Safari")

	out := mustRun(t, dir, "show", "1")
	for _, want := range []string{"Fix the login redirect", "web", "auth", "high", "2026-12-25", "Safari"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestShowJSONIncludesNotes(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "With notes", "--notes", "the body")

	var task store.Task
	decode(t, mustRun(t, dir, "show", "1", "--json"), &task)
	if task.Notes != "the body" {
		t.Errorf("notes = %q", task.Notes)
	}
}

// The tree draws guides so a subtask is visibly a subtask, and a parent
// carries its progress.
func TestListTreeShowsHierarchyAndProgress(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Ship the thing")
	mustRun(t, dir, "add", "First piece", "--parent", "1")
	mustRun(t, dir, "add", "Second piece", "--parent", "1")
	mustRun(t, dir, "done", "2")

	out := mustRun(t, dir, "list", "--tree", "--all")
	if !strings.Contains(out, "[1/2]") {
		t.Errorf("parent progress missing:\n%s", out)
	}
	if !strings.Contains(out, "└─") && !strings.Contains(out, "├─") {
		t.Errorf("no tree guides drawn:\n%s", out)
	}

	// --parent lists one task's children on their own.
	out = mustRun(t, dir, "list", "--parent", "1", "--all")
	if strings.Contains(out, "Ship the thing") {
		t.Errorf("--parent included the parent itself:\n%s", out)
	}
	if !strings.Contains(out, "First piece") {
		t.Errorf("--parent missed a child:\n%s", out)
	}
}

func TestStatusCommandCountsWaiting(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "One")
	mustRun(t, dir, "add", "Two")
	if _, err := s.Ask(store.ActorAgent, 1, "claude", "Well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	var summary struct {
		Total    int            `json:"total"`
		Open     int            `json:"open"`
		ByStatus map[string]int `json:"byStatus"`
		DataDir  string         `json:"dataDir"`
	}
	out := mustRun(t, dir, "status", "--json")
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if summary.Total != 2 || summary.Open != 2 {
		t.Errorf("got %+v", summary)
	}
	if summary.DataDir != dir {
		t.Errorf("dataDir = %q, want %q", summary.DataDir, dir)
	}
}

func lineContaining(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, out)
	return ""
}

// An expired lease is not a claim: agents crash, and a lease that cannot
// expire becomes a permanent lie about work nobody is doing.
func TestExpiredClaimsAreNotListed(t *testing.T) {
	dir := t.TempDir()
	s := testStore(t, dir)
	mustRun(t, dir, "add", "Abandoned")

	stale := time.Now().Add(-2 * claim.ExplicitTTL)
	if _, err := claim.Take(s, 1, "claude", "sess-1", claim.ExplicitTTL, true, stale); err != nil {
		t.Fatalf("Take: %v", err)
	}

	if out := mustRun(t, dir, "claims"); !strings.Contains(out, "No agent is working on anything") {
		t.Errorf("an expired claim was still listed:\n%s", out)
	}
}

func TestShortDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "25h00m"},
	}
	for _, tc := range cases {
		if got := shortDuration(tc.in); got != tc.want {
			t.Errorf("shortDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
