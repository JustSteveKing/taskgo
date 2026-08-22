// Package claim tracks which tasks an AI agent is actively working on.
//
// The activity log already records who last touched a task. That is a
// different question from who is working on it *now*, and the second one
// cannot be inferred from the first: an agent that creates ten tasks and stops
// would look busy on all ten, while an agent that reads a task and thinks for
// fifteen minutes before writing would look idle throughout.
//
// So presence is claimed rather than inferred, and a claim is a lease with an
// expiry. Agents crash, sessions get killed, and a claim that cannot expire
// eventually becomes a permanent lie about work nobody is doing.
//
// Claims live in their own file for the same reason the activity log does, in
// reverse: they are ephemeral, and the Markdown is the durable record. Putting
// lease state in the task files would mean an agent working for an hour
// produces a stream of Git noise unrelated to the task's content.
package claim

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

const stateFile = "claims.json"

// DefaultTTL bounds an implicit claim — the lease taken automatically when an
// agent writes to a task without asking for one. Long enough to survive a
// pause for thought, short enough that a killed agent stops being shown as
// busy within a few minutes.
const DefaultTTL = 5 * time.Minute

// ExplicitTTL bounds a claim the agent asked for. Longer, because an agent
// that called claim_task has said it intends to be a while — but still
// bounded, for the same reason.
const ExplicitTTL = 30 * time.Minute

// Claim is one agent's lease on one task.
type Claim struct {
	TaskID int `json:"task"`
	// By is the agent's name, taken from the MCP handshake rather than a tool
	// argument, so it reflects what connected rather than what a caller typed.
	By      string    `json:"by"`
	Session string    `json:"session"`
	Since   time.Time `json:"since"`
	Expires time.Time `json:"expires"`
	// Explicit distinguishes a lease the agent asked for from one taken
	// automatically on a write, which is worth showing differently.
	Explicit bool `json:"explicit,omitempty"`
}

func (c Claim) Active(now time.Time) bool { return now.Before(c.Expires) }

// Held reports how long the agent has had the task.
func (c Claim) Held(now time.Time) time.Duration { return now.Sub(c.Since) }

// Set maps task id to the claim on it. At most one agent holds a task.
type Set map[int]Claim

func (s Set) Get(taskID int) (Claim, bool) {
	c, ok := s[taskID]
	return c, ok
}

// Sorted returns the claims oldest first, which is the order that makes a
// listing read as "what has been going on longest".
func (s Set) Sorted() []Claim {
	out := make([]Claim, 0, len(s))
	for _, c := range s {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

type file struct {
	Claims map[string]Claim `json:"claims"`
}

func path(root string) string { return filepath.Join(root, stateFile) }

// Load returns the claims that are still active. Expired entries are dropped
// from the result but not rewritten — pruning happens on the next write, so
// reading stays lock-free and cannot fail on a read-only filesystem.
func Load(s *store.Store, now time.Time) (Set, error) {
	raw, err := readFile(s.Root())
	if err != nil {
		return Set{}, err
	}

	out := Set{}
	for key, c := range raw.Claims {
		id, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		c.TaskID = id
		if c.Active(now) {
			out[id] = c
		}
	}
	return out, nil
}

func readFile(root string) (file, error) {
	f := file{Claims: map[string]Claim{}}

	data, err := os.ReadFile(path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read %s: %w", stateFile, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		// A damaged claims file costs at most a wrong highlight. Refusing to
		// run over it would be a far worse trade.
		return file{Claims: map[string]Claim{}}, nil
	}
	if f.Claims == nil {
		f.Claims = map[string]Claim{}
	}
	return f, nil
}

// mutate applies fn to the claim file under the store's write lock, pruning
// expired entries on the way through.
func mutate(s *store.Store, now time.Time, fn func(map[string]Claim) error) error {
	return s.WithWriteLock(func() error {
		f, err := readFile(s.Root())
		if err != nil {
			return err
		}

		for key, c := range f.Claims {
			if !c.Active(now) {
				delete(f.Claims, key)
			}
		}

		if err := fn(f.Claims); err != nil {
			return err
		}

		data, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", stateFile, err)
		}
		return store.WriteFileAtomic(path(s.Root()), append(data, '\n'), 0o644)
	})
}

// Take records a lease, or renews one the same session already holds.
//
// A claim held by a *different* live session is not stolen: two agents on one
// task is worth surfacing rather than papering over, and silently taking it
// would make the display lie about which one is actually working.
func Take(s *store.Store, taskID int, by, session string, ttl time.Duration, explicit bool, now time.Time) (Claim, error) {
	var result Claim

	err := mutate(s, now, func(claims map[string]Claim) error {
		key := strconv.Itoa(taskID)

		if existing, ok := claims[key]; ok && existing.Active(now) && existing.Session != session {
			return fmt.Errorf("task %d is already claimed by %s", taskID, existing.By)
		}

		c := Claim{
			TaskID: taskID, By: by, Session: session,
			Since: now, Expires: now.Add(ttl), Explicit: explicit,
		}
		// Renewing keeps the original start time, so "held for 40 minutes"
		// stays true across renewals instead of resetting every write.
		if existing, ok := claims[key]; ok && existing.Session == session {
			c.Since = existing.Since
			c.Explicit = existing.Explicit || explicit
		}

		claims[key] = c
		result = c
		return nil
	})

	return result, err
}

// Touch renews or takes an implicit lease and never fails on contention: it is
// called after a successful write, and refusing to record presence must not
// turn a completed write into an error the agent sees.
func Touch(s *store.Store, taskID int, by, session string, now time.Time) {
	_, _ = Take(s, taskID, by, session, DefaultTTL, false, now)
}

// Release drops a claim. A session may only release its own, so one agent
// cannot clear another's lease by accident.
func Release(s *store.Store, taskID int, session string, now time.Time) error {
	return mutate(s, now, func(claims map[string]Claim) error {
		key := strconv.Itoa(taskID)
		if existing, ok := claims[key]; ok && existing.Session == session {
			delete(claims, key)
		}
		return nil
	})
}

// ReleaseTask drops a claim regardless of who holds it. Used when a task is
// completed or deleted, where the work is over whoever was doing it.
func ReleaseTask(s *store.Store, taskID int, now time.Time) {
	_ = mutate(s, now, func(claims map[string]Claim) error {
		delete(claims, strconv.Itoa(taskID))
		return nil
	})
}

// ReleaseSession drops every claim a session holds, and is what makes the
// common case correct without a heartbeat: when an agent exits, its stdio
// session ends and its claims go with it. The TTL is only the backstop for
// when the server itself is killed.
func ReleaseSession(s *store.Store, session string, now time.Time) int {
	released := 0
	_ = mutate(s, now, func(claims map[string]Claim) error {
		for key, c := range claims {
			if c.Session == session {
				delete(claims, key)
				released++
			}
		}
		return nil
	})
	return released
}
