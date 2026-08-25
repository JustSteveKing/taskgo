package agents

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestRegisterThenList(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	Register(s, "abc123", "claude", now)

	got, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	if got[0].Name != "claude" || got[0].ID != "abc123" {
		t.Errorf("got %+v", got[0])
	}
	if got[0].PID != os.Getpid() {
		t.Errorf("pid = %d, want this process %d", got[0].PID, os.Getpid())
	}
}

// Registering the same id again is a heartbeat, not a second agent: it moves
// LastSeen without disturbing when the agent first connected.
func TestRegisterAgainRefreshesRatherThanDuplicates(t *testing.T) {
	s := newTestStore(t)
	connected := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	later := connected.Add(20 * time.Minute)

	Register(s, "abc123", "claude", connected)
	Register(s, "abc123", "claude", later)

	got, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session after re-registering, got %d", len(got))
	}
	if !got[0].Connected.Equal(connected) {
		t.Errorf("connected = %v, want the original %v", got[0].Connected, connected)
	}
	if !got[0].LastSeen.Equal(later) {
		t.Errorf("lastSeen = %v, want %v", got[0].LastSeen, later)
	}
	if idle := got[0].Idle(later); idle != 0 {
		t.Errorf("idle = %v just after a heartbeat, want 0", idle)
	}
}

func TestUnregisterRemovesTheSession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	Register(s, "abc123", "claude", now)
	Register(s, "def456", "another", now)
	Unregister(s, "abc123")

	got, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "def456" {
		t.Errorf("got %+v", got)
	}
}

// A server killed outright never gets to unregister. The pid is what makes
// that recoverable, so an entry belonging to a dead process must not be listed
// — this is the case a timeout would get wrong.
func TestSessionsOfDeadProcessesAreNotListed(t *testing.T) {
	s := newTestStore(t)

	// A real pid that is definitely gone: run something trivial and wait for
	// it, rather than inventing a number that might belong to anything.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	dead := cmd.Process.Pid

	writeSessions(t, s.Root(), map[string]Session{
		"ghost": {ID: "ghost", Name: "killed-agent", PID: dead,
			Connected: time.Now(), LastSeen: time.Now()},
	})

	got, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a session whose process is gone was listed: %+v", got)
	}
}

// The same sweep runs on write, so ghosts do not accumulate in the file.
func TestWritingSweepsDeadSessions(t *testing.T) {
	s := newTestStore(t)

	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}

	writeSessions(t, s.Root(), map[string]Session{
		"ghost": {ID: "ghost", Name: "killed-agent", PID: cmd.Process.Pid,
			Connected: time.Now(), LastSeen: time.Now()},
	})

	Register(s, "live", "claude", time.Now())

	f, err := read(s.Root())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, ok := f.Sessions["ghost"]; ok {
		t.Error("a dead session survived a write")
	}
	if _, ok := f.Sessions["live"]; !ok {
		t.Error("the live session was swept")
	}
}

func TestSessionWithoutAPIDIsNotAlive(t *testing.T) {
	if (Session{PID: 0}).Alive() {
		t.Error("a session with no pid should not count as alive")
	}
}

// A damaged roster costs an empty panel, not an error: the file is runtime
// state, and refusing to list tasks because of it would be the wrong trade.
func TestUnreadableRosterIsTreatedAsEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(path(s.Root()), []byte("{{ not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := List(s)
	if err != nil {
		t.Fatalf("List should tolerate a damaged roster: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestListIsOrderedByConnectionTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	Register(s, "second", "b", base.Add(time.Minute))
	Register(s, "first", "a", base)
	Register(s, "third", "c", base.Add(2*time.Minute))

	got, err := List(s)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	for i, want := range []string{"first", "second", "third"} {
		if got[i].ID != want {
			t.Errorf("position %d = %s, want %s", i, got[i].ID, want)
		}
	}
}

// The roster lives beside the tasks, and nothing else may be disturbed writing
// it.
func TestRosterFileLivesInTheDataDirectory(t *testing.T) {
	s := newTestStore(t)
	Register(s, "abc123", "claude", time.Now())

	if _, err := os.Stat(filepath.Join(s.Root(), "sessions.json")); err != nil {
		t.Errorf("sessions.json not written to the data directory: %v", err)
	}
}

func writeSessions(t *testing.T, root string, sessions map[string]Session) {
	t.Helper()
	data, err := json.Marshal(file{Sessions: sessions})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path(root), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
