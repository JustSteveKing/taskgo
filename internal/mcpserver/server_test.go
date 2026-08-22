package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/agents"
	"github.com/JustSteveKing/taskgo/internal/claim"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires a real MCP client to a real server over an in-memory
// transport, so these tests exercise the actual protocol path — schema
// validation included — rather than calling the handlers directly.
func connect(t *testing.T) (*mcp.ClientSession, *store.Store) {
	t.Helper()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	srv, _ := New(s, "test")
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession, s
}

// call invokes a tool and decodes its structured output.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, textOf(res))
	}
	if out == nil {
		return
	}
	if res.StructuredContent == nil {
		t.Fatalf("%s: no structured content; text was %q", name, textOf(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: re-marshal structured content: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: decode %s: %v", name, raw, err)
	}
}

// callExpectingError asserts the tool reports a failure rather than succeeding.
func callExpectingError(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		t.Fatalf("%s: expected an error, got success", name)
	}
	return textOf(res)
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Every tool named in the project brief must actually be exposed. This is the
// test that catches a tool being renamed or dropped by accident.
func TestAllPromisedToolsAreRegistered(t *testing.T) {
	cs, _ := connect(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description; an agent has to guess what it does", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
	}

	for _, want := range []string{
		"list_tasks", "get_task", "create_task", "update_task", "complete_task",
		"reopen_task", "list_projects", "create_project", "search_tasks",
		"get_overdue", "get_today", "add_note", "get_activity",
	} {
		if !got[want] {
			t.Errorf("tool %q is not registered", want)
		}
	}
}

// The core promise of the project: what an agent writes, a human sees.
func TestAgentWritesAreVisibleToTheStoreAndAttributed(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{
		"title":    "Written by an agent",
		"project":  "demo",
		"priority": "high",
		"due":      "2026-09-01",
		"tags":     []string{"mcp"},
	}, &created)

	if created.ID == 0 {
		t.Fatal("create_task returned no id")
	}

	// Read it back through the store, the way the CLI would.
	fromDisk, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("store.Get after agent create: %v", err)
	}
	if fromDisk.Title != "Written by an agent" {
		t.Errorf("title on disk = %q", fromDisk.Title)
	}
	if fromDisk.Priority != store.PriorityHigh {
		t.Errorf("priority on disk = %q", fromDisk.Priority)
	}

	// And the change must be attributed to the agent, not to a human.
	events, err := s.Activity(0)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("agent create wrote no activity")
	}
	if events[0].Actor != store.ActorAgent {
		t.Errorf("actor = %q, want %q", events[0].Actor, store.ActorAgent)
	}
}

// The other direction: what a human changes, the agent sees on its next call.
// This is why the server holds no cached index.
func TestHumanWritesAreVisibleToTheAgentImmediately(t *testing.T) {
	cs, s := connect(t)

	// Agent looks first and sees nothing.
	var before taskList
	call(t, cs, "list_tasks", map[string]any{}, &before)
	if before.Count != 0 {
		t.Fatalf("expected an empty store, got %d", before.Count)
	}

	// Human adds a task behind the running server's back.
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Added by the human"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var after taskList
	call(t, cs, "list_tasks", map[string]any{}, &after)
	if after.Count != 1 {
		t.Fatalf("agent did not see the human's task; count = %d", after.Count)
	}
	if after.Tasks[0].Title != "Added by the human" {
		t.Errorf("title = %q", after.Tasks[0].Title)
	}
}

func TestCompleteAndReopenRoundTrip(t *testing.T) {
	cs, _ := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Round trip"}, &created)

	var done store.Task
	call(t, cs, "complete_task", map[string]any{"id": created.ID}, &done)
	if done.Status != store.StatusDone {
		t.Errorf("status = %q, want done", done.Status)
	}

	var reopened store.Task
	call(t, cs, "reopen_task", map[string]any{"id": created.ID}, &reopened)
	if reopened.Status != store.StatusTodo {
		t.Errorf("status = %q, want todo", reopened.Status)
	}
}

// add_note must append. An agent recording progress should never be able to
// destroy what a human wrote.
func TestAddNotePreservesExistingNotes(t *testing.T) {
	cs, _ := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{
		"title": "Has notes",
		"notes": "Human wrote this.",
	}, &created)

	var noted store.Task
	call(t, cs, "add_note", map[string]any{
		"id":   created.ID,
		"note": "Agent appended this.",
	}, &noted)

	if !strings.Contains(noted.Notes, "Human wrote this.") {
		t.Error("add_note destroyed the human's notes")
	}
	if !strings.Contains(noted.Notes, "Agent appended this.") {
		t.Error("add_note did not record the agent's note")
	}
}

// update_task must leave omitted fields alone, and clear_due must be able to
// remove a due date — the distinction the pointer fields exist for.
func TestUpdateOmittedFieldsAreLeftAlone(t *testing.T) {
	cs, _ := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{
		"title": "Original", "due": "2026-09-01", "project": "demo",
	}, &created)

	var updated store.Task
	call(t, cs, "update_task", map[string]any{
		"id": created.ID, "status": "doing",
	}, &updated)

	if updated.Due == nil {
		t.Error("an unrelated update cleared the due date")
	}
	if updated.Project != "demo" {
		t.Errorf("project = %q, want it untouched", updated.Project)
	}
	if updated.Status != store.StatusDoing {
		t.Errorf("status = %q, want doing", updated.Status)
	}

	var cleared store.Task
	call(t, cs, "update_task", map[string]any{
		"id": created.ID, "clear_due": true,
	}, &cleared)
	if cleared.Due != nil {
		t.Errorf("clear_due left due = %v", cleared.Due)
	}
}

func TestSearchFindsNotesBodies(t *testing.T) {
	cs, _ := connect(t)

	call(t, cs, "create_task", map[string]any{
		"title": "Nothing obvious in the title",
		"notes": "the needle is in here",
	}, nil)
	call(t, cs, "create_task", map[string]any{"title": "Unrelated"}, nil)

	var found taskList
	call(t, cs, "search_tasks", map[string]any{"query": "needle"}, &found)
	if found.Count != 1 {
		t.Fatalf("search matched %d tasks, want 1", found.Count)
	}
}

func TestGetTodayIncludesOverdue(t *testing.T) {
	cs, _ := connect(t)

	call(t, cs, "create_task", map[string]any{"title": "Late", "due": "2020-01-01"}, nil)
	call(t, cs, "create_task", map[string]any{"title": "Due today", "due": "today"}, nil)
	call(t, cs, "create_task", map[string]any{"title": "No date"}, nil)

	var today taskList
	call(t, cs, "get_today", map[string]any{}, &today)
	if today.Count != 2 {
		t.Errorf("get_today returned %d, want the overdue one and today's: %+v", today.Count, today.Tasks)
	}

	var overdue taskList
	call(t, cs, "get_overdue", map[string]any{}, &overdue)
	if overdue.Count != 1 {
		t.Errorf("get_overdue returned %d, want 1", overdue.Count)
	}
}

func TestListTasksHidesDoneUnlessAsked(t *testing.T) {
	cs, _ := connect(t)

	var open store.Task
	call(t, cs, "create_task", map[string]any{"title": "Open"}, &open)
	var closed store.Task
	call(t, cs, "create_task", map[string]any{"title": "Closed"}, &closed)
	call(t, cs, "complete_task", map[string]any{"id": closed.ID}, nil)

	var listed taskList
	call(t, cs, "list_tasks", map[string]any{}, &listed)
	if listed.Count != 1 {
		t.Errorf("default list returned %d, want only the open task", listed.Count)
	}

	var all taskList
	call(t, cs, "list_tasks", map[string]any{"include_done": true}, &all)
	if all.Count != 2 {
		t.Errorf("include_done returned %d, want 2", all.Count)
	}
}

func TestProjectTools(t *testing.T) {
	cs, _ := connect(t)

	call(t, cs, "create_project", map[string]any{
		"name": "demo", "description": "a demo project",
	}, nil)
	call(t, cs, "create_task", map[string]any{"title": "In demo", "project": "demo"}, nil)

	var projects projectList
	call(t, cs, "list_projects", map[string]any{}, &projects)
	if projects.Count != 1 {
		t.Fatalf("got %d projects, want 1", projects.Count)
	}
	if projects.Projects[0].Open != 1 {
		t.Errorf("open count = %d, want 1", projects.Projects[0].Open)
	}
}

func TestGetActivityDistinguishesActors(t *testing.T) {
	cs, s := connect(t)

	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Human task"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	call(t, cs, "create_task", map[string]any{"title": "Agent task"}, nil)

	var activity activityList
	call(t, cs, "get_activity", map[string]any{}, &activity)
	if activity.Count != 2 {
		t.Fatalf("got %d events, want 2", activity.Count)
	}
	if activity.Events[0].Actor != store.ActorAgent {
		t.Errorf("newest actor = %q, want agent", activity.Events[0].Actor)
	}
	if activity.Events[1].Actor != store.ActorHuman {
		t.Errorf("older actor = %q, want human", activity.Events[1].Actor)
	}
}

// Errors must reach the agent as tool errors it can read and act on, not as
// silent successes or transport failures.
func TestErrorsSurfaceToTheAgent(t *testing.T) {
	cs, _ := connect(t)

	if msg := callExpectingError(t, cs, "get_task", map[string]any{"id": 999}); !strings.Contains(msg, "not found") {
		t.Errorf("missing task error = %q, want it to say not found", msg)
	}
	if msg := callExpectingError(t, cs, "create_task", map[string]any{"title": ""}); msg == "" {
		t.Error("empty title produced no error message")
	}
	if msg := callExpectingError(t, cs, "create_task", map[string]any{
		"title": "Bad status", "status": "nonsense",
	}); !strings.Contains(msg, "nonsense") {
		t.Errorf("bad status error = %q, want it to name the bad value", msg)
	}
}

// ---------------------------------------------------------------- claims

// The agent's identity comes from the MCP handshake, not a tool argument, so
// what shows up in the UI is what actually connected.
func TestClaimRecordsTheAgentFromTheHandshake(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Work on me"}, &created)

	var out map[string]any
	call(t, cs, "claim_task", map[string]any{"id": created.ID}, &out)

	set, err := claim.Load(s, time.Now())
	if err != nil {
		t.Fatalf("claim.Load: %v", err)
	}
	c, ok := set.Get(created.ID)
	if !ok {
		t.Fatal("no claim recorded")
	}
	// connect() introduces the client as "test-client".
	if c.By != "test-client" {
		t.Errorf("claim.By = %q, want the client's declared identity", c.By)
	}
	if !c.Explicit {
		t.Error("claim_task should record an explicit claim")
	}
}

// An agent that knows nothing about claiming still gets highlighted, because
// writing to a task takes a short lease on its own.
func TestWritingTakesAnImplicitClaim(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Implicitly claimed"}, &created)

	set, _ := claim.Load(s, time.Now())
	c, ok := set.Get(created.ID)
	if !ok {
		t.Fatal("a write should take an implicit claim")
	}
	if c.Explicit {
		t.Error("a write should not be recorded as an explicit claim")
	}
}

// Completing the work ends the lease: nothing should still be shown as being
// worked on once it is done.
func TestCompletingReleasesTheClaim(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Finish me"}, &created)
	call(t, cs, "claim_task", map[string]any{"id": created.ID}, nil)

	if set, _ := claim.Load(s, time.Now()); len(set) != 1 {
		t.Fatalf("expected a claim before completing, got %+v", set)
	}

	call(t, cs, "complete_task", map[string]any{"id": created.ID}, nil)

	if set, _ := claim.Load(s, time.Now()); len(set) != 0 {
		t.Errorf("completing left a claim behind: %+v", set)
	}
}

func TestReleaseTaskDropsTheClaim(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Give up on me"}, &created)
	call(t, cs, "claim_task", map[string]any{"id": created.ID}, nil)
	call(t, cs, "release_task", map[string]any{"id": created.ID}, nil)

	if set, _ := claim.Load(s, time.Now()); len(set) != 0 {
		t.Errorf("release_task left a claim: %+v", set)
	}
}

func TestListClaimsReportsWhatIsHeld(t *testing.T) {
	cs, _ := connect(t)

	var a, b store.Task
	call(t, cs, "create_task", map[string]any{"title": "One"}, &a)
	call(t, cs, "create_task", map[string]any{"title": "Two"}, &b)
	call(t, cs, "claim_task", map[string]any{"id": a.ID}, nil)

	var listed claimList
	call(t, cs, "list_claims", map[string]any{}, &listed)

	// Both are held — one explicitly, one implicitly from being created.
	if listed.Count != 2 {
		t.Fatalf("got %d claims, want 2: %+v", listed.Count, listed.Claims)
	}
	var explicit int
	for _, c := range listed.Claims {
		if c.Explicit {
			explicit++
		}
	}
	if explicit != 1 {
		t.Errorf("want exactly one explicit claim, got %d", explicit)
	}
}

// Claiming a task that does not exist must fail rather than record a lease on
// nothing.
func TestClaimingAMissingTaskFails(t *testing.T) {
	cs, _ := connect(t)
	if msg := callExpectingError(t, cs, "claim_task", map[string]any{"id": 999}); !strings.Contains(msg, "not found") {
		t.Errorf("error = %q", msg)
	}
}

// ------------------------------------------------------------- questions

// ask_human must not block. An agent that sat in a tool call waiting for a
// person to look at their screen would be useless.
func TestAskHumanReturnsImmediatelyAndFlagsTheTask(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Decide something"}, &created)

	done := make(chan struct{})
	go func() {
		defer close(done)
		call(t, cs, "ask_human", map[string]any{
			"id": created.ID, "question": "JWTs or session cookies?",
		}, nil)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ask_human blocked; it must return without waiting for a human")
	}

	task, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !task.AwaitingAnswer() {
		t.Error("task is not flagged as waiting")
	}
	if task.AskedBy != "test-client" {
		t.Errorf("AskedBy = %q, want the client's declared identity", task.AskedBy)
	}
}

// The loop that matters: agent asks, human answers out of band, agent polls
// and gets on with it.
func TestAgentSeesTheHumansAnswerOnItsNextCheck(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Blocked on a decision"}, &created)
	call(t, cs, "ask_human", map[string]any{"id": created.ID, "question": "which?"}, nil)

	var pending answerOut
	call(t, cs, "check_answer", map[string]any{"id": created.ID}, &pending)
	if pending.Answered {
		t.Fatal("check_answer said answered before anyone answered")
	}

	if _, err := s.Answer(store.ActorHuman, created.ID, "the second one"); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	var replied answerOut
	call(t, cs, "check_answer", map[string]any{"id": created.ID}, &replied)
	if !replied.Answered || replied.Answer != "the second one" {
		t.Errorf("agent did not see the answer: %+v", replied)
	}
}

func TestListQuestionsShowsWhatIsWaiting(t *testing.T) {
	cs, _ := connect(t)

	var a, b store.Task
	call(t, cs, "create_task", map[string]any{"title": "One"}, &a)
	call(t, cs, "create_task", map[string]any{"title": "Two"}, &b)
	call(t, cs, "ask_human", map[string]any{"id": a.ID, "question": "eh?"}, nil)

	var listed questionList
	call(t, cs, "list_questions", map[string]any{}, &listed)
	if listed.Count != 1 {
		t.Fatalf("got %d questions, want 1: %+v", listed.Count, listed.Questions)
	}
	if listed.Questions[0].Task != a.ID || listed.Questions[0].AskedBy != "test-client" {
		t.Errorf("question = %+v", listed.Questions[0])
	}
}

// Asking holds the lease, so a task waiting on a human does not also look
// abandoned.
func TestAskingHoldsTheClaim(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Ask and hold"}, &created)
	call(t, cs, "ask_human", map[string]any{"id": created.ID, "question": "hmm?"}, nil)

	set, _ := claim.Load(s, time.Now())
	if _, ok := set.Get(created.ID); !ok {
		t.Error("asking should keep the task claimed")
	}
}

// There is deliberately no tool for an agent to answer its own question.
func TestAgentsCannotAnswerThemselves(t *testing.T) {
	cs, _ := connect(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "answer_question" || tool.Name == "answer" {
			t.Errorf("tool %q lets an agent answer its own question, defeating the point of asking", tool.Name)
		}
	}
}

// ---------------------------------------------------------------- sessions

// Every tool must mark the agent as present, not just the ones that write.
// An agent that connects and only reads is still here, and an empty Agents
// panel while an agent is plainly running would be worse than no panel.
func TestEveryToolMarksTheAgentAsConnected(t *testing.T) {
	readOnly := []struct {
		tool string
		args map[string]any
	}{
		{"list_tasks", map[string]any{}},
		{"get_today", map[string]any{}},
		{"get_overdue", map[string]any{}},
		{"list_projects", map[string]any{}},
		{"get_activity", map[string]any{}},
		{"list_claims", map[string]any{}},
		{"list_questions", map[string]any{}},
		{"search_tasks", map[string]any{"query": "anything"}},
	}

	for _, tc := range readOnly {
		t.Run(tc.tool, func(t *testing.T) {
			cs, s := connect(t)
			call(t, cs, tc.tool, tc.args, nil)

			connected, err := agents.List(s)
			if err != nil {
				t.Fatalf("agents.List: %v", err)
			}
			if len(connected) != 1 {
				t.Fatalf("%s did not register the agent: %+v", tc.tool, connected)
			}
			if connected[0].Name != "test-client" {
				t.Errorf("session name = %q", connected[0].Name)
			}
		})
	}
}

// claim_task in particular: it identifies the session, and used to forget to
// register it, so an agent holding tasks was missing from the roster.
func TestClaimingRegistersTheSession(t *testing.T) {
	cs, s := connect(t)

	var created store.Task
	call(t, cs, "create_task", map[string]any{"title": "Held"}, &created)
	call(t, cs, "claim_task", map[string]any{"id": created.ID}, nil)

	connected, err := agents.List(s)
	if err != nil {
		t.Fatalf("agents.List: %v", err)
	}
	if len(connected) != 1 {
		t.Fatalf("agent holding a task is not in the roster: %+v", connected)
	}
}

// ---------------------------------------------------------------- subtasks

func TestBreakDownCreatesChildrenInheritingTheProject(t *testing.T) {
	cs, s := connect(t)

	var parent store.Task
	call(t, cs, "create_task", map[string]any{"title": "Ship version 1", "project": "api"}, &parent)

	var out breakDownOut
	call(t, cs, "break_down_task", map[string]any{
		"id":     parent.ID,
		"titles": []string{"Write the release notes", "Tag the release", "Update the docs"},
	}, &out)

	if len(out.Created) != 3 {
		t.Fatalf("created %d subtasks, want 3", len(out.Created))
	}
	if out.Subtasks != "0/3" {
		t.Errorf("progress = %q, want 0/3", out.Subtasks)
	}
	for _, child := range out.Created {
		if child.Parent != parent.ID {
			t.Errorf("subtask %d has parent %d", child.ID, child.Parent)
		}
		if child.Project != "api" {
			t.Errorf("subtask %d project = %q, want it inherited", child.ID, child.Project)
		}
	}

	children, err := s.Children(parent.ID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 3 {
		t.Errorf("store has %d children", len(children))
	}
}

func TestGetSubtasksReportsProgress(t *testing.T) {
	cs, _ := connect(t)

	var parent store.Task
	call(t, cs, "create_task", map[string]any{"title": "Parent"}, &parent)
	var out breakDownOut
	call(t, cs, "break_down_task", map[string]any{
		"id": parent.ID, "titles": []string{"One", "Two"},
	}, &out)

	call(t, cs, "complete_task", map[string]any{"id": out.Created[0].ID}, nil)

	var listed subtaskList
	call(t, cs, "get_subtasks", map[string]any{"id": parent.ID}, &listed)
	if listed.Count != 2 {
		t.Fatalf("got %d subtasks, want 2", listed.Count)
	}
	if listed.Progress != "1/2" {
		t.Errorf("progress = %q, want 1/2", listed.Progress)
	}
}

// A parent's progress should be visible in a plain listing, so an agent does
// not have to call get_subtasks on every row to find out.
func TestListTasksCarriesSubtaskProgress(t *testing.T) {
	cs, _ := connect(t)

	var parent store.Task
	call(t, cs, "create_task", map[string]any{"title": "Parent"}, &parent)
	call(t, cs, "break_down_task", map[string]any{
		"id": parent.ID, "titles": []string{"One", "Two"},
	}, nil)

	var listed taskList
	call(t, cs, "list_tasks", map[string]any{}, &listed)

	var found string
	for _, row := range listed.Tasks {
		if row.ID == parent.ID {
			found = row.Subtasks
		}
	}
	if found != "0/2" {
		t.Errorf("parent row subtasks = %q, want 0/2", found)
	}
}

// Tasks without children must not carry an empty progress field, or every row
// in a flat list grows a meaningless "0/0".
func TestChildlessTasksHaveNoSubtaskField(t *testing.T) {
	cs, _ := connect(t)
	call(t, cs, "create_task", map[string]any{"title": "Alone"}, nil)

	var listed taskList
	call(t, cs, "list_tasks", map[string]any{}, &listed)
	if len(listed.Tasks) != 1 {
		t.Fatalf("got %d tasks", len(listed.Tasks))
	}
	if listed.Tasks[0].Subtasks != "" {
		t.Errorf("subtasks = %q, want empty for a childless task", listed.Tasks[0].Subtasks)
	}
}

// The server tells agents how to work here, at handshake, rather than leaving
// them to infer a workflow from tool descriptions.
func TestServerShipsInstructions(t *testing.T) {
	cs, _ := connect(t)

	got := cs.InitializeResult().Instructions
	if got == "" {
		t.Fatal("no instructions sent at initialize")
	}
	for _, want := range []string{"claim_task", "ask_human", "subtask", "add_note"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions do not mention %q", want)
		}
	}
}
