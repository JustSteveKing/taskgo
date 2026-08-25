package cmd

import (
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/JustSteveKing/taskgo/internal/store"
	"github.com/spf13/cobra"
)

// completions run against the real store, so they are exercised the way the
// shell would. --data-dir is all openStore needs, which is the same reason the
// flag can stand in for the config file everywhere else.
func completionApp(dir string) *app {
	return &app{dataDir: dir, out: io.Discard, err: io.Discard}
}

// names strips the tab-separated descriptions cobra shows on the right.
func names(completions []string) []string {
	out := make([]string, 0, len(completions))
	for _, c := range completions {
		out = append(out, strings.SplitN(c, "\t", 2)[0])
	}
	sort.Strings(out)
	return out
}

func TestCompleteStatusAndPriority(t *testing.T) {
	got, directive := completeStatus(nil, nil, "")
	if want := []string{"blocked", "doing", "done", "todo"}; !equal(names(got), want) {
		t.Errorf("statuses = %v, want %v", names(got), want)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v; completing a status must not offer filenames", directive)
	}

	got, _ = completeStatus(nil, nil, "do")
	if want := []string{"doing", "done"}; !equal(names(got), want) {
		t.Errorf("statuses for %q = %v, want %v", "do", names(got), want)
	}

	got, _ = completePriority(nil, nil, "")
	if want := []string{"high", "low", "normal", "urgent"}; !equal(names(got), want) {
		t.Errorf("priorities = %v, want %v", names(got), want)
	}

	// Every offered value must actually parse, or the completion is lying.
	for _, s := range names(got) {
		if _, err := store.ParsePriority(s); err != nil {
			t.Errorf("completion offers %q which does not parse: %v", s, err)
		}
	}
	statuses, _ := completeStatus(nil, nil, "")
	for _, s := range names(statuses) {
		if _, err := store.ParseStatus(s); err != nil {
			t.Errorf("completion offers %q which does not parse: %v", s, err)
		}
	}
}

func TestCompleteDue(t *testing.T) {
	got, _ := completeDue(nil, nil, "")
	if want := []string{"today", "tomorrow"}; !equal(names(got), want) {
		t.Errorf("due = %v, want %v", names(got), want)
	}

	// Both must be values ParseDue accepts.
	for _, s := range names(got) {
		if _, err := store.ParseDue(s); err != nil {
			t.Errorf("completion offers %q which does not parse: %v", s, err)
		}
	}

	got, _ = completeDue(nil, nil, "tom")
	if want := []string{"tomorrow"}; !equal(names(got), want) {
		t.Errorf("due for %q = %v", "tom", names(got))
	}
}

func TestCompleteTaskRefOffersIDsWithTitles(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Fix the login redirect")
	mustRun(t, dir, "add", "Write release notes")
	a := completionApp(dir)

	got, directive := a.completeTaskRef(&cobra.Command{Use: "done"}, nil, "")
	if len(got) != 2 {
		t.Fatalf("want 2 completions, got %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", directive)
	}
	// The shell inserts the id and shows the title, so the pair matters.
	if !strings.HasPrefix(got[0], "1\t") || !strings.Contains(got[0], "Fix the login redirect") {
		t.Errorf("completion is not id<TAB>title: %q", got[0])
	}

	// A second positional argument is a title, not another ref.
	if got, _ := a.completeTaskRef(&cobra.Command{Use: "done"}, []string{"1"}, ""); got != nil {
		t.Errorf("completing past the ref offered %v", got)
	}
}

// done should not offer what is already done; reopen should offer nothing else.
func TestCompleteTaskRefRespectsTheCommand(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "Open task")
	mustRun(t, dir, "add", "Finished task")
	mustRun(t, dir, "done", "2")
	a := completionApp(dir)

	got, _ := a.completeTaskRef(&cobra.Command{Use: "done"}, nil, "")
	if len(got) != 1 || !strings.Contains(got[0], "Open task") {
		t.Errorf("done offered %v, want only the open task", got)
	}

	got, _ = a.completeTaskRef(&cobra.Command{Use: "reopen"}, nil, "")
	if len(got) != 1 || !strings.Contains(got[0], "Finished task") {
		t.Errorf("reopen offered %v, want only the completed task", got)
	}
}

func TestCompleteTaskRefFiltersByWhatIsTyped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12; i++ {
		mustRun(t, dir, "add", "Task")
	}
	a := completionApp(dir)

	got, _ := a.completeTaskRef(&cobra.Command{Use: "done"}, nil, "1")
	// 1, 10, 11, 12 — prefix on the id, which is all a shell can offer here.
	if want := []string{"1", "10", "11", "12"}; !equal(names(got), want) {
		t.Errorf("ids = %v, want %v", names(got), want)
	}
}

func TestCompleteProjectShowsOpenCounts(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "projects", "new", "web")
	mustRun(t, dir, "projects", "new", "infra")
	mustRun(t, dir, "add", "A web task", "--project", "web")
	a := completionApp(dir)

	got, _ := a.completeProject(nil, nil, "")
	if want := []string{"infra", "web"}; !equal(names(got), want) {
		t.Errorf("projects = %v, want %v", names(got), want)
	}
	for _, c := range got {
		if strings.HasPrefix(c, "web\t") && !strings.Contains(c, "1 open") {
			t.Errorf("web completion should carry its count: %q", c)
		}
	}

	got, _ = a.completeProject(nil, nil, "w")
	if want := []string{"web"}; !equal(names(got), want) {
		t.Errorf("projects for %q = %v", "w", names(got))
	}
}

// Tags come off the index rather than a list of their own, so a tag stops
// being offered as soon as nothing carries it.
func TestCompleteTagReadsTheIndex(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "add", "One", "--tag", "auth", "--tag", "bug")
	mustRun(t, dir, "add", "Two", "--tag", "auth")
	a := completionApp(dir)

	got, _ := a.completeTag(nil, nil, "")
	if want := []string{"auth", "bug"}; !equal(names(got), want) {
		t.Errorf("tags = %v, want %v", names(got), want)
	}
	for _, c := range got {
		switch {
		case strings.HasPrefix(c, "auth\t") && !strings.Contains(c, "2 tasks"):
			t.Errorf("auth should say 2 tasks: %q", c)
		case strings.HasPrefix(c, "bug\t") && !strings.Contains(c, "1 task"):
			t.Errorf("bug should say 1 task, singular: %q", c)
		}
	}

	mustRun(t, dir, "delete", "1", "--force")
	got, _ = a.completeTag(nil, nil, "")
	if want := []string{"auth"}; !equal(names(got), want) {
		t.Errorf("after deleting the only bug task, tags = %v, want %v", names(got), want)
	}
}

func TestCompletionScriptsAreGenerated(t *testing.T) {
	dir := t.TempDir()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out := mustRun(t, dir, "completion", shell)
		if len(out) < 100 {
			t.Errorf("%s completion looks empty:\n%s", shell, out)
		}
		if !strings.Contains(out, "taskgo") {
			t.Errorf("%s completion does not mention taskgo", shell)
		}
	}

	// An unknown shell gets cobra's usage rather than a script. Cobra exits 0
	// there, so what matters is that nothing script-shaped is emitted.
	out, err := run(t, dir, "completion", "commodore64")
	if err == nil && strings.Contains(out, "complete -") {
		t.Errorf("an unknown shell produced something script-shaped:\n%s", out)
	}
	if !strings.Contains(out, "Usage") && err == nil {
		t.Errorf("an unknown shell should at least print usage:\n%s", out)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
