// Package mcpserver exposes the taskgo store to AI agents over MCP.
//
// Every tool here is a thin wrapper over internal/store, and every mutation is
// attributed to store.ActorAgent. That attribution is the entire point of the
// separation: a human running `taskgo done 4` and an agent calling
// complete_task produce the same file on disk and distinguishable lines in the
// activity log.
//
// Tools take task ids, never titles. An agent that wants to act on "the login
// task" should call search_tasks or list_tasks first and use the id it gets
// back — resolving a fuzzy reference inside a tool call would mean guessing on
// the agent's behalf, and completing the wrong task is not recoverable from
// the agent's side.
package mcpserver

import (
	"context"
	"time"

	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const actor = store.ActorAgent

// New builds a server exposing the store. The returned server has not been
// connected to a transport yet.
func New(s *store.Store, version string) (*mcp.Server, *sessions) {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "taskgo",
		Title:   "taskgo",
		Version: version,
	}, &mcp.ServerOptions{
		// Sent at initialize, so an agent knows the workflow before its first
		// tool call rather than inferring one from tool descriptions.
		Instructions: instructions,
	})

	sess := newSessions()

	registerTaskTools(srv, s, sess)
	registerQueryTools(srv, s, sess)
	registerProjectTools(srv, s, sess)
	registerActivityTools(srv, s, sess)
	registerClaimTools(srv, s, sess)
	registerQuestionTools(srv, s, sess)
	registerSubtaskTools(srv, s, sess)

	return srv, sess
}

// ---------------------------------------------------------------- shared IO

// taskList is the shape every multi-task tool returns. Count is included
// because it saves the agent counting an array it may only partly read.
type taskRow struct {
	store.IndexEntry
	// Subtasks is present only when the task has children, so a parent's
	// state is visible without a second call.
	Subtasks string `json:"subtasks,omitempty"`
}

type taskList struct {
	Tasks []taskRow `json:"tasks"`
	Count int       `json:"count"`
}

func listResult(entries []store.IndexEntry) taskList {
	return taskList{Tasks: rows(entries, nil), Count: len(entries)}
}

func listResultWithProgress(entries []store.IndexEntry, progress map[int]store.Progress) taskList {
	return taskList{Tasks: rows(entries, progress), Count: len(entries)}
}

func rows(entries []store.IndexEntry, progress map[int]store.Progress) []taskRow {
	out := make([]taskRow, 0, len(entries))
	for _, e := range entries {
		row := taskRow{IndexEntry: e}
		if p, ok := progress[e.ID]; ok && p.Any() {
			row.Subtasks = p.String()
		}
		out = append(out, row)
	}
	return out
}

// ---------------------------------------------------------------- task tools

type createTaskIn struct {
	Title    string   `json:"title" jsonschema:"what the task is; required"`
	Notes    string   `json:"notes,omitempty" jsonschema:"free-form Markdown detail"`
	Status   string   `json:"status,omitempty" jsonschema:"todo, doing, blocked or done; defaults to todo"`
	Priority string   `json:"priority,omitempty" jsonschema:"low, normal, high or urgent; defaults to normal"`
	Due      string   `json:"due,omitempty" jsonschema:"due date as YYYY-MM-DD, or 'today' or 'tomorrow'"`
	Project  string   `json:"project,omitempty" jsonschema:"project name this belongs to"`
	Tags     []string `json:"tags,omitempty" jsonschema:"short labels"`
	Parent   int      `json:"parent,omitempty" jsonschema:"id of a parent task, making this a subtask"`
}

type updateTaskIn struct {
	ID       int       `json:"id" jsonschema:"id of the task to change; required"`
	Title    *string   `json:"title,omitempty" jsonschema:"new title"`
	Notes    *string   `json:"notes,omitempty" jsonschema:"REPLACES the notes body; use add_note to append instead"`
	Status   *string   `json:"status,omitempty" jsonschema:"todo, doing, blocked or done"`
	Priority *string   `json:"priority,omitempty" jsonschema:"low, normal, high or urgent"`
	Due      *string   `json:"due,omitempty" jsonschema:"new due date as YYYY-MM-DD, 'today' or 'tomorrow'"`
	ClearDue bool      `json:"clear_due,omitempty" jsonschema:"remove the due date entirely; overrides due"`
	Project  *string   `json:"project,omitempty" jsonschema:"new project name, or empty string to unset"`
	Tags     *[]string `json:"tags,omitempty" jsonschema:"REPLACES all tags"`
	Parent   *int      `json:"parent,omitempty" jsonschema:"new parent task id, or 0 to detach"`
}

type taskIDIn struct {
	ID int `json:"id" jsonschema:"id of the task; required"`
}

type addNoteIn struct {
	ID   int    `json:"id" jsonschema:"id of the task; required"`
	Note string `json:"note" jsonschema:"text to append; required"`
}

func registerTaskTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_task",
		Description: "Create a task. Returns the created task including its new id, " +
			"which you will need to update or complete it later.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createTaskIn) (*mcp.CallToolResult, *store.Task, error) {
		newTask := store.NewTask{
			Title:   in.Title,
			Notes:   in.Notes,
			Project: in.Project,
			Tags:    in.Tags,
			Parent:  in.Parent,
		}

		var err error
		if in.Status != "" {
			if newTask.Status, err = store.ParseStatus(in.Status); err != nil {
				return nil, nil, err
			}
		}
		if in.Priority != "" {
			if newTask.Priority, err = store.ParsePriority(in.Priority); err != nil {
				return nil, nil, err
			}
		}
		if newTask.Due, err = store.ParseDue(in.Due); err != nil {
			return nil, nil, err
		}

		task, err := s.Create(actor, newTask)
		if err != nil {
			return nil, nil, err
		}
		touch(s, sess, req, task.ID)
		return nil, task, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_task",
		Description: "Fetch one task in full, including its notes body.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, *store.Task, error) {
		seen(s, sess, req)
		task, err := s.Get(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, task, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_task",
		Description: "Change fields on an existing task. Omitted fields are left alone. " +
			"Note that tags and notes REPLACE what is there; use add_note to append a note.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateTaskIn) (*mcp.CallToolResult, *store.Task, error) {
		up := store.Update{
			Title:   in.Title,
			Notes:   in.Notes,
			Project: in.Project,
			Tags:    in.Tags,
			Parent:  in.Parent,
		}

		if in.Status != nil {
			parsed, err := store.ParseStatus(*in.Status)
			if err != nil {
				return nil, nil, err
			}
			up.Status = &parsed
		}
		if in.Priority != nil {
			parsed, err := store.ParsePriority(*in.Priority)
			if err != nil {
				return nil, nil, err
			}
			up.Priority = &parsed
		}

		// clear_due is a separate flag rather than `due: null` because an
		// agent emitting an explicit null is far less reliable than one
		// setting a boolean, and "unset the due date" needs to be expressible.
		switch {
		case in.ClearDue:
			var none *store.DueDate
			up.Due = &none
		case in.Due != nil:
			parsed, err := store.ParseDue(*in.Due)
			if err != nil {
				return nil, nil, err
			}
			up.Due = &parsed
		}

		task, err := s.Update(actor, in.ID, up)
		if err != nil {
			return nil, nil, err
		}
		touch(s, sess, req, task.ID)
		return nil, task, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "complete_task",
		Description: "Mark a task done.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, *store.Task, error) {
		task, err := s.Complete(actor, in.ID)
		if err != nil {
			return nil, nil, err
		}
		// The work is over, so the lease is too — whoever held it.
		claim.ReleaseTask(s, task.ID, time.Now())
		return nil, task, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reopen_task",
		Description: "Move a completed task back to todo.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in taskIDIn) (*mcp.CallToolResult, *store.Task, error) {
		task, err := s.Reopen(actor, in.ID)
		if err != nil {
			return nil, nil, err
		}
		touch(s, sess, req, task.ID)
		return nil, task, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "add_note",
		Description: "Append a note to a task's notes body, preserving what is already there. " +
			"Prefer this over update_task when recording progress.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addNoteIn) (*mcp.CallToolResult, *store.Task, error) {
		task, err := s.AddNote(actor, in.ID, in.Note)
		if err != nil {
			return nil, nil, err
		}
		touch(s, sess, req, task.ID)
		return nil, task, nil
	})
}

// --------------------------------------------------------------- query tools

type listTasksIn struct {
	Project     string `json:"project,omitempty" jsonschema:"only tasks in this project"`
	Status      string `json:"status,omitempty" jsonschema:"only this status: todo, doing, blocked or done"`
	Tag         string `json:"tag,omitempty" jsonschema:"only tasks carrying this tag"`
	IncludeDone bool   `json:"include_done,omitempty" jsonschema:"include completed tasks; they are hidden by default"`
	Parent      *int   `json:"parent,omitempty" jsonschema:"only subtasks of this task id; pass 0 for top-level tasks only"`
}

type searchIn struct {
	Query string `json:"query" jsonschema:"text to look for in titles and notes; required"`
}

func registerQueryTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_tasks",
		Description: "List tasks, newest commitments first. Completed tasks are omitted " +
			"unless include_done is true. Returns index entries without notes bodies; " +
			"call get_task for the full text of one task.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTasksIn) (*mcp.CallToolResult, taskList, error) {
		seen(s, sess, req)
		f := store.Filter{
			Project:     in.Project,
			Tag:         in.Tag,
			IncludeDone: in.IncludeDone,
			Parent:      in.Parent,
		}
		if in.Status != "" {
			parsed, err := store.ParseStatus(in.Status)
			if err != nil {
				return nil, taskList{}, err
			}
			f.Status = parsed
		}

		entries, err := s.List(f)
		if err != nil {
			return nil, taskList{}, err
		}
		progress, _ := s.ProgressFor()
		return nil, listResultWithProgress(entries, progress), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_tasks",
		Description: "Full-text search across task titles and notes bodies.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, taskList, error) {
		seen(s, sess, req)
		entries, err := s.Search(in.Query)
		if err != nil {
			return nil, taskList{}, err
		}
		return nil, listResult(entries), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_overdue",
		Description: "Unfinished tasks whose due date has passed. Completed tasks are never overdue.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, taskList, error) {
		seen(s, sess, req)
		entries, err := s.Overdue(time.Now())
		if err != nil {
			return nil, taskList{}, err
		}
		return nil, listResult(entries), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_today",
		Description: "What is on today: unfinished tasks due today, plus anything already overdue. " +
			"This is the tool to call when asked what needs doing now.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, taskList, error) {
		seen(s, sess, req)
		entries, err := s.Today(time.Now())
		if err != nil {
			return nil, taskList{}, err
		}
		return nil, listResult(entries), nil
	})
}

// ------------------------------------------------------------- project tools

type createProjectIn struct {
	Name        string `json:"name" jsonschema:"project name: letters, digits, dot, dash or underscore; required"`
	Description string `json:"description,omitempty" jsonschema:"what the project is for"`
}

type projectList struct {
	Projects []store.ProjectSummary `json:"projects"`
	Count    int                    `json:"count"`
}

func registerProjectTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_projects",
		Description: "List projects with their open and completed task counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, projectList, error) {
		seen(s, sess, req)
		projects, err := s.ListProjects()
		if err != nil {
			return nil, projectList{}, err
		}
		if projects == nil {
			projects = []store.ProjectSummary{}
		}
		return nil, projectList{Projects: projects, Count: len(projects)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_project",
		Description: "Create a project. Tasks reference projects by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createProjectIn) (*mcp.CallToolResult, *store.Project, error) {
		seen(s, sess, req)
		p, err := s.CreateProject(actor, in.Name, in.Description)
		if err != nil {
			return nil, nil, err
		}
		return nil, p, nil
	})
}

// ------------------------------------------------------------ activity tools

type activityIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many entries to return, newest first; 0 means all"`
}

type activityList struct {
	Events []store.Event `json:"events"`
	Count  int           `json:"count"`
}

func registerActivityTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_activity",
		Description: "Recent changes, newest first. Each entry records whether a human or " +
			"an agent made the change, which is how you tell what the user did " +
			"since you last looked.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in activityIn) (*mcp.CallToolResult, activityList, error) {
		seen(s, sess, req)
		limit := in.Limit
		if limit == 0 {
			// An agent asking for "recent activity" wants a readable window,
			// not the entire history of the store.
			limit = 50
		}
		if limit < 0 {
			limit = 0
		}

		events, err := s.Activity(limit)
		if err != nil {
			return nil, activityList{}, err
		}
		if events == nil {
			events = []store.Event{}
		}
		return nil, activityList{Events: events, Count: len(events)}, nil
	})
}
