package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// touch records that this agent is working on a task, after a write that
// already succeeded.
//
// It cannot fail the call: presence is a display concern, and turning a
// completed write into an error because a lease could not be recorded would
// trade something that matters for something that does not.
func touch(s *store.Store, sess *sessions, req *mcp.CallToolRequest, taskID int) {
	if req == nil {
		return
	}
	id, agent := sess.identify(req.Session)
	claim.Touch(s, taskID, agent, id, time.Now())
}

type claimIn struct {
	ID int `json:"id" jsonschema:"id of the task you are starting work on; required"`
}

type claimOut struct {
	Task    int    `json:"task"`
	By      string `json:"by"`
	Expires string `json:"expires"`
	Note    string `json:"note"`
}

func registerClaimTools(srv *mcp.Server, s *store.Store, sess *sessions) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "claim_task",
		Description: "Announce that you are starting work on a task, so the human sees it " +
			"marked as being worked on rather than merely touched. The claim is a lease: " +
			"it is released automatically when you complete the task or your session ends, " +
			"and expires on its own if neither happens. Call release_task if you stop early.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, claimOut, error) {
		if _, err := s.Get(in.ID); err != nil {
			return nil, claimOut{}, err
		}

		id, agent := sess.identify(req.Session)
		c, err := claim.Take(s, in.ID, agent, id, claim.ExplicitTTL, true, time.Now())
		if err != nil {
			return nil, claimOut{}, err
		}

		return nil, claimOut{
			Task: c.TaskID, By: c.By, Expires: c.Expires.UTC().Format(time.RFC3339),
			Note: "Released on complete_task, release_task, or when this session ends.",
		}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "release_task",
		Description: "Give up a task you claimed without completing it, so it stops showing " +
			"as being worked on.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, claimOut, error) {
		id, agent := sess.identify(req.Session)
		if err := claim.Release(s, in.ID, id, time.Now()); err != nil {
			return nil, claimOut{}, err
		}
		return nil, claimOut{Task: in.ID, By: agent, Note: "released"}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_claims",
		Description: "Tasks currently being worked on by an agent, including you. Useful " +
			"before starting work, to avoid duplicating what another agent is already doing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, claimList, error) {
		now := time.Now()
		set, err := claim.Load(s, now)
		if err != nil {
			return nil, claimList{}, err
		}

		out := claimList{Claims: []claimView{}}
		for _, c := range set.Sorted() {
			out.Claims = append(out.Claims, claimView{
				Task: c.TaskID, By: c.By, Explicit: c.Explicit,
				HeldFor: fmt.Sprintf("%dm", int(c.Held(now).Minutes())),
			})
		}
		out.Count = len(out.Claims)
		return nil, out, nil
	})
}

type claimView struct {
	Task     int    `json:"task"`
	By       string `json:"by"`
	HeldFor  string `json:"heldFor"`
	Explicit bool   `json:"explicit"`
}

type claimList struct {
	Claims []claimView `json:"claims"`
	Count  int         `json:"count"`
}
