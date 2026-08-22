package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// run executes the CLI the way a user would, through the real command tree,
// and captures what it wrote.
//
// Each call builds a fresh root command. That is only safe because flag values
// live on an app struct rather than in package-level variables — otherwise one
// test's --data-dir would leak into the next.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	root := NewRootCommand("test")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"--data-dir", dir}, args...))

	err := root.Execute()
	return buf.String(), err
}

// mustRun fails the test if the command errors.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("taskgo %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestAddThenList(t *testing.T) {
	dir := t.TempDir()

	out := mustRun(t, dir, "add", "Fix", "the", "login", "redirect")
	if !strings.Contains(out, "Added #1") {
		t.Errorf("add said %q", out)
	}

	out = mustRun(t, dir, "list")
	if !strings.Contains(out, "Fix the login redirect") {
		t.Errorf("list did not show the task:\n%s", out)
	}
}

// Bare words become the title, so quoting is optional.
func TestAddJoinsBareWords(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "several", "separate", "words")

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)

	if len(tasks) != 1 || tasks[0].Title != "several separate words" {
		t.Errorf("got %+v", tasks)
	}
}

func TestJSONOutputIsMachineReadable(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Task one", "--tag", "a", "--tag", "b", "--priority", "high")

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)

	if len(tasks) != 1 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if tasks[0].Priority != store.PriorityHigh {
		t.Errorf("priority = %q", tasks[0].Priority)
	}
	if len(tasks[0].Tags) != 2 {
		t.Errorf("tags = %v", tasks[0].Tags)
	}
}

// An empty result must still be a valid empty JSON array, not `null` — a
// caller doing `taskgo list --json | jq '.[]'` should not have to special-case
// the empty store.
func TestEmptyListIsAnEmptyJSONArray(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir, "list", "--json")

	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty list emitted %q, want []", strings.TrimSpace(out))
	}
}

func TestDoneResolvesByTitle(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Fix the login redirect")
	mustRun(t, dir, "add", "Write documentation")

	out := mustRun(t, dir, "done", "login")
	if !strings.Contains(out, "Done #1") {
		t.Errorf("done said %q", out)
	}

	// Completed tasks drop out of the default listing.
	var open []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &open)
	if len(open) != 1 || open[0].ID != 2 {
		t.Errorf("expected only the open task, got %+v", open)
	}

	var all []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--all", "--json"), &all)
	if len(all) != 2 {
		t.Errorf("--all should show both, got %d", len(all))
	}
}

func TestAmbiguousTitleIsAnErrorListingCandidates(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Fix login redirect")
	mustRun(t, dir, "add", "Fix login timeout")

	_, err := run(t, dir, "done", "login")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"matches 2 tasks", "Fix login redirect", "Fix login timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the candidates, missing %q:\n%v", want, err)
		}
	}
}

func TestUnknownTaskIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, dir, "done", "99"); err == nil {
		t.Error("expected an error for a missing task")
	}
	if _, err := run(t, dir, "show", "nothing at all"); err == nil {
		t.Error("expected an error for an unmatched title")
	}
}

func TestInvalidFlagValuesAreRejectedWithTheBadValue(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"add", "T", "--status", "nonsense"}, "nonsense"},
		{[]string{"add", "T", "--priority", "immediately"}, "immediately"},
		{[]string{"add", "T", "--due", "next thursday"}, "next thursday"},
	}

	for _, tc := range cases {
		_, err := run(t, dir, tc.args...)
		if err == nil {
			t.Errorf("%v: expected an error", tc.args)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error %q should name the bad value %q", tc.args, err, tc.want)
		}
	}
}

func TestDueAcceptsTodayAndTomorrow(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Due today", "--due", "today")
	mustRun(t, dir, "add", "Due tomorrow", "--due", "tomorrow")

	out := mustRun(t, dir, "list")
	if !strings.Contains(out, "today") || !strings.Contains(out, "tomorrow") {
		t.Errorf("relative dates not rendered:\n%s", out)
	}
}

func TestEditChangesFieldsAndClearDueRemovesTheDate(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Original", "--due", "2026-09-01")

	mustRun(t, dir, "edit", "1", "--title", "Renamed", "--priority", "urgent")

	var task store.Task
	decode(t, mustRun(t, dir, "show", "1", "--json"), &task)
	if task.Title != "Renamed" || task.Priority != store.PriorityUrgent {
		t.Errorf("edit did not apply: %+v", task)
	}
	if task.Due == nil {
		t.Error("an unrelated edit cleared the due date")
	}

	mustRun(t, dir, "edit", "1", "--clear-due")

	// A fresh variable: json.Unmarshal does not clear fields that are absent
	// from the payload, so decoding into the struct above would leave the old
	// due date in place and the assertion would test nothing.
	var cleared store.Task
	decode(t, mustRun(t, dir, "show", "1", "--json"), &cleared)
	if cleared.Due != nil {
		t.Errorf("--clear-due left due = %v", cleared.Due)
	}
}

func TestNoteAppends(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Has notes", "--notes", "First line.")
	mustRun(t, dir, "note", "1", "Second", "thought")

	var task store.Task
	decode(t, mustRun(t, dir, "show", "1", "--json"), &task)
	if !strings.Contains(task.Notes, "First line.") {
		t.Error("note overwrote the original text")
	}
	if !strings.Contains(task.Notes, "Second thought") {
		t.Error("note was not appended")
	}
}

// The workflow the plain-file design exists for: edit the Markdown by hand,
// reindex, and the change is picked up.
func TestHandEditThenReindex(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Before the edit")

	path := filepath.Join(dir, "tasks", "1.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task file: %v", err)
	}
	edited := strings.ReplaceAll(string(data), "title: Before the edit", "title: After the edit")
	edited = strings.ReplaceAll(edited, "priority: normal", "priority: urgent")
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write task file: %v", err)
	}

	mustRun(t, dir, "reindex")

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "After the edit" {
		t.Fatalf("reindex did not pick up the edit: %+v", tasks)
	}
	if tasks[0].Priority != store.PriorityUrgent {
		t.Errorf("priority = %q, want urgent", tasks[0].Priority)
	}
}

// Reindex must not destroy history — that is why the log is a separate file.
func TestReindexPreservesActivity(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "One")
	mustRun(t, dir, "add", "Two")
	mustRun(t, dir, "done", "1")

	before, err := os.ReadFile(filepath.Join(dir, "activity.jsonl"))
	if err != nil {
		t.Fatalf("read activity: %v", err)
	}

	mustRun(t, dir, "reindex")

	after, err := os.ReadFile(filepath.Join(dir, "activity.jsonl"))
	if err != nil {
		t.Fatalf("read activity after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("reindex modified the activity log")
	}
}

func TestActivityAttributesToHuman(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "From the CLI")

	var events []store.Event
	decode(t, mustRun(t, dir, "activity", "--json"), &events)

	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	if events[0].Actor != store.ActorHuman {
		t.Errorf("actor = %q, want human", events[0].Actor)
	}
}

func TestProjectsListWithCounts(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "projects", "new", "web", "-d", "The website")
	mustRun(t, dir, "add", "One", "--project", "web")
	mustRun(t, dir, "add", "Two", "--project", "web")
	mustRun(t, dir, "done", "1")

	var projects []store.ProjectSummary
	decode(t, mustRun(t, dir, "projects", "--json"), &projects)

	if len(projects) != 1 {
		t.Fatalf("got %d projects", len(projects))
	}
	if projects[0].Open != 1 || projects[0].Done != 1 {
		t.Errorf("counts wrong: open=%d done=%d", projects[0].Open, projects[0].Done)
	}
}

func TestInvalidProjectNameIsRejected(t *testing.T) {
	dir := t.TempDir()
	// A name with a slash would escape the projects directory.
	if _, err := run(t, dir, "projects", "new", "../escape"); err == nil {
		t.Error("expected a path-like project name to be rejected")
	}
}

func TestDeleteNeedsForceWhenNotInteractive(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Doomed")

	out := mustRun(t, dir, "delete", "1", "--force")
	if !strings.Contains(out, "Deleted #1") {
		t.Errorf("delete said %q", out)
	}

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--all", "--json"), &tasks)
	if len(tasks) != 0 {
		t.Errorf("task survived deletion: %+v", tasks)
	}
}

func TestFilters(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Web bug", "--project", "web", "--tag", "bug")
	mustRun(t, dir, "add", "Web chore", "--project", "web")
	mustRun(t, dir, "add", "Api bug", "--project", "api", "--tag", "bug")
	mustRun(t, dir, "add", "Late one", "--due", "2020-01-01")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"by project", []string{"list", "--project", "web", "--json"}, 2},
		{"by tag", []string{"list", "--tag", "bug", "--json"}, 2},
		{"overdue", []string{"list", "--overdue", "--json"}, 1},
		{"search", []string{"list", "--search", "chore", "--json"}, 1},
		{"by status", []string{"list", "--status", "todo", "--json"}, 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tasks []store.IndexEntry
			decode(t, mustRun(t, dir, tc.args...), &tasks)
			if len(tasks) != tc.want {
				t.Errorf("got %d, want %d: %+v", len(tasks), tc.want, tasks)
			}
		})
	}
}

func TestSubtasks(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Parent")
	mustRun(t, dir, "add", "Child", "--parent", "1")

	var kids []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--parent", "1", "--json"), &kids)
	if len(kids) != 1 || kids[0].Title != "Child" {
		t.Errorf("subtask filter returned %+v", kids)
	}

	// A parent that does not exist must be refused rather than producing an
	// orphan pointing at nothing.
	if _, err := run(t, dir, "add", "Orphan", "--parent", "99"); err == nil {
		t.Error("expected a missing parent to be rejected")
	}
}

func TestStatusSummary(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "One")
	mustRun(t, dir, "add", "Two", "--due", "2020-01-01")
	mustRun(t, dir, "done", "1")

	var summary struct {
		Total   int `json:"total"`
		Open    int `json:"open"`
		Overdue int `json:"overdue"`
	}
	decode(t, mustRun(t, dir, "status", "--json"), &summary)

	if summary.Total != 2 || summary.Open != 1 || summary.Overdue != 1 {
		t.Errorf("summary = %+v", summary)
	}
}

// A title containing a percent sign must not be treated as a format string.
func TestTitleWithFormatVerbIsPrintedLiterally(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Ship 50% of the feature")

	out := mustRun(t, dir, "list")
	if !strings.Contains(out, "Ship 50% of the feature") {
		t.Errorf("percent sign mangled:\n%s", out)
	}
	out = mustRun(t, dir, "activity")
	if !strings.Contains(out, "Ship 50% of the feature") {
		t.Errorf("percent sign mangled in activity:\n%s", out)
	}
}

func decode(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
}
