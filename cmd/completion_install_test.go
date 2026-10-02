package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func runCompletion(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "taskgo"}
	root.AddCommand(&cobra.Command{Use: "thing", Run: func(*cobra.Command, []string) {}})
	addCompletionInstall(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCompletionInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", "/nonexistent") // no zsh to ask, wherever this runs

	out, err := runCompletion(t, "completion", "install", "bash")
	path := filepath.Join(home, "data", "bash-completion", "completions", "taskgo")
	if err != nil || !strings.HasPrefix(out, "Installed bash completions to "+path) {
		t.Fatalf("bash: %q %v", out, err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "__complete") {
		t.Fatal("not a bash completion script")
	}

	runCompletion(t, "completion", "install", "fish")
	if _, err := os.Stat(filepath.Join(home, "config", "fish", "completions", "taskgo.fish")); err != nil {
		t.Fatal(err)
	}

	// zsh with the folder not on its fpath: prints what to add and leaves
	// ~/.zshrc alone, and adds it with --yes.
	out, _ = runCompletion(t, "completion", "install", "zsh")
	if !strings.Contains(out, "is not on zsh's fpath") {
		t.Fatalf("zsh: %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
		t.Fatal("~/.zshrc must not be touched without --yes")
	}
	runCompletion(t, "completion", "install", "zsh", "--yes")
	if rc, _ := os.ReadFile(filepath.Join(home, ".zshrc")); !strings.Contains(string(rc), "# Added by completion install") {
		t.Fatalf(".zshrc: %q", rc)
	}

	if out, _ := runCompletion(t, "completion", "uninstall", "bash"); !strings.HasPrefix(out, "Removed "+path) {
		t.Fatalf("uninstall: %q", out)
	}
	t.Setenv("SHELL", "/bin/tcsh")
	if _, err := runCompletion(t, "completion", "install"); err == nil || !strings.Contains(err.Error(), "name it") {
		t.Fatalf("unknown shell: %v", err)
	}
}

// Cobra fixes its completion output when the command is built; the scripts
// must still go to whatever output is set afterwards.
func TestCompletionScriptsFollowSetOut(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, err := runCompletion(t, "completion", shell)
		if err != nil || len(out) < 100 || !strings.Contains(out, "taskgo") {
			t.Errorf("%s: %d bytes, %v", shell, len(out), err)
		}
	}
	if out, _ := runCompletion(t, "completion", "zsh", "--no-descriptions"); !strings.Contains(out, "taskgo") {
		t.Error("--no-descriptions should still produce a script")
	}
}
