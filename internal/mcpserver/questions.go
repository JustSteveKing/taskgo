package mcpserver

import (
	"context"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type askIn struct {
	ID       int    `json:"id" jsonschema:"id of the task you are stuck on; required"`
	Question string `json:"question" jsonschema:"what you need the human to decide, asked plainly and in full; required"`
}

type askOut struct {
	Task     int    `json:"task"`
	Question string `json:"question"`
	Note     string `json:"note"`
}

type answerOut struct {
	Task     int    `json:"task"`
	Answered bool   `json:"answered"`
	Answer   string `json:"answer,omitempty"`
	Question string `json:"question,omitempty"`
	Note     string `json:"note"`
}

func registerQuestionTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "ask_human",
		Description: "Ask the human a question about a task you cannot decide yourself, " +
			"instead of guessing. The task is flagged as waiting on them and shown " +
			"prominently. This returns immediately — it does not block until they reply. " +
			"Poll check_answer (or get_task) to see whether they have responded, and get " +
			"on with other work in the meantime.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in askIn) (*mcp.CallToolResult, askOut, error) {
		_, agent := sess.identify(req.Session)

		task, err := s.Ask(actor, in.ID, agent, in.Question)
		if err != nil {
			return nil, askOut{}, err
		}

		// Asking is working on it: hold the lease so the human can see who is
		// waiting, and so the task does not look abandoned while it waits.
		touch(s, sess, req, task.ID)

		return nil, askOut{
			Task: task.ID, Question: task.Question,
			Note: "Waiting on the human. This did not block; poll check_answer and do other work meanwhile.",
		}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "check_answer",
		Description: "See whether the human has answered your question on a task. " +
			"Returns answered=false while it is still pending.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, answerOut, error) {
		task, err := s.Get(in.ID)
		if err != nil {
			return nil, answerOut{}, err
		}

		if task.AwaitingAnswer() {
			return nil, answerOut{
				Task: task.ID, Answered: false, Question: task.Question,
				Note: "Still waiting. Do not guess — keep polling or work on something else.",
			}, nil
		}
		if task.Answer == "" {
			return nil, answerOut{
				Task: task.ID, Answered: false,
				Note: "No question is pending on this task.",
			}, nil
		}
		return nil, answerOut{
			Task: task.ID, Answered: true, Answer: task.Answer,
			Note: "Answered. Carry on.",
		}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_questions",
		Description: "Every task currently waiting on a human answer, including questions " +
			"asked by other agents. Worth checking before asking a question someone " +
			"else has already put to them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, questionList, error) {
		entries, err := s.Waiting()
		if err != nil {
			return nil, questionList{}, err
		}

		out := questionList{Questions: []questionView{}}
		for _, e := range entries {
			out.Questions = append(out.Questions, questionView{
				Task: e.ID, Title: e.Title, Question: e.Question, AskedBy: e.AskedBy,
			})
		}
		out.Count = len(out.Questions)
		return nil, out, nil
	})
}

type questionView struct {
	Task     int    `json:"task"`
	Title    string `json:"title"`
	Question string `json:"question"`
	AskedBy  string `json:"askedBy,omitempty"`
}

type questionList struct {
	Questions []questionView `json:"questions"`
	Count     int            `json:"count"`
}

// answering from the agent side is deliberately absent: an agent answering its
// own question would defeat the point of asking.
