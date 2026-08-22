package cmd

import (
	"github.com/JustSteveKing/taskgo/internal/tui"
	"github.com/spf13/cobra"
)

func newTUICommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Browse and update tasks interactively",
		Long: `Open the interactive interface.

It re-reads the store every couple of seconds while idle, so a task an agent
creates over MCP appears without you doing anything.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			return tui.Run(s)
		},
	}
}
