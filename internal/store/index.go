package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// indexVersion guards against a future format change being silently misread.
const indexVersion = 1

// Index is the derived view in state.json: enough to list and filter without
// opening every Markdown file, plus the id counter.
//
// It holds no notes bodies. Those live only in the Markdown, so the index
// stays small and there is exactly one copy of the free-form text.
type Index struct {
	Version int          `json:"version"`
	NextID  int          `json:"nextId"`
	Tasks   []IndexEntry `json:"tasks"`
}

type IndexEntry struct {
	ID       int      `json:"id"`
	Title    string   `json:"title"`
	Status   Status   `json:"status"`
	Priority Priority `json:"priority"`
	Due      *DueDate `json:"due,omitempty"`
	Project  string   `json:"project,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Parent   int      `json:"parent,omitempty"`
	// Question is carried in the index so a listing can show what is waiting
	// on you without opening every task file.
	Question string `json:"question,omitempty"`
	AskedBy  string `json:"askedBy,omitempty"`
}

func entryFor(t *Task) IndexEntry {
	return IndexEntry{
		ID:       t.ID,
		Title:    t.Title,
		Status:   t.Status,
		Priority: t.Priority,
		Due:      t.Due,
		Project:  t.Project,
		Tags:     t.Tags,
		Parent:   t.Parent,
		Question: t.Question,
		AskedBy:  t.AskedBy,
	}
}

// readIndex loads state.json. A missing file is not an error — it is a store
// that has not been written to yet, or one whose index was deleted and will be
// rebuilt. Callers should treat the zero Index as empty, not broken.
func (s *Store) readIndex() (*Index, error) {
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return &Index{Version: indexVersion, NextID: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state.json: %w", err)
	}

	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse state.json (run `taskgo reindex` to rebuild it): %w", err)
	}
	if idx.NextID < 1 {
		idx.NextID = 1
	}
	idx.Version = indexVersion
	return &idx, nil
}

func (s *Store) writeIndex(idx *Index) error {
	idx.Version = indexVersion
	sort.Slice(idx.Tasks, func(i, j int) bool { return idx.Tasks[i].ID < idx.Tasks[j].ID })

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state.json: %w", err)
	}
	return writeFileAtomic(s.statePath(), append(data, '\n'), 0o644)
}

func (idx *Index) upsert(t *Task) {
	entry := entryFor(t)
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == t.ID {
			idx.Tasks[i] = entry
			return
		}
	}
	idx.Tasks = append(idx.Tasks, entry)
}

func (idx *Index) remove(id int) {
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == id {
			idx.Tasks = append(idx.Tasks[:i], idx.Tasks[i+1:]...)
			return
		}
	}
}

// Reindex rebuilds state.json from the Markdown files, which are canonical.
//
// It deliberately does not touch activity.jsonl: the log records things that
// happened and cannot be derived from current state, so regenerating it would
// mean inventing or destroying history. That is the whole reason the log is a
// separate file.
func (s *Store) Reindex() (*Index, error) {
	var rebuilt *Index

	err := s.withWriteLock(func() error {
		tasks, err := s.scanTasks()
		if err != nil {
			return err
		}

		idx := &Index{Version: indexVersion, NextID: 1}
		for _, t := range tasks {
			idx.upsert(t)
			if t.ID >= idx.NextID {
				idx.NextID = t.ID + 1
			}
		}

		if err := s.writeIndex(idx); err != nil {
			return err
		}
		rebuilt = idx
		return nil
	})

	return rebuilt, err
}

// scanTasks reads every task file. Used by Reindex and by anything that needs
// notes bodies for all tasks (full-text search).
func (s *Store) scanTasks() ([]*Task, error) {
	entries, err := os.ReadDir(s.tasksDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	var tasks []*Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		id, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".md"))
		if err != nil {
			// A file whose name is not an id is not ours. Leave it alone
			// rather than guessing — the directory is meant to be safe for a
			// human to keep notes in.
			continue
		}

		t, err := s.readTaskFile(filepath.Join(s.tasksDir(), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("task %d: %w", id, err)
		}

		// The filename is authoritative for identity: a task copied to a new
		// filename should become that task, not silently shadow the original.
		t.ID = id
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

func (s *Store) readTaskFile(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Task
	if err := t.UnmarshalMarkdown(data); err != nil {
		return nil, err
	}
	return &t, nil
}
