package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// The path is interpolated into a string handed to `sh -c`, so the quoting is
// the only thing standing between a data directory with an apostrophe in its
// name and the shell executing whatever follows it.
func TestShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/home/me/tasks/1.md", `'/home/me/tasks/1.md'`},
		{"space", "/home/me/my tasks/1.md", `'/home/me/my tasks/1.md'`},
		{"apostrophe", "/home/steve's/1.md", `'/home/steve'\''s/1.md'`},
		{"double quote", `/home/me/"q"/1.md`, `'/home/me/"q"/1.md'`},
		{"dollar", "/home/me/$HOME/1.md", `'/home/me/$HOME/1.md'`},
		{"backtick", "/home/me/`whoami`/1.md", "'/home/me/`whoami`/1.md'"},
		{"semicolon", "/tmp/x; rm -rf ~/1.md", `'/tmp/x; rm -rf ~/1.md'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Errorf("shellQuote(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// Whatever the quoting produces, the shell must hand back the original string
// as a single argument. This is the property the table above is a proxy for.
func TestShellQuoteSurvivesTheShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}

	for _, path := range []string{
		"/home/me/tasks/1.md",
		"/home/me/my tasks/1.md",
		"/home/steve's/1.md",
		"/tmp/x; echo INJECTED",
		"/tmp/$(echo INJECTED)/1.md",
		"/tmp/`echo INJECTED`/1.md",
		`/tmp/"quoted"/1.md`,
	} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+shellQuote(path)).Output()
		if err != nil {
			t.Errorf("sh rejected %q: %v", path, err)
			continue
		}
		if string(out) != path {
			t.Errorf("round trip changed %q into %q", path, out)
		}
		if strings.Contains(string(out), "INJECTED") && !strings.Contains(path, "INJECTED") {
			t.Errorf("%q was executed rather than quoted", path)
		}
	}
}

// `taskgo edit` with no flags opens the file and reindexes afterwards, because
// the user may have changed indexed fields by hand — the workflow the plain
// file design exists to allow.
func TestEditWithNoFlagsOpensTheEditorAndReindexes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	mustRun(t, dir, "add", "Before the edit")

	// A fake editor that rewrites the title, standing in for a human typing.
	editor := filepath.Join(t.TempDir(), "fake-editor")
	script := fakeEditorScript("Before the edit", "After the edit")
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	out := mustRun(t, dir, "edit", "1")
	if !strings.Contains(out, "Saved #1") {
		t.Errorf("got %q", out)
	}

	// The index must reflect the hand edit without a separate reindex.
	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "After the edit" {
		t.Errorf("the edit did not reach the index: %+v", tasks)
	}
}

// A data directory whose path needs quoting must still work end to end.
func TestEditWorksInADirectoryThatNeedsQuoting(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := filepath.Join(t.TempDir(), "steve's tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustRun(t, dir, "add", "Quote me")

	editor := filepath.Join(t.TempDir(), "fake-editor")
	script := fakeEditorScript("Quote me", "Quoted fine")
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	mustRun(t, dir, "edit", "1")

	var tasks []store.IndexEntry
	decode(t, mustRun(t, dir, "list", "--json"), &tasks)
	if len(tasks) != 1 || tasks[0].Title != "Quoted fine" {
		t.Errorf("editing in a quoted path failed: %+v", tasks)
	}
}

// An editor that exits non-zero must surface as an error naming it, not as a
// silent success that loses the edit.
func TestEditReportsAFailingEditor(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	mustRun(t, dir, "add", "A task")

	t.Setenv("EDITOR", "exit 3 #")
	t.Setenv("VISUAL", "")

	_, err := run(t, dir, "edit", "1")
	if err == nil {
		t.Fatal("a failing editor should be an error")
	}
	if !strings.Contains(err.Error(), "editor") {
		t.Errorf("error should name the editor, got: %v", err)
	}
}

// fakeEditorScript builds a stand-in for $EDITOR that performs one
// substitution on the file it is handed.
//
// It redirects rather than using `sed -i`, because the in-place flag takes a
// mandatory backup suffix on BSD sed and so fails on macOS — one of the two
// platforms taskgo claims. The CI macOS job caught exactly this.
func fakeEditorScript(from, to string) string {
	return "#!/bin/sh\n" +
		"set -e\n" +
		"sed 's/" + from + "/" + to + "/' \"$1\" > \"$1.new\"\n" +
		"mv \"$1.new\" \"$1\"\n"
}
