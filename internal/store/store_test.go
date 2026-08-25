package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// A body containing a `---` line is the case a naive frontmatter split gets
// wrong: it truncates the notes at the horizontal rule. Round-tripping it is
// the point of this test.
func TestTaskMarkdownRoundTrip(t *testing.T) {
	created := time.Date(2026, 8, 22, 10, 14, 0, 0, time.UTC)

	cases := []struct {
		name string
		task Task
	}{
		{
			name: "full",
			task: Task{
				ID: 47, Title: "Fix login redirect",
				Status: StatusDoing, Priority: PriorityHigh,
				Due:     ptrDue("2026-08-25"),
				Project: "taskgo", Tags: []string{"auth", "bug"}, Parent: 12,
				Created: created, Updated: created,
				Notes: "Some notes.",
			},
		},
		{
			name: "minimal - optional fields must not appear as empty keys",
			task: Task{
				ID: 1, Title: "Bare", Status: StatusTodo, Priority: PriorityNormal,
				Created: created, Updated: created,
			},
		},
		{
			name: "body containing a fence",
			task: Task{
				ID: 2, Title: "Has a rule", Status: StatusTodo, Priority: PriorityNormal,
				Created: created, Updated: created,
				Notes: "Above the rule.\n\n---\n\nBelow the rule.\n\n---",
			},
		},
		{
			name: "body containing yaml-looking lines",
			task: Task{
				ID: 3, Title: "Config snippet", Status: StatusTodo, Priority: PriorityNormal,
				Created: created, Updated: created,
				Notes: "```yaml\nid: 999\ntitle: not the real title\n```",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.task.MarshalMarkdown()
			if err != nil {
				t.Fatalf("MarshalMarkdown: %v", err)
			}

			var got Task
			if err := got.UnmarshalMarkdown(data); err != nil {
				t.Fatalf("UnmarshalMarkdown: %v\n--- file ---\n%s", err, data)
			}

			if !reflect.DeepEqual(tc.task, got) {
				t.Errorf("round trip changed the task\n want: %+v\n  got: %+v\n--- file ---\n%s",
					tc.task, got, data)
			}
		})
	}
}

func TestMinimalTaskOmitsUnsetFields(t *testing.T) {
	task := Task{ID: 1, Title: "Bare", Status: StatusTodo, Priority: PriorityNormal}
	data, err := task.MarshalMarkdown()
	if err != nil {
		t.Fatalf("MarshalMarkdown: %v", err)
	}

	for _, unwanted := range []string{"due:", "project:", "tags:", "parent:"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("unset field %q leaked into the file:\n%s", unwanted, data)
		}
	}
}

func TestCreateAllocatesSequentialIDs(t *testing.T) {
	s := newTestStore(t)

	for want := 1; want <= 3; want++ {
		task, err := s.Create(ActorHuman, NewTask{Title: "Task"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if task.ID != want {
			t.Fatalf("id = %d, want %d", task.ID, want)
		}
	}
}

// Reindex must rebuild state.json faithfully from Markdown while leaving the
// activity log byte-identical — the log is the one thing that cannot be
// derived from current state.
func TestReindexRebuildsIndexAndPreservesActivity(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		if _, err := s.Create(ActorHuman, NewTask{Title: "Task", Project: "demo"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := s.Complete(ActorAgent, 2); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	before, err := s.readIndex()
	if err != nil {
		t.Fatalf("readIndex: %v", err)
	}
	activityBefore, err := os.ReadFile(s.activityPath())
	if err != nil {
		t.Fatalf("read activity: %v", err)
	}

	// Corrupt the index the way a crashed write or a bad merge would.
	if err := os.WriteFile(s.statePath(), []byte(`{"version":1,"nextId":1,"tasks":[]}`), 0o644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	rebuilt, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	if !reflect.DeepEqual(before.Tasks, rebuilt.Tasks) {
		t.Errorf("reindex did not restore the task index\n want: %+v\n  got: %+v", before.Tasks, rebuilt.Tasks)
	}
	if rebuilt.NextID != before.NextID {
		t.Errorf("nextId = %d, want %d", rebuilt.NextID, before.NextID)
	}

	activityAfter, err := os.ReadFile(s.activityPath())
	if err != nil {
		t.Fatalf("read activity after: %v", err)
	}
	if string(activityBefore) != string(activityAfter) {
		t.Error("reindex modified activity.jsonl; the log must never be rebuilt")
	}
}

// Reindex is also the repair path for a state.json that will not parse at all.
func TestReindexRecoversFromUnparseableIndex(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Survivor"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(s.statePath(), []byte("this is not json"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	if _, err := s.List(Filter{}); err == nil {
		t.Error("expected List to fail on an unparseable index")
	}
	if _, err := s.Reindex(); err != nil {
		t.Fatalf("Reindex should repair an unparseable index: %v", err)
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List after reindex: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Survivor" {
		t.Errorf("expected the task back, got %+v", got)
	}
}

// Concurrent creators must not collide on an id or interleave a write. This is
// what the coarse write lock exists for.
func TestConcurrentCreatesGetDistinctIDs(t *testing.T) {
	s := newTestStore(t)

	const n = 12
	var wg sync.WaitGroup
	ids := make([]int, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task, err := s.Create(ActorAgent, NewTask{Title: "Concurrent"})
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = task.ID
		}(i)
	}
	wg.Wait()

	seen := map[int]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if seen[ids[i]] {
			t.Fatalf("duplicate id %d", ids[i])
		}
		seen[ids[i]] = true
	}

	files, err := os.ReadDir(s.tasksDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(files) != n {
		t.Errorf("wrote %d task files, want %d", len(files), n)
	}

	// Every file must parse: a torn write would show up here.
	for _, f := range files {
		if _, err := s.readTaskFile(filepath.Join(s.tasksDir(), f.Name())); err != nil {
			t.Errorf("task file %s is not well formed: %v", f.Name(), err)
		}
	}
}

func TestUpdateDistinguishesClearFromLeaveAlone(t *testing.T) {
	s := newTestStore(t)
	due := DueDate("2026-08-25")
	task, err := s.Create(ActorHuman, NewTask{Title: "Dated", Due: &due})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Updating something else must leave the due date alone.
	title := "Renamed"
	got, err := s.Update(ActorHuman, task.ID, Update{Title: &title})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Due == nil {
		t.Fatal("due date was cleared by an unrelated update")
	}

	// Explicitly clearing must work.
	var cleared *DueDate
	got, err = s.Update(ActorHuman, task.ID, Update{Due: &cleared})
	if err != nil {
		t.Fatalf("Update clearing due: %v", err)
	}
	if got.Due != nil {
		t.Errorf("due = %v, want nil", got.Due)
	}
}

func TestAddNoteAppendsRatherThanReplaces(t *testing.T) {
	s := newTestStore(t)
	task, err := s.Create(ActorHuman, NewTask{Title: "Notes", Notes: "Written by a human."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.AddNote(ActorAgent, task.ID, "Added by an agent.")
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}

	if !strings.Contains(got.Notes, "Written by a human.") {
		t.Error("agent note destroyed the human's text")
	}
	if !strings.Contains(got.Notes, "Added by an agent.") {
		t.Error("agent note was not recorded")
	}
}

func TestActivityRecordsActorAndOrdersNewestFirst(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "One"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorAgent, NewTask{Title: "Two"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	events, err := s.Activity(0)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Detail != "Two" {
		t.Errorf("newest event = %q, want the most recent", events[0].Detail)
	}
	if events[0].Actor != ActorAgent || events[1].Actor != ActorHuman {
		t.Errorf("actors not recorded correctly: %+v", events)
	}
}

func TestResolveByIDAndTitle(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Fix login redirect"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Write docs"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if id, err := s.Resolve("1"); err != nil || id != 1 {
		t.Errorf("Resolve(\"1\") = %d, %v", id, err)
	}
	if id, err := s.Resolve("login"); err != nil || id != 1 {
		t.Errorf("Resolve(\"login\") = %d, %v", id, err)
	}
	if id, err := s.Resolve("LOGIN"); err != nil || id != 1 {
		t.Errorf("Resolve should be case-insensitive: %d, %v", id, err)
	}
	if _, err := s.Resolve("nothing here"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.Resolve("99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound for a missing id, got %v", err)
	}
}

func TestResolveAmbiguousTitleErrorsWithCandidates(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Fix login redirect"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Fix login timeout"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := s.Resolve("login")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"matches 2 tasks", "1", "2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list candidates, missing %q: %v", want, err)
		}
	}
}

// A completed task should not shadow the open one you meant.
func TestResolvePrefersOpenTasks(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Deploy the thing"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Deploy the thing again"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Complete(ActorHuman, 1); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	id, err := s.Resolve("Deploy the thing")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != 2 {
		t.Errorf("resolved to %d, want the open task 2", id)
	}
}

func TestListHidesDoneByDefault(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Open"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Closed"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Complete(ActorHuman, 2); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	open, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 1 || open[0].ID != 1 {
		t.Errorf("default list should hide done tasks, got %+v", open)
	}

	all, err := s.List(Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("IncludeDone should show both, got %+v", all)
	}
}

func TestOverdueIgnoresCompletedTasks(t *testing.T) {
	s := newTestStore(t)
	past := DueDate("2020-01-01")

	if _, err := s.Create(ActorHuman, NewTask{Title: "Late", Due: &past}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Late but done", Due: &past}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Complete(ActorHuman, 2); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	overdue, err := s.Overdue(time.Now())
	if err != nil {
		t.Fatalf("Overdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].ID != 1 {
		t.Errorf("a finished task is never overdue, got %+v", overdue)
	}
}

// The filename is authoritative for identity, so a mismatched frontmatter id
// must not win — otherwise copying a file would silently shadow another task.
func TestScanTrustsFilenameOverFrontmatterID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Original"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := os.ReadFile(s.taskPath(1))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.tasksDir(), "9.md"), data, 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	idx, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if len(idx.Tasks) != 2 {
		t.Fatalf("want 2 tasks after copying a file, got %d", len(idx.Tasks))
	}
	if idx.NextID != 10 {
		t.Errorf("nextId = %d, want 10 so the copy's id is not reused", idx.NextID)
	}
}

func TestNonTaskFilesInTasksDirAreLeftAlone(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Real"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stray := filepath.Join(s.tasksDir(), "scratch-notes.md")
	if err := os.WriteFile(stray, []byte("just my notes, not a task"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	idx, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex should ignore non-task files: %v", err)
	}
	if len(idx.Tasks) != 1 {
		t.Errorf("stray file was indexed: %+v", idx.Tasks)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Error("reindex deleted a file it did not own")
	}
}

func ptrDue(s string) *DueDate {
	d := DueDate(s)
	return &d
}

func TestAskAndAnswerRoundTrip(t *testing.T) {
	s := newTestStore(t)
	task, err := s.Create(ActorHuman, NewTask{Title: "Decide the auth model"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	asked, err := s.Ask(ActorAgent, task.ID, "claude-code", "JWTs or session cookies?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !asked.AwaitingAnswer() {
		t.Fatal("task should be waiting after Ask")
	}
	if asked.AskedBy != "claude-code" {
		t.Errorf("AskedBy = %q", asked.AskedBy)
	}

	answered, err := s.Answer(ActorHuman, task.ID, "Session cookies.")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if answered.AwaitingAnswer() {
		t.Error("task should no longer be waiting")
	}
	if answered.Answer != "Session cookies." {
		t.Errorf("Answer = %q", answered.Answer)
	}

	// The whole exchange belongs in the Markdown, not just the outcome.
	for _, want := range []string{"JWTs or session cookies?", "Session cookies.", "**Question**", "**Answer**"} {
		if !strings.Contains(answered.Notes, want) {
			t.Errorf("notes missing %q:\n%s", want, answered.Notes)
		}
	}
}

func TestAnsweringSomethingNobodyAskedFails(t *testing.T) {
	s := newTestStore(t)
	task, err := s.Create(ActorHuman, NewTask{Title: "Nothing pending"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Answer(ActorHuman, task.ID, "an answer"); err == nil {
		t.Error("expected an error answering a task with no question")
	}
}

// A new question must not leave the previous answer lying around, or an agent
// polling could read a stale reply as the response to what it just asked.
func TestNewQuestionClearsThePreviousAnswer(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.Create(ActorHuman, NewTask{Title: "Twice asked"})

	if _, err := s.Ask(ActorAgent, task.ID, "agent", "first?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if _, err := s.Answer(ActorHuman, task.ID, "first answer"); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	asked, err := s.Ask(ActorAgent, task.ID, "agent", "second?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if asked.Answer != "" {
		t.Errorf("stale answer survived a new question: %q", asked.Answer)
	}
}

// A task waiting on a human sorts above everything, because it is the only
// state where work has actually stopped.
func TestWaitingTasksSortFirst(t *testing.T) {
	s := newTestStore(t)
	due := DueDate("2020-01-01")

	if _, err := s.Create(ActorHuman, NewTask{Title: "Overdue and urgent", Due: &due, Priority: PriorityUrgent}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stuck, _ := s.Create(ActorHuman, NewTask{Title: "Merely waiting", Priority: PriorityLow})
	if _, err := s.Ask(ActorAgent, stuck.ID, "agent", "which one?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	list, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 || list[0].ID != stuck.ID {
		t.Errorf("waiting task should sort first, got %+v", list)
	}

	waiting, err := s.Waiting()
	if err != nil {
		t.Fatalf("Waiting: %v", err)
	}
	if len(waiting) != 1 || waiting[0].ID != stuck.ID {
		t.Errorf("Waiting() = %+v", waiting)
	}
}

// The question round-trips through the Markdown, so a hand edit or a reindex
// does not lose what an agent is stuck on.
func TestQuestionSurvivesReindex(t *testing.T) {
	s := newTestStore(t)
	task, _ := s.Create(ActorHuman, NewTask{Title: "Ask me"})
	if _, err := s.Ask(ActorAgent, task.ID, "claude-code", "well?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if _, err := s.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	waiting, err := s.Waiting()
	if err != nil {
		t.Fatalf("Waiting: %v", err)
	}
	if len(waiting) != 1 || waiting[0].Question != "well?" || waiting[0].AskedBy != "claude-code" {
		t.Errorf("question did not survive reindex: %+v", waiting)
	}
}

// Guarding only self-parenting let 1→2 then 2→1 through, producing a loop that
// any tree walk would hang on.
func TestParentCyclesAreRejected(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ActorHuman, NewTask{Title: "Task"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	two := 2
	if _, err := s.Update(ActorHuman, 1, Update{Parent: &two}); err != nil {
		t.Fatalf("1 under 2: %v", err)
	}

	// Direct loop.
	one := 1
	if _, err := s.Update(ActorHuman, 2, Update{Parent: &one}); err == nil {
		t.Error("a direct parent loop was accepted")
	}

	// Indirect: 3 under 1, then 2 under 3 would close 2→3→1→2.
	if _, err := s.Update(ActorHuman, 3, Update{Parent: &one}); err != nil {
		t.Fatalf("3 under 1: %v", err)
	}
	three := 3
	if _, err := s.Update(ActorHuman, 2, Update{Parent: &three}); err == nil {
		t.Error("an indirect parent loop was accepted")
	}

	// Self-parenting stays rejected.
	if _, err := s.Update(ActorHuman, 1, Update{Parent: &one}); err == nil {
		t.Error("self-parenting was accepted")
	}
}

// A cycle already on disk, from a hand edit, must not hang the check meant to
// catch it.
func TestCycleOnDiskDoesNotHangTheGuard(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 2; i++ {
		if _, err := s.Create(ActorHuman, NewTask{Title: "Task"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Forge the loop by hand, the way an editor would.
	for _, pair := range [][2]int{{1, 2}, {2, 1}} {
		path := filepath.Join(s.tasksDir(), strconv.Itoa(pair[0])+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		edited := strings.Replace(string(data), "created:", "parent: "+strconv.Itoa(pair[1])+"\ncreated:", 1)
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if _, err := s.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		third, _ := s.Create(ActorHuman, NewTask{Title: "Third"})
		one := 1
		_, _ = s.Update(ActorHuman, third.ID, Update{Parent: &one})
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the cycle guard hung on a loop that was already on disk")
	}
}

func TestChildrenAndProgress(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Create(ActorHuman, NewTask{Title: "Parent"})
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ActorHuman, NewTask{Title: "Child", Parent: parent.ID}); err != nil {
			t.Fatalf("Create child: %v", err)
		}
	}
	if _, err := s.Complete(ActorHuman, 2); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	kids, err := s.Children(parent.ID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(kids) != 3 {
		t.Errorf("got %d children, want 3", len(kids))
	}

	progress, err := s.ProgressFor()
	if err != nil {
		t.Fatalf("ProgressFor: %v", err)
	}
	if got := progress[parent.ID]; got.Done != 1 || got.Total != 3 {
		t.Errorf("progress = %+v, want 1/3", got)
	}
}

// Grandchildren must not count toward a parent's progress: "2 of 3" should not
// change because someone subdivided one of the three.
func TestProgressCountsDirectChildrenOnly(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Create(ActorHuman, NewTask{Title: "Parent"})
	child, _ := s.Create(ActorHuman, NewTask{Title: "Child", Parent: parent.ID})
	if _, err := s.Create(ActorHuman, NewTask{Title: "Grandchild", Parent: child.ID}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	progress, _ := s.ProgressFor()
	if got := progress[parent.ID]; got.Total != 1 {
		t.Errorf("parent progress = %+v, want a total of 1", got)
	}
}

func TestTreeNestsChildrenUnderParents(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Create(ActorHuman, NewTask{Title: "Parent"})
	if _, err := s.Create(ActorHuman, NewTask{Title: "Child A", Parent: parent.ID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Child B", Parent: parent.ID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Unrelated"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	nodes, err := s.Tree(Filter{})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("got %d nodes, want 4", len(nodes))
	}

	var depths []int
	for _, n := range nodes {
		depths = append(depths, n.Depth)
	}
	// Parent, its two children, then the unrelated task.
	want := []int{0, 1, 1, 0}
	for i := range want {
		if depths[i] != want[i] {
			t.Fatalf("depths = %v, want %v (%+v)", depths, want, nodes)
		}
	}
	if !nodes[2].Last {
		t.Error("the second child should be marked as last")
	}
}

// Filtering must not hide a matching subtask just because its parent did not
// match — an overdue subtask is still overdue.
func TestTreePromotesOrphanedMatches(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Create(ActorHuman, NewTask{Title: "Parent, not overdue"})
	past := DueDate("2020-01-01")
	if _, err := s.Create(ActorHuman, NewTask{Title: "Overdue child", Parent: parent.ID, Due: &past}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	today := DueOnDay(time.Now())
	nodes, err := s.Tree(Filter{DueBefore: &today})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want just the overdue child: %+v", len(nodes), nodes)
	}
	if nodes[0].Depth != 0 {
		t.Errorf("the orphaned match should be promoted to depth 0, got %d", nodes[0].Depth)
	}
}

// The id counter is a high-water mark, not a derived value. Deleting the
// newest tasks and rebuilding must not wind it back onto ids the activity log
// has already attributed to different tasks.
func TestReindexNeverRewindsTheIDCounter(t *testing.T) {
	s := newTestStore(t)

	for _, title := range []string{"One", "Two", "Three"} {
		if _, err := s.Create(ActorHuman, NewTask{Title: title}); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}
	if err := s.Delete(ActorHuman, 3); err != nil {
		t.Fatalf("Delete 3: %v", err)
	}
	if err := s.Delete(ActorHuman, 2); err != nil {
		t.Fatalf("Delete 2: %v", err)
	}

	idx, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if idx.NextID != 4 {
		t.Fatalf("nextId = %d after reindex, want 4; ids 2 and 3 have been issued", idx.NextID)
	}

	created, err := s.Create(ActorHuman, NewTask{Title: "A brand new task"})
	if err != nil {
		t.Fatalf("Create after reindex: %v", err)
	}
	if created.ID != 4 {
		t.Errorf("new task got id %d, reusing an id the activity log already spent", created.ID)
	}
}

// Losing state.json entirely is the case the previous index cannot cover, so
// the counter has to come from the log, which is never rebuilt.
func TestReindexRecoversTheIDCounterFromTheActivityLog(t *testing.T) {
	s := newTestStore(t)

	for _, title := range []string{"One", "Two", "Three"} {
		if _, err := s.Create(ActorHuman, NewTask{Title: title}); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}
	if err := s.Delete(ActorHuman, 3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := os.Remove(s.statePath()); err != nil {
		t.Fatalf("remove state.json: %v", err)
	}

	idx, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if idx.NextID != 4 {
		t.Errorf("nextId = %d, want 4 from the activity log", idx.NextID)
	}
}

// An index written by a future taskgo may mean something different by the same
// fields, so reading it is refused rather than guessed at.
func TestIndexFromANewerVersionIsRefused(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Existing"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	future := fmt.Sprintf(`{"version":%d,"nextId":9,"tasks":[]}`, indexVersion+1)
	if err := os.WriteFile(s.statePath(), []byte(future), 0o644); err != nil {
		t.Fatalf("write future index: %v", err)
	}

	if _, err := s.List(Filter{}); err == nil {
		t.Error("expected List to refuse an index from a newer taskgo")
	} else if !strings.Contains(err.Error(), "newer taskgo") {
		t.Errorf("error should name the reason, got: %v", err)
	}

	// Reindex is the documented way out, and must still work.
	idx, err := s.Reindex()
	if err != nil {
		t.Fatalf("Reindex should rebuild over a future index: %v", err)
	}
	if idx.NextID != 9 {
		t.Errorf("nextId = %d, want 9 kept from the index it replaced", idx.NextID)
	}
}

// A deleted parent must not leave its children pointing at an id that no
// longer resolves.
func TestDeletingAParentPromotesItsChildren(t *testing.T) {
	s := newTestStore(t)

	grandparent, err := s.Create(ActorHuman, NewTask{Title: "Grandparent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	parent, err := s.Create(ActorHuman, NewTask{Title: "Parent", Parent: grandparent.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	child, err := s.Create(ActorHuman, NewTask{Title: "Child", Parent: parent.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Delete(ActorHuman, parent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// On disk, not just in the index — the Markdown is what a human reads.
	reread, err := s.Get(child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if reread.Parent != grandparent.ID {
		t.Errorf("child parent = %d, want %d", reread.Parent, grandparent.ID)
	}

	kids, err := s.Children(grandparent.ID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID {
		t.Errorf("grandparent should have adopted the child, got %+v", kids)
	}
}

// With no grandparent to inherit, a promoted child becomes top-level.
func TestDeletingATopLevelParentDetachesItsChildren(t *testing.T) {
	s := newTestStore(t)

	parent, err := s.Create(ActorHuman, NewTask{Title: "Parent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	child, err := s.Create(ActorHuman, NewTask{Title: "Child", Parent: parent.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Delete(ActorHuman, parent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	reread, err := s.Get(child.ID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if reread.Parent != 0 {
		t.Errorf("child parent = %d, want 0", reread.Parent)
	}
}
