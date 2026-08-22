package store

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Filter narrows a listing. The zero Filter matches everything except that it
// hides completed tasks — the common case is "what is left to do", and having
// to type --status todo every time would be a poor default.
type Filter struct {
	Project     string
	Status      Status
	Tag         string
	Parent      *int
	DueBefore   *DueDate
	DueOn       *DueDate
	IncludeDone bool
	Text        string
}

// List returns index entries matching the filter, ordered the way a person
// reads a task list: overdue and urgent first, then by due date, then
// priority, then id.
func (s *Store) List(f Filter) ([]IndexEntry, error) {
	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}

	var out []IndexEntry
	for _, e := range idx.Tasks {
		if f.matches(e) {
			out = append(out, e)
		}
	}

	sortEntries(out)
	return out, nil
}

func (f Filter) matches(e IndexEntry) bool {
	if f.Status != "" && e.Status != f.Status {
		return false
	}
	if f.Status == "" && !f.IncludeDone && e.Status == StatusDone {
		return false
	}
	if f.Project != "" && !strings.EqualFold(e.Project, f.Project) {
		return false
	}
	if f.Tag != "" {
		found := false
		for _, tag := range e.Tags {
			if strings.EqualFold(tag, f.Tag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Parent != nil && e.Parent != *f.Parent {
		return false
	}
	if f.DueOn != nil {
		if e.Due == nil || *e.Due != *f.DueOn {
			return false
		}
	}
	if f.DueBefore != nil {
		if e.Due == nil || !e.Due.Before(*f.DueBefore) {
			return false
		}
	}
	if f.Text != "" && !strings.Contains(strings.ToLower(e.Title), strings.ToLower(f.Text)) {
		return false
	}
	return true
}

func sortEntries(entries []IndexEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]

		// Anything with a due date outranks anything without one: a dated task
		// is a commitment, an undated one is a wish.
		if (a.Due == nil) != (b.Due == nil) {
			return a.Due != nil
		}
		if a.Due != nil && *a.Due != *b.Due {
			return a.Due.Before(*b.Due)
		}
		if a.Priority.Rank() != b.Priority.Rank() {
			return a.Priority.Rank() < b.Priority.Rank()
		}
		return a.ID < b.ID
	})
}

// Overdue returns unfinished tasks whose due date has passed.
func (s *Store) Overdue(now time.Time) ([]IndexEntry, error) {
	today := DueOnDay(now)
	return s.List(Filter{DueBefore: &today})
}

// Today returns unfinished tasks due today, plus everything already overdue —
// which is what a person actually means by "what's on today".
func (s *Store) Today(now time.Time) ([]IndexEntry, error) {
	today := DueOnDay(now)

	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}

	var out []IndexEntry
	for _, e := range idx.Tasks {
		if e.Status == StatusDone || e.Due == nil {
			continue
		}
		if *e.Due == today || e.Due.Before(today) {
			out = append(out, e)
		}
	}

	sortEntries(out)
	return out, nil
}

// Search matches against titles and notes bodies. Unlike List it must open
// every Markdown file, because the index deliberately does not carry notes.
func (s *Store) Search(text string) ([]IndexEntry, error) {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return nil, nil
	}

	tasks, err := s.scanTasks()
	if err != nil {
		return nil, err
	}

	var out []IndexEntry
	for _, t := range tasks {
		if strings.Contains(strings.ToLower(t.Title), needle) ||
			strings.Contains(strings.ToLower(t.Notes), needle) {
			out = append(out, entryFor(t))
		}
	}

	sortEntries(out)
	return out, nil
}

// Resolve turns what a user typed into a task id.
//
// An exact integer is used as-is. Anything else is matched as a
// case-insensitive title substring, which errors when it is ambiguous rather
// than guessing — completing the wrong task silently is worse than making
// someone retype.
//
// Note this is NOT prefix matching on the id: with sequential integers "4" is
// both a complete id and a prefix of "47", so there is no rule that could
// resolve it safely.
func (s *Store) Resolve(ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, fmt.Errorf("no task given")
	}

	if id, err := strconv.Atoi(ref); err == nil {
		if _, err := s.Get(id); err != nil {
			return 0, err
		}
		return id, nil
	}

	idx, err := s.readIndex()
	if err != nil {
		return 0, err
	}

	needle := strings.ToLower(ref)
	var matches []IndexEntry
	for _, e := range idx.Tasks {
		if strings.Contains(strings.ToLower(e.Title), needle) {
			matches = append(matches, e)
		}
	}

	// Prefer unfinished matches: "done login" almost never means the login
	// task you closed last month.
	if len(matches) > 1 {
		var open []IndexEntry
		for _, e := range matches {
			if e.Status != StatusDone {
				open = append(open, e)
			}
		}
		if len(open) == 1 {
			return open[0].ID, nil
		}
		if len(open) > 1 {
			matches = open
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("no task matching %q: %w", ref, ErrNotFound)
	case 1:
		return matches[0].ID, nil
	default:
		sortEntries(matches)
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d tasks:\n", ref, len(matches))
		for _, e := range matches {
			fmt.Fprintf(&b, "  %d  %s\n", e.ID, e.Title)
		}
		b.WriteString("give the id instead")
		return 0, fmt.Errorf("%s", b.String())
	}
}
