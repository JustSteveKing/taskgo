package mcpserver

import (
	"context"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type breakDownIn struct {
	ID     int      `json:"id" jsonschema:"id of the task to break up; required"`
	Titles []string `json:"titles" jsonschema:"one title per piece of work, in the order they should happen; required"`
}

type breakDownOut struct {
	Parent   int          `json:"parent"`
	Created  []store.Task `json:"created"`
	Subtasks string       `json:"subtasks"`
}

func registerSubtaskTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "break_down_task",
		Description: "Split a task into subtasks in one call, rather than creating each " +
			"separately and setting parent by hand. Use this when a task turns out to be " +
			"several pieces of work — the human then sees progress against the original " +
			"instead of one task that is 'in progress' for two days.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in breakDownIn) (*mcp.CallToolResult, breakDownOut, error) {
		parent, err := s.Get(in.ID)
		if err != nil {
			return nil, breakDownOut{}, err
		}

		out := breakDownOut{Parent: parent.ID, Created: []store.Task{}}
		for _, title := range in.Titles {
			// Subtasks inherit the parent's project, because a piece of a
			// task belongs to whatever the task belonged to.
			child, err := s.Create(actor, store.NewTask{
				Title: title, Parent: parent.ID, Project: parent.Project,
			})
			if err != nil {
				// Partial failure is reported with what did get created, so
				// the agent can see where it got to rather than guessing.
				return nil, out, err
			}
			out.Created = append(out.Created, *child)
		}

		touch(s, sess, req, parent.ID)

		if progress, err := s.ProgressFor(); err == nil {
			out.Subtasks = progress[parent.ID].String()
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_subtasks",
		Description: "The direct subtasks of a task, with their statuses. Check this before " +
			"completing a parent: finishing it while its pieces are still open is " +
			"usually a mistake.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, subtaskList, error) {
		seen(s, sess, req)

		if _, err := s.Get(in.ID); err != nil {
			return nil, subtaskList{}, err
		}
		children, err := s.Children(in.ID)
		if err != nil {
			return nil, subtaskList{}, err
		}

		progress, _ := s.ProgressFor()
		return nil, subtaskList{
			Parent:   in.ID,
			Subtasks: rows(children, progress),
			Count:    len(children),
			Progress: progress[in.ID].String(),
		}, nil
	})
}

type subtaskList struct {
	Parent   int       `json:"parent"`
	Subtasks []taskRow `json:"subtasks"`
	Count    int       `json:"count"`
	Progress string    `json:"progress"`
}
