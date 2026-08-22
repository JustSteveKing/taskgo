// Package cmd holds the taskgo command line.
//
// Every command is a thin shell over internal/store: parse flags, call one
// store method, render. Business logic that lives here instead of in the store
// is logic the MCP server and the TUI cannot reach, so it does not live here.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/JustSteveKing/taskgo/internal/config"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

var (
	cfg     *config.Config
	dataDir string
	jsonOut bool
)

// openStore is used by every command that touches data.
func openStore() (*store.Store, error) {
	root := dataDir
	if root == "" {
		root = cfg.DataDir
	}
	return store.Open(root)
}

func NewRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "taskgo",
		Short: "Local-first tasks for humans and agents",
		Long: `taskgo keeps tasks as plain Markdown files that a human, a CLI, a TUI and
an MCP server all read and write.

Nothing is hidden in a database: every task is a file you can open, grep,
diff and edit by hand.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.Load()
			if err != nil {
				return err
			}
			cfg = loaded
			return nil
		},
	}

	root.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override the taskgo data directory")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")

	root.AddCommand(
		newAddCommand(),
		newListCommand(),
		newDoneCommand(),
		newReopenCommand(),
		newShowCommand(),
		newEditCommand(),
		newNoteCommand(),
		newStatusCommand(),
		newActivityCommand(),
		newProjectsCommand(),
		newReindexCommand(),
		newDeleteCommand(),
		newMCPCommand(version),
	)

	return root
}

func Execute(version string) {
	if err := NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "taskgo: "+err.Error())
		os.Exit(1)
	}
}

// emitJSON is the single place JSON output is produced, so every command's
// machine-readable form is shaped the same way.
func emitJSON(w *os.File, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
