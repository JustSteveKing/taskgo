package cmd

import (
	"time"

	"github.com/JustSteveKing/taskgo/internal/history"
	"github.com/spf13/cobra"
)

func (a *app) newHistoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Git-backed history of every change",
		Long: `Keep the data directory under Git, so every change is revertible.

The activity log records what happened but cannot undo any of it, which is a
thin guarantee for a system an agent can write to unattended. Since the storage
is already plain text designed to diff, Git turns it into a complete,
revertible history for almost nothing.

Opt-in: run 'taskgo history init' once. Committing is best-effort afterwards —
a Git failure never turns a successful task write into an error.

Runtime state (.lock, claims.json, sessions.json, notified.json) is ignored.
It is not history.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.showLog(20)
		},
	}

	cmd.AddCommand(
		a.newHistoryInitCommand(),
		a.newHistoryLogCommand(),
		a.newHistorySaveCommand(),
		a.newUndoCommand("undo"),
	)
	return cmd
}

func (a *app) showLog(limit int) error {
	s, err := a.openStore()
	if err != nil {
		return err
	}
	entries, err := history.Log(s, limit)
	if err != nil {
		return err
	}

	if a.jsonOut {
		if entries == nil {
			entries = []history.Entry{}
		}
		return a.emitJSON(entries)
	}
	if len(entries) == 0 {
		a.printf("No history yet.\n")
		return nil
	}
	for _, e := range entries {
		a.printf("%s  %s  %s\n", e.Short, e.When.Local().Format("2006-01-02 15:04"), e.Message)
	}
	return nil
}

func (a *app) newHistoryInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Start tracking the data directory in Git",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			if err := history.Init(s); err != nil {
				return err
			}
			a.printf("History enabled in %s\n", s.Root())
			a.printf("Every change is now committed. Undo the last one with: taskgo undo\n")
			return nil
		},
	}
}

func (a *app) newHistoryLogCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Recent changes, newest first",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return a.showLog(limit) },
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "how many to show")
	return cmd
}

// newHistorySaveCommand commits whatever is currently uncommitted — hand edits
// to task files, mostly, which arrive without going through the store and so
// never fire the change hook.
func (a *app) newHistorySaveCommand() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Commit changes made outside taskgo (hand edits)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			if message == "" {
				message = "human: hand edit " + time.Now().Format("2006-01-02 15:04")
			}
			if err := history.Commit(s, message); err != nil {
				return err
			}
			a.printf("Saved.\n")
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message")
	return cmd
}

func (a *app) newUndoCommand(use string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: "Undo the last change",
		Long: `Revert the most recent change.

The reversal is recorded as a new commit rather than rewriting history: "this
was undone" is itself worth keeping, and an agent's mistake together with its
correction tells you more than the mistake never having appeared.

Requires history to be enabled.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			undone, err := history.Undo(s)
			if err != nil {
				return err
			}
			a.printf("Undid %s  %s\n", undone.Short, undone.Message)
			return nil
		},
	}
}
