package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// These tests exercise Collect and the record file directly. Send is not
// called: it shells out to notify-send, and a test suite that fires real
// desktop notifications is a test suite people stop running.

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func addDue(t *testing.T, s *store.Store, title, due string) *store.Task {
	t.Helper()
	parsed, err := store.ParseDue(due)
	if err != nil {
		t.Fatalf("parse due %q: %v", due, err)
	}
	task, err := s.Create(store.ActorHuman, store.NewTask{Title: title, Due: parsed})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return task
}

func TestCollectSplitsOverdueFromToday(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Three days late", "2026-08-19")
	addDue(t, s, "Due today", "2026-08-22")
	addDue(t, s, "Not yet", "2026-09-01")
	addDue(t, s, "No date at all", "")

	report, err := Collect(s, now, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(report.Overdue) != 1 {
		t.Fatalf("overdue = %d, want 1: %+v", len(report.Overdue), report.Overdue)
	}
	if report.Overdue[0].Days != 3 {
		t.Errorf("days late = %d, want 3", report.Overdue[0].Days)
	}
	if len(report.Today) != 1 || report.Today[0].Entry.Title != "Due today" {
		t.Errorf("today = %+v", report.Today)
	}
}

func TestCollectIgnoresCompletedTasks(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	task := addDue(t, s, "Late but finished", "2026-08-01")
	if _, err := s.Complete(store.ActorHuman, task.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	report, err := Collect(s, now, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !report.Empty() {
		t.Errorf("a finished task should never be chased: %+v", report)
	}
}

// The point of the record file: a task that stays overdue must not produce an
// identical notification on every run of the timer.
func TestSecondRunOnTheSameDayIsSilent(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Late", "2026-08-01")
	addDue(t, s, "Today", "2026-08-22")

	first, err := Collect(s, now, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if first.Count() != 2 {
		t.Fatalf("first run found %d, want 2", first.Count())
	}
	if err := record(s.Root(), first, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	second, err := Collect(s, now.Add(2*time.Hour), false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !second.Empty() {
		t.Errorf("second run should be silent, got %+v", second)
	}
	if second.Suppressed != 2 {
		t.Errorf("suppressed = %d, want 2", second.Suppressed)
	}
}

// ...but a task that appears later the same day must still surface. This is
// why the record is kept per task rather than per run.
func TestNewTaskSurfacesAfterAnEarlierRun(t *testing.T) {
	s := newStore(t)
	morning := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Known this morning", "2026-08-22")

	first, _ := Collect(s, morning, false)
	if err := record(s.Root(), first, morning); err != nil {
		t.Fatalf("record: %v", err)
	}

	addDue(t, s, "Added at lunchtime", "2026-08-22")

	afternoon := morning.Add(5 * time.Hour)
	second, err := Collect(s, afternoon, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if second.Count() != 1 {
		t.Fatalf("want just the new task, got %+v", second)
	}
	if second.Today[0].Entry.Title != "Added at lunchtime" {
		t.Errorf("surfaced the wrong task: %+v", second.Today[0])
	}
	if second.Suppressed != 1 {
		t.Errorf("suppressed = %d, want 1", second.Suppressed)
	}
}

// A task still overdue tomorrow should nag again — once.
func TestTheNextDayNotifiesAgain(t *testing.T) {
	s := newStore(t)
	today := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Persistently late", "2026-08-01")

	first, _ := Collect(s, today, false)
	if err := record(s.Root(), first, today); err != nil {
		t.Fatalf("record: %v", err)
	}

	tomorrow := today.AddDate(0, 0, 1)
	next, err := Collect(s, tomorrow, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if next.Count() != 1 {
		t.Errorf("a task still late tomorrow should be mentioned again, got %+v", next)
	}
}

func TestForceIgnoresTheRecord(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Late", "2026-08-01")
	first, _ := Collect(s, now, false)
	if err := record(s.Root(), first, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	forced, err := Collect(s, now, true)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if forced.Count() != 1 {
		t.Errorf("--force should ignore the record, got %+v", forced)
	}
}

// The record must not accumulate an entry for every task that ever existed.
//
// Note this only prunes entries that stop being refreshed. A task that is
// still overdue months later is legitimately re-notified each day, so its
// entry keeps a current date and must survive — which is why this test
// completes the old task rather than just waiting.
func TestRecordPrunesEntriesForTasksNoLongerMentioned(t *testing.T) {
	s := newStore(t)
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)

	ancient := addDue(t, s, "Ancient", "2025-12-01")
	first, _ := Collect(s, old, false)
	if err := record(s.Root(), first, old); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Finish it, so it is never collected again and its entry goes stale.
	if _, err := s.Complete(store.ActorHuman, ancient.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	muchLater := old.AddDate(0, 6, 0)
	addDue(t, s, "Recent", "2026-06-25")
	second, _ := Collect(s, muchLater, false)
	if err := record(s.Root(), second, muchLater); err != nil {
		t.Fatalf("record: %v", err)
	}

	st, err := readState(s.Root())
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if when, stale := st.LastNotified["1"]; stale {
		t.Errorf("the stale entry (%s) was not pruned: %+v", when, st.LastNotified)
	}
	if _, ok := st.LastNotified["2"]; !ok {
		t.Error("the current entry was pruned along with the stale one")
	}
}

// A task that is still overdue months later keeps being mentioned, so its
// record entry must be refreshed rather than pruned.
func TestStillOverdueEntryIsRefreshedNotPruned(t *testing.T) {
	s := newStore(t)
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)

	addDue(t, s, "Perpetually late", "2025-12-01")
	first, _ := Collect(s, old, false)
	if err := record(s.Root(), first, old); err != nil {
		t.Fatalf("record: %v", err)
	}

	muchLater := old.AddDate(0, 6, 0)
	second, err := Collect(s, muchLater, false)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if second.Count() != 1 {
		t.Fatalf("a still-late task should be mentioned again, got %+v", second)
	}
	if err := record(s.Root(), second, muchLater); err != nil {
		t.Fatalf("record: %v", err)
	}

	st, _ := readState(s.Root())
	if st.LastNotified["1"] != "2026-07-01" {
		t.Errorf("entry should have been refreshed, got %q", st.LastNotified["1"])
	}
}

// A damaged record should cost at most a duplicate notification, never a
// refusal to run.
func TestDamagedRecordIsToleratedNotFatal(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	addDue(t, s, "Late", "2026-08-01")

	if err := os.WriteFile(filepath.Join(s.Root(), stateFile), []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	report, err := Collect(s, now, false)
	if err != nil {
		t.Fatalf("Collect should tolerate a damaged record: %v", err)
	}
	if report.Count() != 1 {
		t.Errorf("got %+v", report)
	}
}

func TestRecordFileIsReadableJSON(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.Local)
	addDue(t, s, "Late", "2026-08-01")

	report, _ := Collect(s, now, false)
	if err := record(s.Root(), report, now); err != nil {
		t.Fatalf("record: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(s.Root(), stateFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed state
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("the record should be plain readable JSON: %v\n%s", err, data)
	}
	if parsed.LastNotified["1"] != "2026-08-22" {
		t.Errorf("recorded %+v", parsed.LastNotified)
	}
}

// The body must not grow without bound, and must say how much it elided.
func TestBodyCapsLongLists(t *testing.T) {
	var items []Item
	for i := 1; i <= 20; i++ {
		items = append(items, Item{Entry: store.IndexEntry{ID: i, Title: "Task"}})
	}

	text := body(items)
	if lines := strings.Count(text, "\n") + 1; lines > 8 {
		t.Errorf("body is %d lines; it should be capped", lines)
	}
	if !strings.Contains(text, "and 14 more") {
		t.Errorf("body should say what it elided:\n%s", text)
	}
}

func TestPlural(t *testing.T) {
	cases := map[string]string{
		plural(1, "task"): "1 task",
		plural(2, "task"): "2 tasks",
		plural(1, "day"):  "1 day",
		plural(0, "day"):  "0 days",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
