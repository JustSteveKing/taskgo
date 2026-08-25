package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Atomic replacement is what lets readers take no lock at all, so it is worth
// testing directly rather than only through the callers that depend on it.
func TestWriteFileAtomicReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")

	if err := WriteFileAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("contents = %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

// The temp file is an implementation detail and must not survive as litter in
// a directory the user is expected to browse and grep.
func TestWriteFileAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 5; i++ {
		if err := WriteFileAtomic(filepath.Join(dir, "target"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFileAtomic: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want just the target", len(entries))
	}
}

// A failed write must not destroy what was already there — the old file is
// better than no file.
func TestFailedAtomicWriteLeavesTheOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := WriteFileAtomic(path, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	// A directory that does not exist is the simplest failure that cannot
	// half-succeed.
	missing := filepath.Join(dir, "nope", "target")
	if err := WriteFileAtomic(missing, []byte("doomed"), 0o644); err == nil {
		t.Fatal("writing into a missing directory should fail")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("the original was disturbed: %q", got)
	}
}

// The property readers actually rely on: a reader taking no lock never sees a
// half-written file, only the old contents or the new ones.
func TestReadersNeverObserveAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")

	small := []byte(strings.Repeat("a", 4096))
	large := []byte(strings.Repeat("b", 1<<20))
	if err := WriteFileAtomic(path, small, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			// Every read must be one of the two complete versions.
			if len(data) != len(small) && len(data) != len(large) {
				t.Errorf("observed a partial file of %d bytes", len(data))
				return
			}
			if len(data) > 0 && strings.Count(string(data), string(data[0:1])) != len(data) {
				t.Error("observed a file mixing both versions")
				return
			}
		}
	}()

	for i := 0; i < 50; i++ {
		want := large
		if i%2 == 0 {
			want = small
		}
		if err := WriteFileAtomic(path, want, 0o644); err != nil {
			t.Errorf("WriteFileAtomic: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()
}

// WithWriteLock is exported so packages keeping their own file in the taskgo
// directory serialise against task writes rather than inventing a second lock.
func TestWithWriteLockSerialises(t *testing.T) {
	s := newTestStore(t)

	var (
		mu      sync.Mutex
		inside  int
		maxSeen int
	)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.WithWriteLock(func() error {
				mu.Lock()
				inside++
				if inside > maxSeen {
					maxSeen = inside
				}
				mu.Unlock()

				// Give any other goroutine the chance to get in, if it can.
				for j := 0; j < 1000; j++ {
					_ = j
				}

				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("WithWriteLock: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxSeen != 1 {
		t.Errorf("%d goroutines were inside the lock at once", maxSeen)
	}
}

// Reopen is its own method rather than an Update because it deserves a
// distinct activity action — which is the part worth checking.
func TestReopenRecordsItsOwnAction(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(ActorHuman, NewTask{Title: "Finish me"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Complete(ActorHuman, 1); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	task, err := s.Reopen(ActorAgent, 1)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if task.Status != StatusTodo {
		t.Errorf("status = %s, want todo", task.Status)
	}

	events, err := s.Activity(1)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(events) != 1 || events[0].Action != ActionReopen {
		t.Fatalf("latest event = %+v, want a reopen", events)
	}
	if events[0].Actor != ActorAgent {
		t.Errorf("actor = %s", events[0].Actor)
	}
}
