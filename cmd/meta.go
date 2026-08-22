package cmd

import (
	"fmt"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "A summary of where things stand",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			now := time.Now()

			open, err := s.List(store.Filter{})
			if err != nil {
				return err
			}
			all, err := s.List(store.Filter{IncludeDone: true})
			if err != nil {
				return err
			}
			overdue, err := s.Overdue(now)
			if err != nil {
				return err
			}
			today, err := s.Today(now)
			if err != nil {
				return err
			}

			byStatus := map[store.Status]int{}
			for _, e := range all {
				byStatus[e.Status]++
			}

			summary := struct {
				Total    int                  `json:"total"`
				Open     int                  `json:"open"`
				Overdue  int                  `json:"overdue"`
				Today    int                  `json:"today"`
				ByStatus map[store.Status]int `json:"byStatus"`
				DataDir  string               `json:"dataDir"`
			}{
				Total:    len(all),
				Open:     len(open),
				Overdue:  len(overdue),
				Today:    len(today),
				ByStatus: byStatus,
				DataDir:  s.Root(),
			}

			if a.jsonOut {
				return a.emitJSON(summary)
			}

			a.printf("%d open of %d total\n", summary.Open, summary.Total)
			for _, st := range []store.Status{store.StatusTodo, store.StatusDoing, store.StatusBlocked, store.StatusDone} {
				if n := byStatus[st]; n > 0 {
					a.printf("  %-8s %d\n", st, n)
				}
			}
			if summary.Overdue > 0 {
				a.printf("\n%d overdue\n", summary.Overdue)
			}
			if summary.Today > 0 {
				a.printf("%d due today or earlier\n", summary.Today)
			}
			a.printf("\ndata  %s\n", summary.DataDir)
			return nil
		},
	}
}

func (a *app) newActivityCommand() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Who changed what, and when",
		Long: `Show the activity log.

Every entry records whether a human or an agent made the change. The log is
append-only and is never rebuilt, so it survives 'taskgo reindex'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			events, err := s.Activity(limit)
			if err != nil {
				return err
			}

			if a.jsonOut {
				if events == nil {
					events = []store.Event{}
				}
				return a.emitJSON(events)
			}

			if len(events) == 0 {
				a.printf("Nothing yet." + "\n")
				return nil
			}
			for _, e := range events {
				line := fmt.Sprintf("%s  %-5s  %-14s", e.Time.Local().Format("2006-01-02 15:04"), e.Actor, e.Action)
				if e.Task != 0 {
					line += fmt.Sprintf("  #%d", e.Task)
				}
				if e.Detail != "" {
					line += "  " + e.Detail
				}
				a.printf("%s\n", line)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "how many entries to show (0 for all)")
	return cmd
}

func (a *app) newProjectsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			projects, err := s.ListProjects()
			if err != nil {
				return err
			}

			if a.jsonOut {
				if projects == nil {
					projects = []store.ProjectSummary{}
				}
				return a.emitJSON(projects)
			}

			if len(projects) == 0 {
				a.printf("No projects. Create one with: taskgo projects new <name>" + "\n")
				return nil
			}
			for _, p := range projects {
				a.printf("%-20s %d open", p.Name, p.Open)
				if p.Done > 0 {
					a.printf(", %d done", p.Done)
				}
				if p.Description != "" {
					a.printf("  — %s", p.Description)
				}
				a.printf("\n")
			}
			return nil
		},
	}

	cmd.AddCommand(a.newProjectNewCommand())
	return cmd
}

func (a *app) newProjectNewCommand() *cobra.Command {
	var description string

	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			p, err := s.CreateProject(cliActor, args[0], description)
			if err != nil {
				return err
			}

			if a.jsonOut {
				return a.emitJSON(p)
			}
			a.printf("Created project %s\n", p.Name)
			return nil
		},
	}

	cmd.Flags().StringVarP(&description, "description", "d", "", "what the project is")
	return cmd
}

func (a *app) newReindexCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild state.json from the Markdown files",
		Long: `Rebuild the index.

The Markdown files are canonical; state.json is a derived cache for fast
listing. Run this after editing task files by hand, or if the index is ever
lost or corrupted.

The activity log is not touched: it records things that happened and cannot be
derived from current state.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			idx, err := s.Reindex()
			if err != nil {
				return err
			}

			if a.jsonOut {
				return a.emitJSON(idx)
			}
			a.printf("Reindexed %d tasks.\n", len(idx.Tasks))
			return nil
		},
	}
}
