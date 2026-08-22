// Package agents tracks which AI agents are connected right now.
//
// This is a different question again from claims. A claim says an agent is
// working on a particular task; a session says an agent is *here*. An agent
// that has connected and is reading, planning or waiting on an answer holds
// nothing, and deriving the roster from claims alone would render it invisible
// at exactly the moment you most want to know it exists.
//
// The registry is written by the MCP server on connect and cleared on
// disconnect. Because a hard-killed server never gets to clean up, every entry
// carries the server's pid and readers check whether that process is still
// alive — on Linux that is an exact answer, not a heuristic, which beats any
// timeout for deciding whether something is still running.
package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

const stateFile = "sessions.json"

// Session is one connected agent.
type Session struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PID of the taskgo mcp process serving this session, used to detect
	// entries left behind by a server that was killed outright.
	PID       int       `json:"pid"`
	Connected time.Time `json:"connected"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Idle reports how long since the agent last called a tool.
func (s Session) Idle(now time.Time) time.Duration { return now.Sub(s.LastSeen) }

// Alive reports whether the server process serving this session still exists.
//
// Signal 0 performs the permission and existence checks without delivering
// anything, which is the standard way to ask "is this pid still there".
func (s Session) Alive() bool {
	if s.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(s.PID)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

type file struct {
	Sessions map[string]Session `json:"sessions"`
}

func path(root string) string { return filepath.Join(root, stateFile) }

func read(root string) (file, error) {
	f := file{Sessions: map[string]Session{}}

	data, err := os.ReadFile(path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read %s: %w", stateFile, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		// A damaged roster costs an empty panel, not correctness.
		return file{Sessions: map[string]Session{}}, nil
	}
	if f.Sessions == nil {
		f.Sessions = map[string]Session{}
	}
	return f, nil
}

// List returns the sessions whose server process is still running, newest
// connection last.
func List(s *store.Store) ([]Session, error) {
	f, err := read(s.Root())
	if err != nil {
		return nil, err
	}

	out := make([]Session, 0, len(f.Sessions))
	for _, sess := range f.Sessions {
		if sess.Alive() {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Connected.Before(out[j].Connected) })
	return out, nil
}

func mutate(s *store.Store, fn func(map[string]Session)) error {
	return s.WithWriteLock(func() error {
		f, err := read(s.Root())
		if err != nil {
			return err
		}

		// Sweep entries whose server is gone. Doing it on every write keeps
		// the file from accumulating ghosts without needing a separate reaper.
		for id, sess := range f.Sessions {
			if !sess.Alive() {
				delete(f.Sessions, id)
			}
		}

		fn(f.Sessions)

		data, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", stateFile, err)
		}
		return store.WriteFileAtomic(path(s.Root()), append(data, '\n'), 0o644)
	})
}

// Register records a connected agent, or refreshes one already recorded.
func Register(s *store.Store, id, name string, now time.Time) {
	_ = mutate(s, func(sessions map[string]Session) {
		if existing, ok := sessions[id]; ok {
			existing.LastSeen = now
			existing.Name = name
			sessions[id] = existing
			return
		}
		sessions[id] = Session{
			ID: id, Name: name, PID: os.Getpid(),
			Connected: now, LastSeen: now,
		}
	})
}

// Unregister removes a session that has disconnected cleanly.
func Unregister(s *store.Store, id string) {
	_ = mutate(s, func(sessions map[string]Session) { delete(sessions, id) })
}
