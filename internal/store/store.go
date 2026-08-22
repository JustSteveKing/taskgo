// Package store owns every read and write of the taskgo data directory.
//
// The CLI, the MCP server and the TUI all go through this package; none of
// them touch the filesystem directly. That is what keeps a task created by an
// agent and a task created by a human genuinely identical on disk.
//
// Two invariants hold everywhere in here:
//
//   - Markdown is canonical, state.json is derived. Writes update the Markdown
//     first. If the two ever disagree, the Markdown wins and Reindex says so.
//     This is what lets a human edit a task file in an editor without
//     corrupting anything.
//
//   - Every write is atomic: temp file, fsync, rename, fsync the directory.
//     Rename is atomic within a filesystem, so a reader never observes a
//     partial file — which is why reads take no lock at all. Only writers lock.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// ErrNotFound is returned when a task or project does not exist. Callers use
// errors.Is to distinguish "no such task" from a real IO failure.
var ErrNotFound = errors.New("not found")

// Store is a handle on a taskgo data directory.
type Store struct {
	root string

	// Two locks, guarding two different things.
	//
	// flock is advisory and PER PROCESS: it stops the CLI and a running MCP
	// server from writing over each other, but it does not serialise
	// goroutines inside one process — every goroutine holding this same
	// *Flock acquires it happily. The MCP server is long-running and serves
	// concurrent requests, so without mu two agent calls can interleave an id
	// allocation and both write task 2.
	//
	// mu is therefore not belt-and-braces; it is the half of the problem
	// flock does not solve.
	mu   sync.Mutex
	lock *flock.Flock

	// now is swappable so tests can pin timestamps.
	now func() time.Time

	// onChange fires after a mutation is recorded. See OnChange.
	onChange ChangeHook
}

// Open prepares a data directory, creating it if absent.
func Open(root string) (*Store, error) {
	s := &Store{
		root: root,
		lock: flock.New(filepath.Join(root, ".lock")),
		now:  time.Now,
	}

	for _, dir := range []string{root, s.tasksDir(), s.projectsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return s, nil
}

func (s *Store) Root() string        { return s.root }
func (s *Store) tasksDir() string    { return filepath.Join(s.root, "tasks") }
func (s *Store) projectsDir() string { return filepath.Join(s.root, "projects") }
func (s *Store) statePath() string   { return filepath.Join(s.root, "state.json") }
func (s *Store) activityPath() string {
	return filepath.Join(s.root, "activity.jsonl")
}

func (s *Store) taskPath(id int) string {
	return filepath.Join(s.tasksDir(), strconv.Itoa(id)+".md")
}

func (s *Store) projectPath(name string) string {
	return filepath.Join(s.projectsDir(), name+".json")
}

// WithWriteLock runs fn holding the store's exclusive lock.
//
// Exported so packages that keep their own file in the taskgo directory
// (notifications, claims) serialise against task writes using the same lock
// rather than inventing a second one. One lock for the whole directory is
// easier to reason about than several, and contention here is one human and
// one agent.
func (s *Store) WithWriteLock(fn func() error) error { return s.withWriteLock(fn) }

// withWriteLock runs fn holding the exclusive lock.
//
// The lock is coarse on purpose: it wraps a whole mutation — allocate an id,
// write the Markdown, update the index, append to the activity log — so those
// either all land or none do. At this scale contention is one human and one
// agent, not a fleet, and a coarse lock that is obviously correct beats a fine
// one that is subtly wrong.
func (s *Store) withWriteLock(fn func() error) error {
	// In-process first, so goroutines queue here rather than all sailing
	// through the advisory lock together.
	s.mu.Lock()
	defer s.mu.Unlock()

	locked, err := s.lock.TryLockContext(lockContext(), 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	if !locked {
		return errors.New("timed out waiting for the taskgo lock; another taskgo process may be stuck")
	}
	defer func() { _ = s.lock.Unlock() }()

	return fn()
}

// WriteFileAtomic is the exported form, for packages that keep their own file
// in the taskgo directory and need the same durability guarantee.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm)
}

// writeFileAtomic replaces path's contents in a way no reader can observe
// half-done.
//
// The directory fsync at the end is the step people skip: without it the
// rename itself can be lost on power failure even though the file contents
// were flushed, leaving the old file (or no file) behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Any failure from here on must not leave the temp file behind.
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename into %s: %w", path, err)
	}

	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", dir, err)
	}
	defer func() { _ = d.Close() }()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
