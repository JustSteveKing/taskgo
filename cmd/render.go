package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// statusMark is a one-glyph status column. Plain ASCII: this output gets piped
// into other tools and read over SSH.
func statusMark(s store.Status) string {
	switch s {
	case store.StatusDone:
		return "x"
	case store.StatusDoing:
		return ">"
	case store.StatusBlocked:
		return "!"
	default:
		return " "
	}
}

func priorityMark(p store.Priority) string {
	switch p {
	case store.PriorityUrgent:
		return "!!"
	case store.PriorityHigh:
		return "!"
	case store.PriorityLow:
		return "."
	default:
		return ""
	}
}

// dueLabel renders a due date relative to today, because "overdue 3d" tells
// you what to do and "2026-08-19" makes you work it out.
func dueLabel(due *store.DueDate, now time.Time) string {
	if due == nil {
		return ""
	}

	today := store.DueOnDay(now)

	days := int(due.Time().Sub(today.Time()).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("overdue %dd", -days)
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days <= 7:
		return fmt.Sprintf("in %dd", days)
	default:
		return due.String()
	}
}

func renderTaskTable(w io.Writer, entries []store.IndexEntry, now time.Time) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tS\tP\tTITLE\tPROJECT\tDUE\tTAGS")

	for _, e := range entries {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.ID,
			statusMark(e.Status),
			priorityMark(e.Priority),
			e.Title,
			e.Project,
			dueLabel(e.Due, now),
			strings.Join(e.Tags, ","),
		)
	}
	_ = tw.Flush()
}

func renderTask(w io.Writer, t *store.Task, now time.Time) {
	fmt.Fprintf(w, "#%d  %s\n", t.ID, t.Title)
	fmt.Fprintf(w, "status    %s\n", t.Status)
	fmt.Fprintf(w, "priority  %s\n", t.Priority)

	if t.Due != nil {
		fmt.Fprintf(w, "due       %s (%s)\n", t.Due, dueLabel(t.Due, now))
	}
	if t.Project != "" {
		fmt.Fprintf(w, "project   %s\n", t.Project)
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(w, "tags      %s\n", strings.Join(t.Tags, ", "))
	}
	if t.Parent != 0 {
		fmt.Fprintf(w, "parent    #%d\n", t.Parent)
	}
	fmt.Fprintf(w, "created   %s\n", t.Created.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(w, "updated   %s\n", t.Updated.Local().Format("2006-01-02 15:04"))

	if t.Notes != "" {
		fmt.Fprintf(w, "\n%s\n", t.Notes)
	}
}
