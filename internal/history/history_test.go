package history

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func TestNotEnabledUntilInit(t *testing.T) {
	s := newStore(t)

	if Enabled(s.Root()) {
		t.Error("history should be off until asked for")
	}
	if _, err := Log(s, 10); err != ErrNotEnabled {
		t.Errorf("Log err = %v, want ErrNotEnabled", err)
	}
	if _, err := Undo(s); err != ErrNotEnabled {
		t.Errorf("Undo err = %v, want ErrNotEnabled", err)
	}
}

func TestInitAndAutomaticCommits(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	if _, err := s.Create(store.ActorAgent, store.NewTask{Title: "Written by an agent"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := Log(s, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("want the init commit plus the create, got %+v", entries)
	}
	// The subject should read like the activity log, actor included.
	if !strings.Contains(entries[0].Message, "agent") || !strings.Contains(entries[0].Message, "Written by an agent") {
		t.Errorf("commit subject = %q; it should say who did what", entries[0].Message)
	}
}

// Runtime state is not history. Committing claims.json would produce a commit
// every time an agent so much as looked at a task.
func TestRuntimeStateIsNotCommitted(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	for _, name := range []string{"claims.json", "sessions.json", "notified.json", ".lock"} {
		if err := os.WriteFile(filepath.Join(s.Root(), name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "Trigger a commit"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tracked := gitOutput(t, s.Root(), "ls-files")
	for _, name := range []string{"claims.json", "sessions.json", "notified.json", ".lock"} {
		if strings.Contains(tracked, name) {
			t.Errorf("%s is tracked; runtime state must not be committed:\n%s", name, tracked)
		}
	}
	if !strings.Contains(tracked, "tasks/1.md") {
		t.Errorf("the task file is not tracked:\n%s", tracked)
	}
}

func TestUndoRevertsTheLastChangeAndRecordsIt(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	task, err := s.Create(store.ActorHuman, store.NewTask{Title: "Keep me"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.AddNote(store.ActorAgent, task.ID, "an agent wrote this by mistake"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}

	undone, err := Undo(s)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !strings.Contains(undone.Message, "note") {
		t.Errorf("undid %q, expected the note", undone.Message)
	}

	after, err := s.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if strings.Contains(after.Notes, "by mistake") {
		t.Error("the note survived the undo")
	}

	// The reversal is recorded rather than rewriting history: "this was
	// undone" is itself a fact worth keeping.
	entries, _ := Log(s, 5)
	if len(entries) < 3 {
		t.Fatalf("want the undo recorded as a new commit, got %+v", entries)
	}
	if !strings.Contains(entries[0].Message, "undo") {
		t.Errorf("newest commit = %q, want it to record the undo", entries[0].Message)
	}
}

// After an undo the Markdown is authoritative again, so the index must reflect
// what was restored rather than what was reverted away.
func TestUndoReindexes(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	if _, err := s.Create(store.ActorHuman, store.NewTask{Title: "First"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(store.ActorAgent, store.NewTask{Title: "Second, created in error"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Undo(s); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	listed, err := s.List(store.Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Title != "First" {
		t.Errorf("index not rebuilt after undo: %+v", listed)
	}
}

// A hand edit sitting uncommitted would be swept into the revert. Refusing is
// better than quietly discarding someone's work.
func TestUndoRefusesWithUncommittedChanges(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	task, err := s.Create(store.ActorHuman, store.NewTask{Title: "Committed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	path := filepath.Join(s.Root(), "tasks", "1.md")
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), "title: Committed", "title: Edited by hand", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("hand edit: %v", err)
	}

	if _, err := Undo(s); err == nil {
		t.Fatal("Undo should refuse while a hand edit is uncommitted")
	}

	// The edit must still be there.
	after, _ := s.Get(task.ID)
	if after.Title != "Edited by hand" {
		t.Errorf("the hand edit was discarded: %q", after.Title)
	}

	// Saving it first makes undo possible again.
	if err := Commit(s, "human: hand edit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := Undo(s); err != nil {
		t.Errorf("Undo after save: %v", err)
	}
}

// A commit failure must never surface as a failed task operation: the task
// file is already on disk by then.
func TestCommitFailureDoesNotFailTheWrite(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Attach(s)

	// Break the repository underneath the hook.
	if err := os.RemoveAll(filepath.Join(s.Root(), ".git")); err != nil {
		t.Fatalf("remove .git: %v", err)
	}

	task, err := s.Create(store.ActorHuman, store.NewTask{Title: "Still written"})
	if err != nil {
		t.Fatalf("a broken repository must not fail the write: %v", err)
	}
	if _, err := s.Get(task.ID); err != nil {
		t.Errorf("the task was not written: %v", err)
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	s := newStore(t)
	if err := Init(s); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Only the init commit exists; reverting it is legitimate but there is
	// nothing before it, so this just must not panic or corrupt anything.
	if _, err := Undo(s); err != nil {
		t.Logf("undo of the init commit: %v", err)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
