package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Project metadata. Projects are deliberately thin — a name, a description and
// a creation time. Anything richer belongs on the tasks.
type Project struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Created     time.Time `json:"created"`
}

// projectNamePattern keeps a project name usable as a filename and as a CLI
// argument without quoting. Rejecting up front is kinder than writing a file
// the user then cannot address.
var projectNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validProjectName(name string) error {
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("invalid project name %q: use letters, digits, dot, dash or underscore, starting with a letter or digit", name)
	}
	return nil
}

func (s *Store) CreateProject(actor Actor, name, description string) (*Project, error) {
	name = strings.TrimSpace(name)
	if err := validProjectName(name); err != nil {
		return nil, err
	}

	var created *Project

	err := s.withWriteLock(func() error {
		if _, err := s.GetProject(name); err == nil {
			return fmt.Errorf("project %q already exists", name)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}

		p := &Project{
			Name:        name,
			Description: strings.TrimSpace(description),
			Created:     s.now().UTC().Truncate(time.Second),
		}

		data, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return fmt.Errorf("encode project: %w", err)
		}
		if err := writeFileAtomic(s.projectPath(name), append(data, '\n'), 0o644); err != nil {
			return err
		}

		created = p
		return s.appendEvent(Event{
			Actor: actor, Action: ActionProjectCreate,
			Project: name, Detail: p.Description,
		})
	})

	return created, err
}

func (s *Store) GetProject(name string) (*Project, error) {
	if err := validProjectName(name); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.projectPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read project %q: %w", name, err)
	}

	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse project %q: %w", name, err)
	}
	return &p, nil
}

// ProjectSummary pairs a project with its task counts, which is what anyone
// listing projects actually wants to see.
type ProjectSummary struct {
	Project
	Open int `json:"open"`
	Done int `json:"done"`
}

func (s *Store) ListProjects() ([]ProjectSummary, error) {
	entries, err := os.ReadDir(s.projectsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	idx, err := s.readIndex()
	if err != nil {
		return nil, err
	}

	counts := map[string][2]int{}
	for _, e := range idx.Tasks {
		if e.Project == "" {
			continue
		}
		key := strings.ToLower(e.Project)
		c := counts[key]
		if e.Status == StatusDone {
			c[1]++
		} else {
			c[0]++
		}
		counts[key] = c
	}

	var out []ProjectSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")

		var p Project
		data, err := os.ReadFile(filepath.Join(s.projectsDir(), entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read project %q: %w", name, err)
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse project %q: %w", name, err)
		}

		c := counts[strings.ToLower(p.Name)]
		out = append(out, ProjectSummary{Project: p, Open: c[0], Done: c[1]})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
