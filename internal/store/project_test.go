package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndGetProject(t *testing.T) {
	s := newTestStore(t)

	created, err := s.CreateProject(ActorHuman, "web", "  the marketing site  ")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.Name != "web" {
		t.Errorf("name = %q", created.Name)
	}
	if created.Description != "the marketing site" {
		t.Errorf("description = %q, want it trimmed", created.Description)
	}
	if created.Created.IsZero() {
		t.Error("created timestamp not set")
	}

	got, err := s.GetProject("web")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Description != created.Description {
		t.Errorf("round trip lost the description: %q", got.Description)
	}
}

func TestGetMissingProjectIsNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetProject("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateProjectTwiceIsAnError(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateProject(ActorHuman, "web", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.CreateProject(ActorHuman, "web", "again"); err == nil {
		t.Error("creating the same project twice should fail")
	}
}

// A project name becomes a filename, so anything that could escape the
// projects directory — or simply not be addressable afterwards — is refused up
// front rather than written and regretted.
func TestProjectNamesThatWouldEscapeTheDirectoryAreRefused(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{
		"../escape",
		"../../etc/passwd",
		"web/../../oops",
		"/absolute",
		"..",
		".hidden",
		"-leading-dash",
		"has space",
		"has/slash",
		"",
		"   ",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateProject(ActorHuman, name, ""); err == nil {
				t.Errorf("CreateProject(%q) was allowed", name)
			}
			if _, err := s.GetProject(name); err == nil {
				t.Errorf("GetProject(%q) was allowed", name)
			}
		})
	}

	// Nothing was written outside the projects directory, and nothing odd
	// inside it.
	entries, err := os.ReadDir(s.projectsDir())
	if err != nil {
		t.Fatalf("read projects dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected name still wrote a file: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "..", "escape.json")); err == nil {
		t.Error("a project file escaped the data directory")
	}
}

func TestValidProjectNamesAreAccepted(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"web", "Web2", "api-v2", "api_v2", "site.com", "9lives"} {
		if _, err := s.CreateProject(ActorHuman, name, ""); err != nil {
			t.Errorf("CreateProject(%q): %v", name, err)
		}
	}
}

func TestListProjectsCountsTasks(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"web", "infra"} {
		if _, err := s.CreateProject(ActorHuman, name, ""); err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ActorHuman, NewTask{Title: "Web task", Project: "web"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := s.Create(ActorHuman, NewTask{Title: "Infra task", Project: "infra"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Complete(ActorHuman, 1); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := s.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 projects, got %d", len(got))
	}

	counts := map[string][2]int{}
	for _, p := range got {
		counts[p.Name] = [2]int{p.Open, p.Done}
	}
	if counts["web"] != [2]int{2, 1} {
		t.Errorf("web = %v, want 2 open and 1 done", counts["web"])
	}
	if counts["infra"] != [2]int{1, 0} {
		t.Errorf("infra = %v, want 1 open", counts["infra"])
	}
}

func TestListProjectsOnAnEmptyStore(t *testing.T) {
	s := newTestStore(t)

	got, err := s.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestCreatingAProjectIsRecordedInActivity(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateProject(ActorAgent, "web", "made by an agent"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	events, err := s.Activity(0)
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Action != ActionProjectCreate || events[0].Actor != ActorAgent {
		t.Errorf("event = %+v", events[0])
	}
	if events[0].Project != "web" {
		t.Errorf("project = %q", events[0].Project)
	}
}

// A project file someone hand-edited into invalid JSON should name itself in
// the error rather than failing anonymously.
func TestUnparseableProjectFileNamesItself(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateProject(ActorHuman, "web", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := os.WriteFile(s.projectPath("web"), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := s.GetProject("web")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "web") {
		t.Errorf("error should name the project, got: %v", err)
	}
}
