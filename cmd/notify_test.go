package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/notify"
	"github.com/JustSteveKing/taskgo/internal/store"
)

// --dry-run is the only way to exercise the notify path in a test suite:
// anything else fires real desktop popups.
func TestNotifyDryRunListsWhatWouldBeSent(t *testing.T) {
	dir := t.TempDir()
	yesterday := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	mustRun(t, dir, "add", "Late thing", "--due", yesterday)
	mustRun(t, dir, "add", "Due today", "--due", "today")
	mustRun(t, dir, "add", "Not due for ages", "--due", time.Now().AddDate(0, 1, 0).Format("2006-01-02"))

	out := mustRun(t, dir, "notify", "--dry-run")
	if !strings.Contains(out, "Late thing") || !strings.Contains(out, "overdue") {
		t.Errorf("overdue task not described:\n%s", out)
	}
	if !strings.Contains(out, "3d late") {
		t.Errorf("lateness not quantified:\n%s", out)
	}
	if !strings.Contains(out, "Due today") {
		t.Errorf("today's task not described:\n%s", out)
	}
	if strings.Contains(out, "Not due for ages") {
		t.Errorf("a task due next month was included:\n%s", out)
	}
}

func TestNotifyDryRunOnAQuietStore(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "No due date")

	out := mustRun(t, dir, "notify", "--dry-run")
	if !strings.Contains(out, "Nothing due") {
		t.Errorf("got %q", out)
	}
}

// A completed task is not something to be reminded about, however late.
func TestNotifyIgnoresCompletedTasks(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	mustRun(t, dir, "add", "Late but finished", "--due", past)
	mustRun(t, dir, "done", "1")

	out := mustRun(t, dir, "notify", "--dry-run")
	if strings.Contains(out, "Late but finished") {
		t.Errorf("a completed task would have been notified:\n%s", out)
	}
}

func TestNotifyJSONReport(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	mustRun(t, dir, "add", "Late", "--due", past)

	var report notify.Report
	decode(t, mustRun(t, dir, "notify", "--json"), &report)
	if len(report.Overdue) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Overdue[0].Entry.Title != "Late" {
		t.Errorf("got %+v", report.Overdue[0])
	}
	if report.Empty() {
		t.Error("a report with an overdue task reports itself empty")
	}
}

// The timer is printed, never installed: writing into a user's systemd
// directory unasked is not a task manager's business.
func TestNotifyPrintTimerEmitsUnitsWithoutTouchingAnything(t *testing.T) {
	dir := t.TempDir()

	out := mustRun(t, dir, "notify", "--print-timer")
	for _, want := range []string{
		"taskgo-notify.service",
		"taskgo-notify.timer",
		"OnCalendar=hourly",
		"systemctl --user enable --now",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("units missing %q:\n%s", want, out)
		}
	}

	// It must not need a store, so it works before taskgo has ever run.
	fresh := t.TempDir() + "/never-used"
	if _, err := run(t, fresh, "notify", "--print-timer"); err != nil {
		t.Errorf("--print-timer needed a data directory: %v", err)
	}
}

func TestDescribeCountsSuppressed(t *testing.T) {
	got := describe(notify.Report{Suppressed: 3})
	if !strings.Contains(got, "3 already notified today") {
		t.Errorf("got %q", got)
	}

	got = describe(notify.Report{
		Overdue:    []notify.Item{{Entry: store.IndexEntry{ID: 1, Title: "Late"}, Days: 2}},
		Suppressed: 1,
	})
	if !strings.Contains(got, "#1") || !strings.Contains(got, "2d late") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "1 suppressed") {
		t.Errorf("suppressed count missing: %q", got)
	}
}
