package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/history"
	"github.com/JustSteveKing/taskgo/internal/store"
)

func needsGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// History is opt-in, so every command that needs it has to say so plainly
// rather than failing in Git's words.
func TestHistoryCommandsRefuseUntilEnabled(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "add", "A task")

	// Every one of them, including the read-only log — the feature is either
	// on or it is not, and a log that silently showed nothing would look like
	// history was enabled and empty.
	for _, args := range [][]string{
		{"undo"},
		{"history", "save"},
		{"history", "log"},
		{"history"},
	} {
		_, err := run(t, dir, args...)
		if err == nil {
			t.Errorf("taskgo %s should fail before history init", strings.Join(args, " "))
			continue
		}
		if !strings.Contains(err.Error(), "history init") {
			t.Errorf("taskgo %s: error should name the fix, got: %v", strings.Join(args, " "), err)
		}
	}
}

func TestHistoryInitCommitsWhatIsAlreadyThere(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "add", "Existing work")

	out := mustRun(t, dir, "history", "init")
	if !strings.Contains(out, "History enabled") {
		t.Errorf("got %q", out)
	}
	if !history.Enabled(dir) {
		t.Fatal("the data directory is not a Git repository")
	}

	// Enabling twice is an error rather than a silent no-op.
	if _, err := run(t, dir, "history", "init"); err == nil {
		t.Error("history init twice should fail")
	}

	out = mustRun(t, dir, "history", "log")
	if !strings.Contains(out, "history enabled") {
		t.Errorf("the first commit is missing from the log:\n%s", out)
	}
}

// Runtime state is not history. Committing claims.json would produce a commit
// every time an agent so much as looked at a task.
func TestHistoryIgnoresRuntimeState(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "add", "A task")
	mustRun(t, dir, "history", "init")

	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, name := range []string{".lock", "claims.json", "sessions.json", "notified.json"} {
		if !strings.Contains(string(ignore), name) {
			t.Errorf(".gitignore does not cover %s:\n%s", name, ignore)
		}
	}
}

// Each change through the store becomes a commit whose subject names the actor
// — which is what makes `git log` read like the activity log.
func TestEveryChangeIsCommittedWithItsActor(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")

	mustRun(t, dir, "add", "Made by a human")

	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	history.Attach(s)
	if _, err := s.Create(store.ActorAgent, store.NewTask{Title: "Made by an agent"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	out := mustRun(t, dir, "history", "log")
	if !strings.Contains(out, "human: create") {
		t.Errorf("no human-attributed commit:\n%s", out)
	}
	if !strings.Contains(out, "agent: create") {
		t.Errorf("no agent-attributed commit:\n%s", out)
	}
	if !strings.Contains(out, "Made by an agent") {
		t.Errorf("the commit subject does not carry the title:\n%s", out)
	}
}

func TestHistoryLogLimit(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	for i := 0; i < 5; i++ {
		mustRun(t, dir, "add", "Task")
	}

	out := mustRun(t, dir, "history", "log", "-n", "2")
	if got := strings.Count(strings.TrimSpace(out), "\n") + 1; got != 2 {
		t.Errorf("-n 2 printed %d lines:\n%s", got, out)
	}
}

func TestHistoryLogJSON(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	mustRun(t, dir, "add", "Findable")

	var entries []history.Entry
	decode(t, mustRun(t, dir, "history", "log", "--json"), &entries)
	if len(entries) < 2 {
		t.Fatalf("want at least the init and the add, got %+v", entries)
	}
	if entries[0].Short == "" || entries[0].When.IsZero() {
		t.Errorf("entry is missing its identity: %+v", entries[0])
	}
}

func TestUndoRevertsTheLastChange(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	mustRun(t, dir, "add", "Keep me")
	mustRun(t, dir, "add", "Undo me")

	out := mustRun(t, dir, "undo")
	if !strings.Contains(out, "Undid") {
		t.Errorf("got %q", out)
	}

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "Keep me" {
		t.Errorf("undo did not remove the second task: %+v", tasks)
	}
}

// The reversal is a new commit rather than a rewrite: an agent's mistake
// together with its correction tells you more than the mistake never having
// appeared.
func TestUndoRecordsTheReversalRatherThanRewriting(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	mustRun(t, dir, "add", "Undo me")

	before := strings.Count(mustRun(t, dir, "history", "log"), "\n")
	mustRun(t, dir, "undo")
	after := mustRun(t, dir, "history", "log")

	if strings.Count(after, "\n") <= before {
		t.Errorf("undo shortened the history rather than adding to it:\n%s", after)
	}
	if !strings.Contains(after, "Undo me") {
		t.Errorf("the undone change was erased from the log:\n%s", after)
	}
}

// A hand edit never went through the store, so it never fired the change hook.
// Undo refuses to sweep it into the revert; save is how it gets recorded.
func TestUndoRefusesWhileAHandEditIsUncommitted(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	mustRun(t, dir, "add", "Edited by hand")

	path := filepath.Join(dir, "tasks", "1.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(path, append(data, []byte("\nA line typed in an editor.\n")...), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := run(t, dir, "undo"); err == nil {
		t.Fatal("undo should refuse while a hand edit is uncommitted")
	}

	out := mustRun(t, dir, "history", "save", "-m", "hand edit")
	if !strings.Contains(out, "Saved") {
		t.Errorf("got %q", out)
	}

	// Now it has something to revert to, and the edit is safe in a commit.
	if _, err := run(t, dir, "undo"); err != nil {
		t.Errorf("undo after save: %v", err)
	}
	if !strings.Contains(mustRun(t, dir, "history", "log"), "hand edit") {
		t.Error("the saved hand edit is not in the log")
	}
}

func TestHistorySaveWithNothingToSave(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")

	// A no-op commit is not an error: nothing to record is a normal outcome.
	if _, err := run(t, dir, "history", "save"); err != nil {
		t.Errorf("save with a clean tree: %v", err)
	}
}

// The bare `taskgo history` is the log, because that is what people want when
// they type it.
func TestBareHistoryShowsTheLog(t *testing.T) {
	needsGit(t)
	dir := t.TempDir()
	mustRun(t, dir, "history", "init")
	mustRun(t, dir, "add", "Findable")

	if !strings.Contains(mustRun(t, dir, "history"), "Findable") {
		t.Error("bare history did not show the log")
	}
}
