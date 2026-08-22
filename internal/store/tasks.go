package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

// NewTask carries the fields a caller may set when creating. Using a struct
// rather than a long parameter list means the CLI and the MCP server describe
// a new task the same way, and the MCP SDK can infer a schema from it.
type NewTask struct {
	Title    string
	Notes    string
	Status   Status
	Priority Priority
	Due      *DueDate
	Project  string
	Tags     []string
	Parent   int
}

// Update carries changes. Every field is a pointer so that "set to empty" and
// "leave alone" are distinguishable — without this there is no way to clear a
// due date, which is a thing people genuinely need to do.
type Update struct {
	Title    *string
	Notes    *string
	Status   *Status
	Priority *Priority
	Due      **DueDate
	Project  *string
	Tags     *[]string
	Parent   *int
}

// Create writes a new task and returns it with its allocated id.
func (s *Store) Create(actor Actor, in NewTask) (*Task, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, errors.New("a task needs a title")
	}

	status := in.Status
	if status == "" {
		status = StatusTodo
	}
	if !status.Valid() {
		return nil, fmt.Errorf("unknown status %q", status)
	}

	priority := in.Priority
	if priority == "" {
		priority = PriorityNormal
	}
	if !priority.Valid() {
		return nil, fmt.Errorf("unknown priority %q", priority)
	}

	var created *Task

	err := s.withWriteLock(func() error {
		idx, err := s.readIndex()
		if err != nil {
			return err
		}

		if in.Parent != 0 && !idx.has(in.Parent) {
			return fmt.Errorf("parent task %d: %w", in.Parent, ErrNotFound)
		}

		now := s.now().UTC().Truncate(time.Second)
		t := &Task{
			ID:       idx.NextID,
			Title:    title,
			Status:   status,
			Priority: priority,
			Due:      in.Due,
			Project:  strings.TrimSpace(in.Project),
			Tags:     normaliseTags(in.Tags),
			Parent:   in.Parent,
			Created:  now,
			Updated:  now,
			Notes:    strings.TrimSpace(in.Notes),
		}

		if err := s.writeTask(t); err != nil {
			return err
		}

		idx.NextID = t.ID + 1
		idx.upsert(t)
		if err := s.writeIndex(idx); err != nil {
			return err
		}

		created = t
		return s.appendEvent(Event{
			Actor: actor, Action: ActionCreate, Task: t.ID,
			Project: t.Project, Detail: t.Title,
		})
	})

	return created, err
}

// Get reads one task, notes included.
func (s *Store) Get(id int) (*Task, error) {
	t, err := s.readTaskFile(s.taskPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("task %d: %w", id, err)
	}
	t.ID = id
	return t, nil
}

// Update applies a partial change.
func (s *Store) Update(actor Actor, id int, up Update) (*Task, error) {
	var updated *Task

	err := s.withWriteLock(func() error {
		t, err := s.Get(id)
		if err != nil {
			return err
		}

		var changes []string

		if up.Title != nil {
			title := strings.TrimSpace(*up.Title)
			if title == "" {
				return errors.New("a task needs a title")
			}
			if title != t.Title {
				changes = append(changes, "title")
				t.Title = title
			}
		}
		if up.Status != nil {
			if !up.Status.Valid() {
				return fmt.Errorf("unknown status %q", *up.Status)
			}
			if *up.Status != t.Status {
				changes = append(changes, "status="+string(*up.Status))
				t.Status = *up.Status
			}
		}
		if up.Priority != nil {
			if !up.Priority.Valid() {
				return fmt.Errorf("unknown priority %q", *up.Priority)
			}
			if *up.Priority != t.Priority {
				changes = append(changes, "priority="+string(*up.Priority))
				t.Priority = *up.Priority
			}
		}
		if up.Due != nil {
			t.Due = *up.Due
			if t.Due == nil {
				changes = append(changes, "due cleared")
			} else {
				changes = append(changes, "due="+t.Due.String())
			}
		}
		if up.Project != nil {
			t.Project = strings.TrimSpace(*up.Project)
			changes = append(changes, "project")
		}
		if up.Tags != nil {
			t.Tags = normaliseTags(*up.Tags)
			changes = append(changes, "tags")
		}
		if up.Parent != nil {
			if *up.Parent == id {
				return errors.New("a task cannot be its own parent")
			}
			if *up.Parent != 0 {
				if _, err := s.Get(*up.Parent); err != nil {
					return err
				}
			}
			t.Parent = *up.Parent
			changes = append(changes, "parent")
		}
		if up.Notes != nil {
			t.Notes = strings.TrimSpace(*up.Notes)
			changes = append(changes, "notes")
		}

		if len(changes) == 0 {
			updated = t
			return nil
		}

		t.Updated = s.now().UTC().Truncate(time.Second)
		if err := s.writeTask(t); err != nil {
			return err
		}

		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		idx.upsert(t)
		if err := s.writeIndex(idx); err != nil {
			return err
		}

		updated = t
		return s.appendEvent(Event{
			Actor: actor, Action: ActionUpdate, Task: t.ID,
			Project: t.Project, Detail: strings.Join(changes, ", "),
		})
	})

	return updated, err
}

// Complete marks a task done. It is its own method rather than an Update
// because completing is the single most common mutation and deserves a
// distinct activity action.
func (s *Store) Complete(actor Actor, id int) (*Task, error) {
	return s.setDone(actor, id, StatusDone, ActionComplete)
}

// Reopen moves a completed task back to todo.
func (s *Store) Reopen(actor Actor, id int) (*Task, error) {
	return s.setDone(actor, id, StatusTodo, ActionReopen)
}

func (s *Store) setDone(actor Actor, id int, status Status, action Action) (*Task, error) {
	var result *Task

	err := s.withWriteLock(func() error {
		t, err := s.Get(id)
		if err != nil {
			return err
		}
		if t.Status == status {
			result = t
			return nil
		}

		t.Status = status
		t.Updated = s.now().UTC().Truncate(time.Second)

		if err := s.writeTask(t); err != nil {
			return err
		}

		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		idx.upsert(t)
		if err := s.writeIndex(idx); err != nil {
			return err
		}

		result = t
		return s.appendEvent(Event{
			Actor: actor, Action: action, Task: t.ID,
			Project: t.Project, Detail: t.Title,
		})
	})

	return result, err
}

// AddNote appends to the notes body rather than replacing it, so an agent
// leaving a breadcrumb never destroys what a human wrote.
func (s *Store) AddNote(actor Actor, id int, note string) (*Task, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, errors.New("note is empty")
	}

	var result *Task

	err := s.withWriteLock(func() error {
		t, err := s.Get(id)
		if err != nil {
			return err
		}

		stamp := s.now().UTC().Format("2006-01-02 15:04")
		entry := fmt.Sprintf("_%s — %s_\n\n%s", stamp, actor, note)

		if t.Notes == "" {
			t.Notes = entry
		} else {
			t.Notes = t.Notes + "\n\n" + entry
		}
		t.Updated = s.now().UTC().Truncate(time.Second)

		if err := s.writeTask(t); err != nil {
			return err
		}

		result = t
		return s.appendEvent(Event{
			Actor: actor, Action: ActionNote, Task: t.ID,
			Project: t.Project, Detail: firstLine(note),
		})
	})

	return result, err
}

// Delete removes a task file and its index entry.
func (s *Store) Delete(actor Actor, id int) error {
	return s.withWriteLock(func() error {
		t, err := s.Get(id)
		if err != nil {
			return err
		}
		if err := os.Remove(s.taskPath(id)); err != nil {
			return fmt.Errorf("remove task %d: %w", id, err)
		}
		if err := syncDir(s.tasksDir()); err != nil {
			return err
		}

		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		idx.remove(id)
		if err := s.writeIndex(idx); err != nil {
			return err
		}

		return s.appendEvent(Event{
			Actor: actor, Action: ActionDelete, Task: id,
			Project: t.Project, Detail: t.Title,
		})
	})
}

func (s *Store) writeTask(t *Task) error {
	data, err := t.MarshalMarkdown()
	if err != nil {
		return err
	}
	return writeFileAtomic(s.taskPath(t.ID), data, 0o644)
}

func (idx *Index) has(id int) bool {
	for _, e := range idx.Tasks {
		if e.ID == id {
			return true
		}
	}
	return false
}

func normaliseTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
