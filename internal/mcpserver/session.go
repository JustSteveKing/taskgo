package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessions assigns a stable id to each connected client and remembers who it
// said it was.
//
// The SDK's ServerSession.ID() is empty on stdio (it carries a value only for
// the HTTP transports), so the id is generated here. Keying off the session
// pointer means this stays correct if taskgo ever serves more than one client
// at a time, rather than assuming the one-process-one-session shape stdio
// happens to have.
type sessions struct {
	mu   sync.Mutex
	ids  map[*mcp.ServerSession]string
	name map[*mcp.ServerSession]string
}

func newSessions() *sessions {
	return &sessions{
		ids:  map[*mcp.ServerSession]string{},
		name: map[*mcp.ServerSession]string{},
	}
}

// identify returns the session id and the agent's name.
//
// The name comes from the MCP handshake rather than a tool argument, so it
// reflects what actually connected instead of what a caller chose to type.
func (s *sessions) identify(ss *mcp.ServerSession) (id, agent string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.ids[ss]; ok {
		return existing, s.name[ss]
	}

	id = randomID()
	agent = "agent"
	if ss != nil {
		if params := ss.InitializeParams(); params != nil && params.ClientInfo != nil {
			if params.ClientInfo.Title != "" {
				agent = params.ClientInfo.Title
			} else if params.ClientInfo.Name != "" {
				agent = params.ClientInfo.Name
			}
		}
	}

	s.ids[ss] = id
	s.name[ss] = agent
	return id, agent
}

// known returns every session id handed out, for releasing claims on shutdown.
func (s *sessions) known() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.ids))
	for _, id := range s.ids {
		out = append(out, id)
	}
	return out
}

func randomID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// An id that collides costs a mis-attributed claim, not data loss, so
		// a degraded fallback beats refusing to serve.
		return "session"
	}
	return hex.EncodeToString(b[:])
}
