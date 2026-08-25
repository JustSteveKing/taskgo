package store

import (
	"strings"
	"testing"
	"time"
)

// A rejected value must name the values that would have worked, because the
// person who typed it is about to type another one.
func TestParseStatusAndPriorityListTheValidValues(t *testing.T) {
	_, err := ParseStatus("nearly")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"nearly", "todo", "doing", "blocked", "done"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("status error should mention %q, got: %v", want, err)
		}
	}

	_, err = ParsePriority("critical")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"critical", "low", "normal", "high", "urgent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("priority error should mention %q, got: %v", want, err)
		}
	}
}

func TestParseStatusAndPriorityAcceptCaseAndSpace(t *testing.T) {
	for _, in := range []string{"doing", "DOING", "  Doing  "} {
		got, err := ParseStatus(in)
		if err != nil {
			t.Errorf("ParseStatus(%q): %v", in, err)
		} else if got != StatusDoing {
			t.Errorf("ParseStatus(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"urgent", "URGENT", "  Urgent  "} {
		got, err := ParsePriority(in)
		if err != nil {
			t.Errorf("ParsePriority(%q): %v", in, err)
		} else if got != PriorityUrgent {
			t.Errorf("ParsePriority(%q) = %q", in, got)
		}
	}
}

// Priority sorts by rank rather than by string, which is the only reason the
// rank exists.
func TestPriorityRanksUrgentFirst(t *testing.T) {
	order := []Priority{PriorityUrgent, PriorityHigh, PriorityNormal, PriorityLow}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() >= order[i].Rank() {
			t.Errorf("%s does not rank above %s", order[i-1], order[i])
		}
	}
	// Alphabetically "high" < "urgent", which is exactly the trap.
	if PriorityHigh.Rank() < PriorityUrgent.Rank() {
		t.Error("high ranks above urgent")
	}
}

func TestValidRejectsUnknownValues(t *testing.T) {
	if Status("nearly").Valid() {
		t.Error("an unknown status reported valid")
	}
	if Priority("critical").Valid() {
		t.Error("an unknown priority reported valid")
	}
	if !StatusBlocked.Valid() || !PriorityLow.Valid() {
		t.Error("a known value reported invalid")
	}
}

func TestParseDue(t *testing.T) {
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)

	got, err := ParseDue("2026-12-25")
	if err != nil {
		t.Fatalf("ParseDue: %v", err)
	}
	if got.String() != "2026-12-25" {
		t.Errorf("got %q", got)
	}

	// today and tomorrow are relative to now, which is why they are words
	// rather than something the shell could expand.
	today, err := ParseDue("today")
	if err != nil {
		t.Fatalf("ParseDue(today): %v", err)
	}
	if want := DueOnDay(now); today.String() != want.String() && today.String() != DueOnDay(time.Now()).String() {
		t.Errorf("today = %q", today)
	}

	tomorrow, err := ParseDue("tomorrow")
	if err != nil {
		t.Fatalf("ParseDue(tomorrow): %v", err)
	}
	if !today.Before(*tomorrow) {
		t.Errorf("tomorrow (%q) does not sort after today (%q)", tomorrow, today)
	}

	for _, bad := range []string{"25-12-2026", "next tuesday", "2026-13-01", "soon"} {
		if _, err := ParseDue(bad); err == nil {
			t.Errorf("ParseDue(%q) was accepted", bad)
		}
	}
}

func TestDueDateComparisonsAreLexical(t *testing.T) {
	// The format is chosen so that string order is date order; this is what
	// lets the index sort without parsing.
	if !DueDate("2026-08-01").Before(DueDate("2026-09-01")) {
		t.Error("August does not sort before September")
	}
	if DueDate("2026-09-01").Before(DueDate("2026-08-01")) {
		t.Error("September sorts before August")
	}

	tm := DueDate("2026-08-25").Time()
	if tm.Year() != 2026 || tm.Month() != time.August || tm.Day() != 25 {
		t.Errorf("Time() = %v", tm)
	}
}

func TestTaskOverdueAndDueOn(t *testing.T) {
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)

	task := &Task{Title: "Late", Status: StatusTodo, Due: ptrDue("2026-08-20")}
	if !task.Overdue(now) {
		t.Error("a task due five days ago is not overdue")
	}

	// Due today is not overdue: you still have the day.
	task.Due = ptrDue("2026-08-25")
	if task.Overdue(now) {
		t.Error("a task due today is overdue")
	}
	if !task.DueOn(now) {
		t.Error("DueOn is false on the due date")
	}

	// Completing it takes it out of the reckoning, whatever the date says.
	task.Due = ptrDue("2026-08-01")
	task.Status = StatusDone
	if task.Overdue(now) {
		t.Error("a completed task is still overdue")
	}

	// And a task with no date can never be late.
	if (&Task{Status: StatusTodo}).Overdue(now) {
		t.Error("an undated task is overdue")
	}
}

func TestTagHelpers(t *testing.T) {
	task := &Task{Tags: []string{"auth"}}

	if !task.HasTag("auth") || !task.HasTag("AUTH") {
		t.Error("HasTag should be case-insensitive")
	}
	if task.HasTag("ui") {
		t.Error("HasTag matched a tag that is not there")
	}

	task.AddTag("ui")
	if len(task.Tags) != 2 {
		t.Fatalf("tags = %v", task.Tags)
	}

	// Adding one that is already there, in any case, changes nothing.
	task.AddTag("AUTH")
	if len(task.Tags) != 2 {
		t.Errorf("a duplicate tag was added: %v", task.Tags)
	}
}

// Tags are deduplicated case-insensitively but keep the case they were typed
// in, because the tag is also a label a human reads.
func TestTagsAreDeduplicatedOnWrite(t *testing.T) {
	s := newTestStore(t)
	task, err := s.Create(ActorHuman, NewTask{
		Title: "Tagged",
		Tags:  []string{"Auth", "auth", "  ", "AUTH", "ui", ""},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(task.Tags) != 2 {
		t.Fatalf("tags = %v, want two", task.Tags)
	}
	if task.Tags[0] != "Auth" {
		t.Errorf("tags = %v, want the first spelling kept", task.Tags)
	}
}

func TestAwaitingAnswer(t *testing.T) {
	if (&Task{}).AwaitingAnswer() {
		t.Error("a task with no question is awaiting an answer")
	}
	if !(&Task{Question: "well?"}).AwaitingAnswer() {
		t.Error("a task with a question is not awaiting an answer")
	}
}
