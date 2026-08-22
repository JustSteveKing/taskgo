package cmd

import (
	"time"

	"github.com/JustSteveKing/taskgo/internal/agents"
	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/spf13/cobra"
)

func (a *app) newAgentsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "AI agents currently connected",
		Long: `Show which agents are connected to taskgo right now.

This is a different question from 'taskgo claims'. A claim says an agent is
working on a particular task; a session says the agent is here at all. An agent
that has connected and is reading or planning holds nothing, and would be
invisible if the roster were derived from claims alone.

Entries are cleared when an agent disconnects. A server killed outright never
gets to clean up, so each entry carries its process id and is dropped once that
process is gone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			now := time.Now()

			connected, err := agents.List(s)
			if err != nil {
				return err
			}

			if a.jsonOut {
				if connected == nil {
					connected = []agents.Session{}
				}
				return a.emitJSON(connected)
			}

			if len(connected) == 0 {
				a.printf("No agents connected.\n")
				return nil
			}

			held, _ := claim.Load(s, now)
			for _, sess := range connected {
				var mine []int
				for id, c := range held {
					if c.Session == sess.ID {
						mine = append(mine, id)
					}
				}

				a.printf("%-20s connected %s ago", sess.Name, shortDuration(now.Sub(sess.Connected)))
				if len(mine) == 0 {
					a.printf(", idle %s\n", shortDuration(sess.Idle(now)))
					continue
				}
				a.printf(", holding %d task(s):", len(mine))
				for _, id := range mine {
					a.printf(" #%d", id)
				}
				a.printf("\n")
			}
			return nil
		},
	}
}
