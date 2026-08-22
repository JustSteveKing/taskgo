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
// waitingMark flags a task an agent has stopped on. It takes the status
// column because "an agent is stuck on this" outranks "this is in progress" —
// nothing is progressing until you answer.
func waitingMark(e store.IndexEntry) string {
	if e.Question != "" {
		return "?"
	}
	return statusMark(e.Status)
}

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
	renderTaskTableWithClaims(w, entries, nil, now)
}

// renderTaskTableWithClaims adds an AGENT column when something is being
// worked on, and omits it entirely when nothing is — an always-empty column is
// a permanent question in the reader's mind.
func renderTaskTableWithClaims(w io.Writer, entries []store.IndexEntry, claims map[int]string, now time.Time) {
	renderTaskTableFull(w, entries, claims, nil, now)
}

func renderTaskTableFull(w io.Writer, entries []store.IndexEntry, claims map[int]string, progress map[int]store.Progress, now time.Time) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	header := "ID\tS\tP\tTITLE\tPROJECT\tDUE\tTAGS"
	if len(claims) > 0 {
		header += "\tAGENT"
	}
	fmt.Fprintln(tw, header)

	for _, e := range entries {
		title := e.Title
		if p, ok := progress[e.ID]; ok && p.Any() {
			title = fmt.Sprintf("%s  [%s]", title, p)
		}

		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s",
			e.ID,
			waitingMark(e),
			priorityMark(e.Priority),
			title,
			e.Project,
			dueLabel(e.Due, now),
			strings.Join(e.Tags, ","),
		)
		if len(claims) > 0 {
			fmt.Fprintf(tw, "\t%s", claims[e.ID])
		}
		fmt.Fprintln(tw)
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

// renderTaskTree draws the hierarchy with box-drawing guides, so a subtask is
// visibly a subtask rather than just another row that happens to be adjacent.
func renderTaskTree(w io.Writer, nodes []store.TreeNode, claims map[int]string, now time.Time) {
	if len(nodes) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	header := "ID\tS\tP\tTITLE\tPROJECT\tDUE\tTAGS"
	if len(claims) > 0 {
		header += "\tAGENT"
	}
	fmt.Fprintln(tw, header)

	for _, n := range nodes {
		e := n.Entry

		title := e.Title
		if n.Progress.Any() {
			title = fmt.Sprintf("%s  [%s]", title, n.Progress)
		}
		if n.Depth > 0 {
			branch := "├─ "
			if n.Last {
				branch = "└─ "
			}
			title = strings.Repeat("   ", n.Depth-1) + branch + title
		}

		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s",
			e.ID, waitingMark(e), priorityMark(e.Priority), title,
			e.Project, dueLabel(e.Due, now), strings.Join(e.Tags, ","))
		if len(claims) > 0 {
			fmt.Fprintf(tw, "\t%s", claims[e.ID])
		}
		fmt.Fprintln(tw)
	}
	_ = tw.Flush()
}
