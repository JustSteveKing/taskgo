package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestNoBackgroundQuery builds taskgo and runs it on a terminal that never
// answers, checking it does not ask for the background colour. Bubble Tea
// asks in its package init; internal/termquiet answers first by relying on
// Go's package initialisation order. Rename or move that package, or drop
// its import from main, and every command waits five seconds on a terminal
// like this one. See internal/termquiet.
func TestNoBackgroundQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pty")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "taskgo")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "--version")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	tty, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}
	defer tty.Close()

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// The reader owns out until it finishes; read it only after readDone.
	var out bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		cmd.Process.Kill()
		t.Fatal("taskgo --version took over 4s on a terminal that does not answer: it is waiting on a terminal query")
	}
	elapsed := time.Since(start)
	tty.Close() // ends the reader's blocked Read
	<-readDone
	if elapsed > 2*time.Second {
		t.Errorf("taskgo --version took %s", elapsed)
	}
	if bytes.Contains(out.Bytes(), []byte("\x1b]11;?")) {
		t.Error("taskgo asked the terminal for its background colour")
	}
}
