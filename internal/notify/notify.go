// Package notify sends desktop notifications for work that is due or late.
//
// It is meant to be run on a timer rather than to sit resident, so it keeps
// its own record of what it has already told you about. Without that, a task
// that stays overdue would produce an identical notification on every run —
// which is how people learn to ignore notifications.
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// stateFile records the last date each task was mentioned. It lives alongside
// the tasks but is not part of them: like activity.jsonl it must survive a
// reindex, and unlike state.json it cannot be derived from anything.
const stateFile = "notified.json"

type state struct {
	// LastNotified maps a task id to the date it was last included in a
	// notification, as YYYY-MM-DD.
	LastNotified map[string]string `json:"lastNotified"`
}

// Item is one thing worth telling the user about.
type Item struct {
	Entry   store.IndexEntry `json:"task"`
	Overdue bool             `json:"overdue"`
	Days    int              `json:"daysLate,omitempty"`
}

// Report is what a run found.
type Report struct {
	Overdue []Item `json:"overdue"`
	Today   []Item `json:"today"`
	// Suppressed counts items skipped because they were already mentioned
	// today, so a dry run can explain a quiet result.
	Suppressed int `json:"suppressed"`
}

func (r Report) Empty() bool { return len(r.Overdue) == 0 && len(r.Today) == 0 }
func (r Report) Count() int  { return len(r.Overdue) + len(r.Today) }

// Collect finds what needs saying. When force is false, tasks already
// mentioned today are filtered out — but a task that becomes overdue after an
// earlier run still surfaces, because the record is kept per task rather than
// per run.
func Collect(s *store.Store, now time.Time, force bool) (Report, error) {
	var report Report

	entries, err := s.Today(now)
	if err != nil {
		return report, err
	}

	st, err := readState(s.Root())
	if err != nil {
		return report, err
	}
	today := store.DueOnDay(now).String()

	for _, e := range entries {
		if !force && st.LastNotified[strconv.Itoa(e.ID)] == today {
			report.Suppressed++
			continue
		}

		item := Item{Entry: e}
		if e.Due != nil {
			days := int(store.DueOnDay(now).Time().Sub(e.Due.Time()).Hours() / 24)
			if days > 0 {
				item.Overdue, item.Days = true, days
			}
		}

		if item.Overdue {
			report.Overdue = append(report.Overdue, item)
		} else {
			report.Today = append(report.Today, item)
		}
	}

	return report, nil
}

// Available reports whether notifications can actually be delivered.
func Available() error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return errors.New("notify-send is not installed (on Arch it is in the libnotify package)")
	}
	return nil
}

// Send delivers the report and records what was mentioned.
//
// One notification per urgency rather than one per task: five separate popups
// for five late tasks is not five times as useful, it is how the whole
// mechanism gets muted.
func Send(s *store.Store, report Report, now time.Time) error {
	if report.Empty() {
		return nil
	}
	if err := Available(); err != nil {
		return err
	}

	if len(report.Overdue) > 0 {
		title := fmt.Sprintf("%s overdue", plural(len(report.Overdue), "task"))
		if err := send("critical", title, body(report.Overdue)); err != nil {
			return err
		}
	}
	if len(report.Today) > 0 {
		title := fmt.Sprintf("%s due today", plural(len(report.Today), "task"))
		if err := send("normal", title, body(report.Today)); err != nil {
			return err
		}
	}

	return record(s.Root(), report, now)
}

func send(urgency, title, text string) error {
	cmd := exec.Command("notify-send",
		"--app-name=taskgo",
		"--urgency="+urgency,
		title, text,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// body lists the tasks, capped so a large backlog does not produce a
// notification taller than the screen.
func body(items []Item) string {
	const maxLines = 6

	var b strings.Builder
	for i, item := range items {
		if i == maxLines {
			fmt.Fprintf(&b, "\n…and %d more", len(items)-maxLines)
			break
		}
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "#%d %s", item.Entry.ID, item.Entry.Title)
		if item.Overdue {
			fmt.Fprintf(&b, " (%s late)", plural(item.Days, "day"))
		}
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ---------------------------------------------------------------- state file

func statePath(root string) string { return filepath.Join(root, stateFile) }

func readState(root string) (state, error) {
	st := state{LastNotified: map[string]string{}}

	data, err := os.ReadFile(statePath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("read %s: %w", stateFile, err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		// A damaged record costs at worst one duplicate notification, which is
		// a far better outcome than refusing to run at all.
		return state{LastNotified: map[string]string{}}, nil
	}
	if st.LastNotified == nil {
		st.LastNotified = map[string]string{}
	}
	return st, nil
}

func record(root string, report Report, now time.Time) error {
	st, err := readState(root)
	if err != nil {
		return err
	}

	today := store.DueOnDay(now).String()
	for _, item := range append(append([]Item{}, report.Overdue...), report.Today...) {
		st.LastNotified[strconv.Itoa(item.Entry.ID)] = today
	}

	// Forget anything not mentioned for a fortnight, so the file cannot grow
	// without bound as tasks come and go.
	cutoff := store.DueOnDay(now.AddDate(0, 0, -14)).String()
	for id, when := range st.LastNotified {
		if when < cutoff {
			delete(st.LastNotified, id)
		}
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", stateFile, err)
	}
	return store.WriteFileAtomic(statePath(root), append(data, '\n'), 0o644)
}
