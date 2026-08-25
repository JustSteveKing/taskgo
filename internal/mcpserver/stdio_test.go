package mcpserver

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"
)

// The EOF watcher is what stops a clean disconnect from looking like a crash:
// the SDK reports a client going away as a wire error, and exiting non-zero
// every time an agent disconnects would make every normal shutdown look like a
// failure to whatever started the server.
func TestEOFWatcherNoticesTheClientGoingAway(t *testing.T) {
	w := &eofWatcher{r: strings.NewReader("some input")}

	if w.seen() {
		t.Error("EOF reported before anything was read")
	}

	buf := make([]byte, 4)
	if _, err := w.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if w.seen() {
		t.Error("EOF reported after a successful read")
	}

	// Drain to the end.
	if _, err := io.ReadAll(w); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !w.seen() {
		t.Error("EOF was not noticed")
	}
}

func TestEOFWatcherOnAnEmptyStream(t *testing.T) {
	w := &eofWatcher{r: bytes.NewReader(nil)}

	if _, err := w.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF", err)
	}
	if !w.seen() {
		t.Error("EOF on the first read was not noticed")
	}
}

// A real failure is not a disconnect, and must not be laundered into a clean
// exit.
func TestEOFWatcherIgnoresOtherErrors(t *testing.T) {
	w := &eofWatcher{r: errReader{errors.New("disk exploded")}}

	if _, err := w.Read(make([]byte, 4)); err == nil {
		t.Fatal("expected the underlying error")
	}
	if w.seen() {
		t.Error("a non-EOF error was treated as the client going away")
	}
}

// Closing stdin is the transport's business, not ours.
func TestEOFWatcherCloseIsANoOp(t *testing.T) {
	r := &closeCounter{Reader: strings.NewReader("x")}
	w := &eofWatcher{r: r}

	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if r.closed != 0 {
		t.Error("closing the watcher closed the underlying reader")
	}
}

// stdout is the protocol channel and belongs to the process; the transport
// must not close it out from under us.
func TestNopWriteCloserDoesNotClose(t *testing.T) {
	var buf bytes.Buffer
	w := nopWriteCloser{&buf}

	if _, err := io.WriteString(w, "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if buf.String() != "hello" {
		t.Errorf("wrote %q", buf.String())
	}
}

// known() is what shutdown iterates to drop the leases a departing agent held,
// so a session missing from it is a lease that outlives its owner.
func TestKnownReturnsEverySessionIdentified(t *testing.T) {
	s := newSessions()

	if got := s.known(); len(got) != 0 {
		t.Errorf("a fresh registry knows %v", got)
	}

	// A nil session is the stdio case: the SDK carries no id there, so one is
	// generated per connection.
	first, _ := s.identify(nil)
	second, _ := s.identify(nil)

	if first != second {
		t.Errorf("the same session got two ids: %q and %q", first, second)
	}

	got := s.known()
	sort.Strings(got)
	if len(got) != 1 || got[0] != first {
		t.Errorf("known() = %v, want [%s]", got, first)
	}
}

func TestIdentifyNamesTheAgentGenericallyWithoutAHandshake(t *testing.T) {
	s := newSessions()

	id, name := s.identify(nil)
	if id == "" {
		t.Error("no session id was generated")
	}
	if name != "agent" {
		t.Errorf("name = %q, want the generic fallback", name)
	}
}

func TestRandomIDsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := randomID()
		if id == "" {
			t.Fatal("empty id")
		}
		if seen[id] {
			t.Fatalf("id %q generated twice in 100 draws", id)
		}
		seen[id] = true
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type closeCounter struct {
	io.Reader
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return nil }
