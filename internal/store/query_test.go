package store

import (
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func mustCreate(t *testing.T, s *Store, in NewTask) *Task {
	t.Helper()
	task, err := s.Create(ActorHuman, in)
	if err != nil {
		t.Fatalf("Create %q: %v", in.Title, err)
	}
	return task
}

// Search opens every Markdown file precisely because the index does not carry
// notes — so matching a word that only appears in a body is the point of it.
func TestSearchMatchesNotesAsWellAsTitles(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{Title: "Fix login", Notes: "the redirect loops on Safari"})
	mustCreate(t, s, NewTask{Title: "Safari testing", Notes: "unrelated"})
	mustCreate(t, s, NewTask{Title: "Something else"})

	got, err := s.Search("safari")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d: %+v", len(got), titles(got))
	}
}

func TestSearchIsCaseInsensitiveAndTrims(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{Title: "Fix the LOGIN redirect"})

	for _, q := range []string{"login", "LOGIN", "  Login  "} {
		got, err := s.Search(q)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(got) != 1 {
			t.Errorf("Search(%q) got %d matches", q, len(got))
		}
	}
}

func TestSearchForNothingMatchesNothing(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{Title: "A task"})

	got, err := s.Search("   ")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty search returned %d tasks", len(got))
	}
}

// Today means "due today, plus anything already overdue" — the question being
// asked is what to work on, not what happens to carry today's date.
func TestTodayIncludesOverdue(t *testing.T) {
	s := newTestStore(t)
	now := day(2026, time.August, 25)

	mustCreate(t, s, NewTask{Title: "Due today", Due: ptrDue("2026-08-25")})
	mustCreate(t, s, NewTask{Title: "Late", Due: ptrDue("2026-08-20")})
	mustCreate(t, s, NewTask{Title: "Later", Due: ptrDue("2026-09-01")})
	mustCreate(t, s, NewTask{Title: "No date"})

	got, err := s.Today(now)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("Today = %v, want the due-today and the overdue one", titles(got))
	}

	overdue, err := s.Overdue(now)
	if err != nil {
		t.Fatalf("Overdue: %v", err)
	}
	if len(overdue) != 1 || overdue[0].Title != "Late" {
		t.Errorf("Overdue = %v", titles(overdue))
	}
}

func TestFilterCombinationsCompose(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{
		Title: "Wanted", Project: "web", Status: StatusDoing,
		Tags: []string{"auth"}, Priority: PriorityHigh,
	})
	mustCreate(t, s, NewTask{Title: "Wrong project", Project: "infra", Status: StatusDoing, Tags: []string{"auth"}})
	mustCreate(t, s, NewTask{Title: "Wrong status", Project: "web", Tags: []string{"auth"}})
	mustCreate(t, s, NewTask{Title: "Wrong tag", Project: "web", Status: StatusDoing, Tags: []string{"ui"}})

	got, err := s.List(Filter{Project: "web", Status: StatusDoing, Tag: "auth"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Wanted" {
		t.Errorf("filters did not compose: %v", titles(got))
	}
}

// Filtering by project and tag is case-insensitive, because nobody remembers
// whether they typed Web or web three weeks ago.
func TestProjectAndTagFiltersIgnoreCase(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{Title: "Task", Project: "Web", Tags: []string{"Auth"}})

	got, err := s.List(Filter{Project: "web", Tag: "auth"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("case-sensitive filtering: %v", titles(got))
	}
}

// The sort is a priority order in the ordinary sense of the word, and it is
// not the `priority:` field first. A question outranks everything because an
// agent has stopped; then a due date, because a dated task is a commitment and
// an undated one is a wish; only then the priority field, and finally the id
// so the order is stable.
func TestListSortOrder(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, NewTask{Title: "Normal, no date"})
	mustCreate(t, s, NewTask{Title: "Urgent, no date", Priority: PriorityUrgent})
	mustCreate(t, s, NewTask{Title: "Low, due later", Priority: PriorityLow, Due: ptrDue("2026-09-01")})
	mustCreate(t, s, NewTask{Title: "Low, due sooner", Priority: PriorityLow, Due: ptrDue("2026-08-01")})
	mustCreate(t, s, NewTask{Title: "Asked about"})
	if _, err := s.Ask(ActorAgent, 5, "claude", "Which way?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{
		"Asked about",     // a question beats a due date
		"Low, due sooner", // a due date beats the priority field
		"Low, due later",
		"Urgent, no date", // only now does priority decide
		"Normal, no date",
	}
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("position %d = %q, want %q (full order %v)", i, got[i].Title, title, titles(got))
		}
	}
}

// Two tasks alike in every ranked field fall back to the id, so the list does
// not shuffle under the cursor between refreshes.
func TestListSortIsStableOnID(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		mustCreate(t, s, NewTask{Title: "Identical"})
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for i, e := range got {
		if e.ID != i+1 {
			t.Fatalf("position %d holds id %d; the tie-break is not the id", i, e.ID)
		}
	}
}

func TestResolveNothing(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Resolve("  "); err == nil {
		t.Error("an empty reference should fail")
	}
	if _, err := s.Resolve("no such task anywhere"); err == nil {
		t.Error("an unmatched reference should fail")
	}
}

func titles(entries []IndexEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Title)
	}
	return out
}
