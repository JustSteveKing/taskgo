package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JustSteveKing/taskgo/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server on stdio",
		Long: `Run the MCP server so an AI agent can use taskgo as persistent memory.

The server speaks MCP over stdin/stdout, so it is started by the agent's
client rather than run by hand. Register it with:

    claude mcp add taskgo -- taskgo mcp

Every change an agent makes is attributed to "agent" in the activity log, and
lands in the same Markdown files the CLI reads — so a task an agent creates
shows up in 'taskgo list' immediately.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}

			// stdout belongs to the protocol. Anything written there that is
			// not a JSON-RPC frame corrupts the session, so diagnostics go to
			// stderr and nothing here prints a banner.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			fmt.Fprintf(os.Stderr, "taskgo mcp: serving %s\n", s.Root())

			// mcpserver.Run already returns nil when the client simply
			// disconnected; anything left is a real failure.
			if err := mcpserver.Run(ctx, s, version); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		},
	}
}
