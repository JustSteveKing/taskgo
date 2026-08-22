// Package cmd holds the taskgo command line.
//
// Every command is a thin shell over internal/store: parse flags, call one
// store method, render. Business logic that lives here instead of in the store
// is logic the MCP server and the TUI cannot reach, so it does not live here.
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/JustSteveKing/taskgo/internal/config"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

// app carries the state a command tree needs.
//
// This is a struct rather than package-level variables so that each
// NewRootCommand gets its own: package-level flag targets are shared mutable
// state, which makes tests order-dependent and would leak one test's
// --data-dir into the next.
type app struct {
	cfg     *config.Config
	dataDir string
	jsonOut bool

	out io.Writer
	err io.Writer
}

func (a *app) openStore() (*store.Store, error) {
	root := a.dataDir
	if root == "" {
		if a.cfg == nil {
			return nil, fmt.Errorf("configuration was not loaded")
		}
		root = a.cfg.DataDir
	}
	return store.Open(root)
}

// emitJSON is the single place JSON output is produced, so every command's
// machine-readable form is shaped the same way.
func (a *app) emitJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.out, format, args...)
}

func NewRootCommand(version string) *cobra.Command {
	a := &app{out: os.Stdout, err: os.Stderr}

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
			// Track cobra's streams so tests can capture output by setting
			// SetOut/SetErr on the root command.
			a.out = cmd.OutOrStdout()
			a.err = cmd.ErrOrStderr()

			loaded, err := config.Load()
			if err != nil {
				return err
			}
			a.cfg = loaded
			return nil
		},
	}

	root.PersistentFlags().StringVar(&a.dataDir, "data-dir", "", "override the taskgo data directory")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "emit JSON instead of text")

	root.AddCommand(
		a.newAddCommand(),
		a.newListCommand(),
		a.newDoneCommand(),
		a.newReopenCommand(),
		a.newShowCommand(),
		a.newEditCommand(),
		a.newNoteCommand(),
		a.newStatusCommand(),
		a.newActivityCommand(),
		a.newProjectsCommand(),
		a.newReindexCommand(),
		a.newDeleteCommand(),
		a.newNotifyCommand(),
		a.newClaimsCommand(),
		a.newQuestionsCommand(),
		a.newAnswerCommand(),
		a.newMCPCommand(version),
		a.newTUICommand(version),
	)

	a.registerCompletions(root)
	return root
}

func Execute(version string) {
	if err := NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "taskgo: "+err.Error())
		os.Exit(1)
	}
}
