package cmd

import (
	"fmt"
	"time"

	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/spf13/cobra"
)

func (a *app) newClaimsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "claims",
		Short: "Tasks an agent is currently working on",
		Long: `Show which tasks an AI agent is actively working on.

This is not the same as "an agent touched it", which 'taskgo activity' already
shows. A claim is a lease an agent holds while it works: released when it
completes the task or its session ends, and expired automatically if neither
happens.

A claim marked (implicit) was taken automatically when the agent wrote to the
task rather than announced deliberately, so it is weaker evidence that work is
still in progress.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			now := time.Now()

			set, err := claim.Load(s, now)
			if err != nil {
				return err
			}
			held := set.Sorted()

			if a.jsonOut {
				if held == nil {
					held = []claim.Claim{}
				}
				return a.emitJSON(held)
			}

			if len(held) == 0 {
				a.printf("No agent is working on anything.\n")
				return nil
			}

			for _, c := range held {
				title := ""
				if task, err := s.Get(c.TaskID); err == nil {
					title = task.Title
				}
				kind := ""
				if !c.Explicit {
					kind = " (implicit)"
				}
				a.printf("#%-4d %-40s %s  held %s%s\n",
					c.TaskID, title, c.By, shortDuration(now.Sub(c.Since)), kind)
			}
			return nil
		},
	}
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
