package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// Actor records who made a change. It is a required parameter on every
// mutating call rather than a global or an environment variable, so it cannot
// be forgotten: the compiler asks for it.
type Actor string

const (
	ActorHuman Actor = "human"
	ActorAgent Actor = "agent"
)

type Action string

const (
	ActionCreate        Action = "create"
	ActionUpdate        Action = "update"
	ActionComplete      Action = "complete"
	ActionReopen        Action = "reopen"
	ActionDelete        Action = "delete"
	ActionNote          Action = "note"
	ActionProjectCreate Action = "project_create"
	ActionAsk           Action = "ask"
	ActionAnswer        Action = "answer"
)

// Event is one line of activity.jsonl.
type Event struct {
	Time    time.Time `json:"ts"`
	Actor   Actor     `json:"actor"`
	Action  Action    `json:"action"`
	Task    int       `json:"task,omitempty"`
	Project string    `json:"project,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// ChangeHook is called after a mutation has been recorded.
//
// It exists so the history package can commit without the store importing it,
// which would be a cycle. Hooks run inside the write lock, so commits
// serialise against each other and never observe a half-applied change.
type ChangeHook func(e Event)

// OnChange registers a hook. The last registration wins; taskgo needs exactly
// one, and a list would invite ordering questions nothing here can answer.
func (s *Store) OnChange(h ChangeHook) { s.onChange = h }

// appendEvent adds one line to activity.jsonl.
//
// Every mutation appends exactly one event, which makes this the single place
// that knows a change has landed — and therefore the right place to hang the
// change hook.
//
// This is the one write in the package that is NOT atomic-rename, and that is
// deliberate. O_APPEND writes below the pipe buffer size are atomic at the
// syscall level, and rewriting the whole log to add a line would make the file
// quadratic in its own length and lose history on a disk-full. Append-only is
// both cheaper and safer here.
func (s *Store) appendEvent(e Event) error {
	if e.Time.IsZero() {
		// UTC, whole seconds — matching the task frontmatter. A log meant to
		// be read by a human and diffed in Git does not benefit from
		// nanoseconds or a machine-local offset.
		e.Time = s.now().UTC().Truncate(time.Second)
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode activity event: %w", err)
	}

	f, err := os.OpenFile(s.activityPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open activity log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append activity event: %w", err)
	}
	if err := f.Sync(); err != nil {
		return err
	}

	if s.onChange != nil {
		s.onChange(e)
	}
	return nil
}

// Activity returns the most recent events, newest first. A limit of 0 or less
// means everything.
func (s *Store) Activity(limit int) ([]Event, error) {
	data, err := os.ReadFile(s.activityPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read activity log: %w", err)
	}

	var events []Event
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// A single malformed line must not make the whole history
			// unreadable — the log is append-only and a torn final line is a
			// plausible outcome of a crash.
			continue
		}
		events = append(events, e)
	}

	// Reverse into newest-first.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}
