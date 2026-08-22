package cmd

import (
	"strings"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

func (a *app) newQuestionsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "questions",
		Aliases: []string{"asks"},
		Short:   "Tasks where an agent is waiting on you",
		Long: `Show questions agents have asked and stopped on.

An agent that hits a decision it cannot make asks rather than guessing, and
waits. Until you answer, that work is not moving.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			waiting, err := s.Waiting()
			if err != nil {
				return err
			}

			if a.jsonOut {
				if waiting == nil {
					waiting = []store.IndexEntry{}
				}
				return a.emitJSON(waiting)
			}

			if len(waiting) == 0 {
				a.printf("Nothing is waiting on you.\n")
				return nil
			}
			for _, e := range waiting {
				asker := e.AskedBy
				if asker == "" {
					asker = "an agent"
				}
				a.printf("#%d  %s\n", e.ID, e.Title)
				a.printf("    %s asks: %s\n", asker, e.Question)
				a.printf("    answer with: taskgo answer %d \"...\"\n\n", e.ID)
			}
			return nil
		},
	}
}

func (a *app) newAnswerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "answer <id|title> <text>...",
		Short: "Answer an agent's question",
		Long: `Answer a question an agent asked, unblocking it.

The exchange is appended to the task's notes, so the Markdown keeps the whole
conversation rather than just the outcome.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.openStore()
			if err != nil {
				return err
			}
			id, err := resolveRef(s, args[0])
			if err != nil {
				return err
			}

			task, err := s.Answer(cliActor, id, strings.Join(args[1:], " "))
			if err != nil {
				return err
			}

			if a.jsonOut {
				return a.emitJSON(task)
			}
			a.printf("Answered #%d. The agent will pick it up on its next check.\n", task.ID)
			return nil
		},
	}
}
