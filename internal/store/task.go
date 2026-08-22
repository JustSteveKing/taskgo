package store

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Status is where a task sits in its lifecycle. The set is deliberately small:
// a status field with fifteen values is a field nobody sets accurately.
type Status string

const (
	StatusTodo    Status = "todo"
	StatusDoing   Status = "doing"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
)

// Priority ordering matters for sorting, so it is expressed as a rank rather
// than inferred from the string.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

var (
	validStatuses   = []Status{StatusTodo, StatusDoing, StatusBlocked, StatusDone}
	validPriorities = []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}

	priorityRank = map[Priority]int{
		PriorityUrgent: 0,
		PriorityHigh:   1,
		PriorityNormal: 2,
		PriorityLow:    3,
	}
)

func (s Status) Valid() bool {
	for _, v := range validStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func (p Priority) Valid() bool {
	for _, v := range validPriorities {
		if v == p {
			return true
		}
	}
	return false
}

func (p Priority) Rank() int {
	if r, ok := priorityRank[p]; ok {
		return r
	}
	return priorityRank[PriorityNormal]
}

// ParseStatus and ParsePriority accept any casing so `--status DONE` works.
func ParseStatus(raw string) (Status, error) {
	s := Status(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", fmt.Errorf("unknown status %q (want one of: %s)", raw, joinStatuses())
	}
	return s, nil
}

func ParsePriority(raw string) (Priority, error) {
	p := Priority(strings.ToLower(strings.TrimSpace(raw)))
	if !p.Valid() {
		return "", fmt.Errorf("unknown priority %q (want one of: %s)", raw, joinPriorities())
	}
	return p, nil
}

func joinStatuses() string {
	out := make([]string, len(validStatuses))
	for i, s := range validStatuses {
		out[i] = string(s)
	}
	return strings.Join(out, ", ")
}

func joinPriorities() string {
	out := make([]string, len(validPriorities))
	for i, p := range validPriorities {
		out[i] = string(p)
	}
	return strings.Join(out, ", ")
}

// DueDate is a calendar date with no time or zone, held as "YYYY-MM-DD".
//
// It is a defined string rather than a struct for two reasons. First, that is
// exactly how it appears in YAML and JSON, so there are no custom marshallers
// to keep in step and the MCP schema generator infers "string" correctly
// instead of describing an object the wire format never carries. Second, in
// this format lexicographic order IS chronological order, so comparisons are
// string comparisons.
//
// A task due "Tuesday" is due all of Tuesday wherever you are, so carrying a
// timestamp would invent precision the domain does not have.
type DueDate string

const dueLayout = "2006-01-02"

func ParseDue(raw string) (*DueDate, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	now := time.Now()
	switch strings.ToLower(raw) {
	case "today":
		return dueFrom(now), nil
	case "tomorrow":
		return dueFrom(now.AddDate(0, 0, 1)), nil
	}

	t, err := time.Parse(dueLayout, raw)
	if err != nil {
		return nil, fmt.Errorf("bad due date %q (want YYYY-MM-DD, 'today' or 'tomorrow')", raw)
	}
	return dueFrom(t), nil
}

func dueFrom(t time.Time) *DueDate {
	d := DueDate(t.Format(dueLayout))
	return &d
}

// DueOnDay is the DueDate for a given instant, in local time.
func DueOnDay(t time.Time) DueDate { return DueDate(t.Format(dueLayout)) }

func (d DueDate) String() string { return string(d) }

// Time renders the date at local midnight, for day arithmetic.
func (d DueDate) Time() time.Time {
	t, err := time.ParseInLocation(dueLayout, string(d), time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Before compares dates. Sound as a string comparison because the format is
// fixed-width and big-endian.
func (d DueDate) Before(other DueDate) bool { return d < other }

// UnmarshalYAML normalises and validates, so a hand-edited file with a
// malformed date is reported rather than silently carried.
func (d *DueDate) UnmarshalYAML(value *yaml.Node) error {
	parsed, err := ParseDue(value.Value)
	if err != nil {
		return err
	}
	if parsed != nil {
		*d = *parsed
	}
	return nil
}

// Task is the whole model. Frontmatter fields carry `omitempty` so an unset
// optional never appears in the file — a task with no due date should not
// carry `due: ""`, because a human editing the file should see only what is
// actually set.
type Task struct {
	ID       int       `yaml:"id" json:"id"`
	Title    string    `yaml:"title" json:"title"`
	Status   Status    `yaml:"status" json:"status"`
	Priority Priority  `yaml:"priority" json:"priority"`
	Due      *DueDate  `yaml:"due,omitempty" json:"due,omitempty"`
	Project  string    `yaml:"project,omitempty" json:"project,omitempty"`
	Tags     []string  `yaml:"tags,omitempty" json:"tags,omitempty"`
	Parent   int       `yaml:"parent,omitempty" json:"parent,omitempty"`
	Created  time.Time `yaml:"created" json:"created"`
	Updated  time.Time `yaml:"updated" json:"updated"`

	// Notes is the Markdown body, not frontmatter.
	Notes string `yaml:"-" json:"notes,omitempty"`
}

func (t *Task) HasTag(tag string) bool {
	for _, existing := range t.Tags {
		if strings.EqualFold(existing, tag) {
			return true
		}
	}
	return false
}

func (t *Task) AddTag(tag string) {
	tag = strings.TrimSpace(tag)
	if tag == "" || t.HasTag(tag) {
		return
	}
	t.Tags = append(t.Tags, tag)
}

// Overdue is false for completed work: a finished task is never chasing you,
// regardless of when it was due.
func (t *Task) Overdue(now time.Time) bool {
	if t.Due == nil || t.Status == StatusDone {
		return false
	}
	return t.Due.Before(DueOnDay(now))
}

func (t *Task) DueOn(day time.Time) bool {
	if t.Due == nil {
		return false
	}
	return *t.Due == DueOnDay(day)
}

const frontmatterFence = "---"

// MarshalMarkdown renders the canonical on-disk form: YAML frontmatter between
// `---` fences, then the notes body.
func (t *Task) MarshalMarkdown() ([]byte, error) {
	meta, err := yaml.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(frontmatterFence + "\n")
	buf.Write(meta)
	buf.WriteString(frontmatterFence + "\n")

	notes := strings.TrimRight(t.Notes, "\n")
	if notes != "" {
		buf.WriteString("\n")
		buf.WriteString(notes)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

// UnmarshalMarkdown parses a task file.
//
// The split is done by hand rather than with a frontmatter library for one
// reason: the body may legitimately contain a `---` line (a Markdown rule, or
// a nested YAML block), and only the FIRST closing fence terminates the
// frontmatter. Splitting on every `---` would truncate such a body silently.
func (t *Task) UnmarshalMarkdown(data []byte) error {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")

	if !strings.HasPrefix(text, frontmatterFence+"\n") {
		return fmt.Errorf("missing opening %q fence", frontmatterFence)
	}
	rest := text[len(frontmatterFence)+1:]

	end := indexFence(rest)
	if end < 0 {
		return fmt.Errorf("missing closing %q fence", frontmatterFence)
	}

	meta := rest[:end]
	body := rest[end+len(frontmatterFence)+1:]

	if err := yaml.Unmarshal([]byte(meta), t); err != nil {
		return fmt.Errorf("decode frontmatter: %w", err)
	}
	t.Notes = strings.TrimLeft(strings.TrimRight(body, "\n"), "\n")

	if t.Status == "" {
		t.Status = StatusTodo
	}
	if t.Priority == "" {
		t.Priority = PriorityNormal
	}
	return nil
}

// indexFence finds the offset of the first line that is exactly `---`.
func indexFence(s string) int {
	offset := 0
	for {
		if strings.HasPrefix(s[offset:], frontmatterFence+"\n") {
			return offset
		}
		if s[offset:] == frontmatterFence {
			return offset
		}
		next := strings.IndexByte(s[offset:], '\n')
		if next < 0 {
			return -1
		}
		offset += next + 1
		if offset >= len(s) {
			return -1
		}
	}
}
