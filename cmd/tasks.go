package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

// Every CLI mutation is attributed to a human. The MCP server passes
// store.ActorAgent instead; that difference is the whole audit trail.
const cliActor = store.ActorHuman

func newAddCommand() *cobra.Command {
	var (
		notes    string
		status   string
		priority string
		due      string
		project  string
		tags     []string
		parent   int
	)

	cmd := &cobra.Command{
		Use:   "add <title>...",
		Short: "Add a task",
		Long: `Add a task.

The title can be given as bare words, so quoting is optional:

  taskgo add Fix the login redirect --due tomorrow --tag auth`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}

			in := store.NewTask{
				Title:   strings.Join(args, " "),
				Notes:   notes,
				Project: project,
				Tags:    tags,
				Parent:  parent,
			}

			if project == "" {
				in.Project = cfg.DefaultProject
			}
			if status != "" {
				if in.Status, err = store.ParseStatus(status); err != nil {
					return err
				}
			}
			if priority != "" {
				if in.Priority, err = store.ParsePriority(priority); err != nil {
					return err
				}
			}
			if in.Due, err = store.ParseDue(due); err != nil {
				return err
			}

			task, err := s.Create(cliActor, in)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			fmt.Printf("Added #%d  %s\n", task.ID, task.Title)
			return nil
		},
	}

	cmd.Flags().StringVarP(&notes, "notes", "n", "", "notes body")
	cmd.Flags().StringVarP(&status, "status", "s", "", "todo|doing|blocked|done")
	cmd.Flags().StringVarP(&priority, "priority", "p", "", "low|normal|high|urgent")
	cmd.Flags().StringVarP(&due, "due", "d", "", "YYYY-MM-DD, 'today' or 'tomorrow'")
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "tag (repeatable)")
	cmd.Flags().IntVar(&parent, "parent", 0, "parent task id, making this a subtask")

	return cmd
}

func newListCommand() *cobra.Command {
	var (
		project  string
		status   string
		tag      string
		due      string
		all      bool
		overdue  bool
		today    bool
		search   string
		parentID int
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks",
		Long: `List tasks.

Completed tasks are hidden unless you pass --all or --status done: the common
question is "what is left", and having to filter that every time would be a
poor default.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			now := time.Now()

			var entries []store.IndexEntry
			switch {
			case search != "":
				entries, err = s.Search(search)
			case overdue:
				entries, err = s.Overdue(now)
			case today:
				entries, err = s.Today(now)
			default:
				f := store.Filter{Project: project, Tag: tag, IncludeDone: all}
				if status != "" {
					if f.Status, err = store.ParseStatus(status); err != nil {
						return err
					}
				}
				if due != "" {
					if f.DueOn, err = store.ParseDue(due); err != nil {
						return err
					}
				}
				if cmd.Flags().Changed("parent") {
					f.Parent = &parentID
				}
				entries, err = s.List(f)
			}
			if err != nil {
				return err
			}

			if jsonOut {
				if entries == nil {
					entries = []store.IndexEntry{}
				}
				return emitJSON(os.Stdout, entries)
			}
			renderTaskTable(os.Stdout, entries, now)
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "only this project")
	cmd.Flags().StringVarP(&status, "status", "s", "", "only this status")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "only this tag")
	cmd.Flags().StringVarP(&due, "due", "d", "", "only tasks due on this date")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include completed tasks")
	cmd.Flags().BoolVar(&overdue, "overdue", false, "only overdue tasks")
	cmd.Flags().BoolVar(&today, "today", false, "due today, plus anything already overdue")
	cmd.Flags().StringVar(&search, "search", "", "full-text search across titles and notes")
	cmd.Flags().IntVar(&parentID, "parent", 0, "only subtasks of this task (0 for top-level)")

	return cmd
}

// resolveRef turns a CLI argument into a task id via the store's resolver,
// which accepts an exact id or a unique title substring.
func resolveRef(s *store.Store, ref string) (int, error) {
	return s.Resolve(ref)
}

func newDoneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id|title>",
		Short: "Mark a task complete",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}
			task, err := s.Complete(cliActor, id)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			fmt.Printf("Done #%d  %s\n", task.ID, task.Title)
			return nil
		},
	}
}

func newReopenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id|title>",
		Short: "Move a completed task back to todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}
			task, err := s.Reopen(cliActor, id)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			fmt.Printf("Reopened #%d  %s\n", task.ID, task.Title)
			return nil
		},
	}
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id|title>",
		Short: "Show one task in full, notes included",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}
			task, err := s.Get(id)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			renderTask(os.Stdout, task, time.Now())
			return nil
		},
	}
}

func newEditCommand() *cobra.Command {
	var (
		title    string
		status   string
		priority string
		due      string
		project  string
		tags     []string
		parent   int
		clearDue bool
	)

	cmd := &cobra.Command{
		Use:   "edit <id|title>",
		Short: "Change a task's fields, or open it in $EDITOR",
		Long: `Change a task.

With no flags this opens the task's Markdown file in your editor, which is the
honest thing to do given the file is the real record. With flags it applies
just those changes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}

			flags := cmd.Flags()
			anyFlag := flags.Changed("title") || flags.Changed("status") ||
				flags.Changed("priority") || flags.Changed("due") ||
				flags.Changed("project") || flags.Changed("tag") ||
				flags.Changed("parent") || clearDue

			if !anyFlag {
				return editInEditor(s, id)
			}

			var up store.Update
			if flags.Changed("title") {
				up.Title = &title
			}
			if flags.Changed("status") {
				parsed, err := store.ParseStatus(status)
				if err != nil {
					return err
				}
				up.Status = &parsed
			}
			if flags.Changed("priority") {
				parsed, err := store.ParsePriority(priority)
				if err != nil {
					return err
				}
				up.Priority = &parsed
			}
			if clearDue {
				var none *store.DueDate
				up.Due = &none
			} else if flags.Changed("due") {
				parsed, err := store.ParseDue(due)
				if err != nil {
					return err
				}
				up.Due = &parsed
			}
			if flags.Changed("project") {
				up.Project = &project
			}
			if flags.Changed("tag") {
				up.Tags = &tags
			}
			if flags.Changed("parent") {
				up.Parent = &parent
			}

			task, err := s.Update(cliActor, id, up)
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			fmt.Printf("Updated #%d  %s\n", task.ID, task.Title)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVarP(&status, "status", "s", "", "todo|doing|blocked|done")
	cmd.Flags().StringVarP(&priority, "priority", "p", "", "low|normal|high|urgent")
	cmd.Flags().StringVarP(&due, "due", "d", "", "YYYY-MM-DD, 'today' or 'tomorrow'")
	cmd.Flags().BoolVar(&clearDue, "clear-due", false, "remove the due date")
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "replace tags")
	cmd.Flags().IntVar(&parent, "parent", 0, "parent task id (0 to detach)")

	return cmd
}

// editInEditor opens the task file itself. After the editor exits the store is
// reindexed, because the user may have changed indexed fields by hand — which
// is exactly the workflow the plain-file design is meant to allow.
func editInEditor(s *store.Store, id int) error {
	path := filepath.Join(s.Root(), "tasks", fmt.Sprintf("%d.md", id))

	editor := cfg.EditorCommand()
	c := exec.Command("sh", "-c", editor+" "+shellQuote(path))
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", editor, err)
	}

	if _, err := s.Reindex(); err != nil {
		return fmt.Errorf("reindex after edit: %w", err)
	}
	fmt.Printf("Saved #%d\n", id)
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newNoteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "note <id|title> <text>...",
		Short: "Append a note to a task",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}
			task, err := s.AddNote(cliActor, id, strings.Join(args[1:], " "))
			if err != nil {
				return err
			}

			if jsonOut {
				return emitJSON(os.Stdout, task)
			}
			fmt.Printf("Noted on #%d\n", task.ID)
			return nil
		},
	}
}

func newDeleteCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <id|title>",
		Aliases: []string{"rm"},
		Short:   "Delete a task",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}
			task, err := s.Get(id)
			if err != nil {
				return err
			}

			// Deleting is the one destructive operation here, and unlike
			// completing it cannot be undone from the CLI.
			if !force {
				fmt.Printf("Delete #%d %q? This cannot be undone. [y/N] ", task.ID, task.Title)
				var answer string
				_, _ = fmt.Scanln(&answer)
				if !strings.EqualFold(strings.TrimSpace(answer), "y") {
					fmt.Println("Left alone.")
					return nil
				}
			}

			if err := s.Delete(cliActor, id); err != nil {
				return err
			}
			fmt.Printf("Deleted #%d\n", id)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip the confirmation")
	return cmd
}
