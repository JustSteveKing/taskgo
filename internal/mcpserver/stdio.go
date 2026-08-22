package mcpserver

import (
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/JustSteveKing/taskgo/internal/agents"
	"github.com/JustSteveKing/taskgo/internal/claim"
	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run serves the store over stdio until the client disconnects or ctx is
// cancelled.
//
// It returns nil when the client simply went away. That case needs detecting
// because the SDK reports it as a JSON-RPC "server is closing" wire error with
// io.EOF only in the message text — the chain bottoms out in an internal
// jsonrpc2 type this package cannot import, so errors.Is against io.EOF does
// not match.
//
// Rather than string-matching an internal sentinel, we watch stdin ourselves
// and treat any shutdown that follows an observed EOF as the normal end of a
// session. Getting this wrong is not cosmetic: exiting non-zero every time an
// agent disconnects makes a clean shutdown look like a crash to the client.
func Run(ctx context.Context, s *store.Store, version string) error {
	stdin := &eofWatcher{r: os.Stdin}

	transport := &mcp.IOTransport{
		Reader: stdin,
		// stdout is the protocol channel and belongs to the process; the
		// transport must not close it out from under us.
		Writer: nopWriteCloser{os.Stdout},
	}

	srv, sess := New(s, version)
	err := srv.Run(ctx, transport)

	// The agent has gone. Drop every lease it held, which is what makes the
	// common case correct without asking agents to send heartbeats — the TTL
	// only has to cover the server being killed outright.
	now := time.Now()
	for _, id := range sess.known() {
		claim.ReleaseSession(s, id, now)
		agents.Unregister(s, id)
	}

	if err != nil && stdin.seen() {
		return nil
	}
	return err
}

// eofWatcher records whether the underlying reader has reported EOF.
type eofWatcher struct {
	r      io.Reader
	sawEOF atomic.Bool
}

func (e *eofWatcher) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		e.sawEOF.Store(true)
	}
	return n, err
}

// Close satisfies io.ReadCloser. Closing stdin is the transport's business,
// not ours, and the process is about to end anyway.
func (e *eofWatcher) Close() error { return nil }

func (e *eofWatcher) seen() bool { return e.sawEOF.Load() }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
