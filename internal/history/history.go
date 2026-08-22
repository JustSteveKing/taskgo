// Package history keeps the taskgo data directory under Git, so that handing
// an agent write access is recoverable rather than one-way.
//
// The activity log already records what happened. It cannot undo any of it,
// which is a thin guarantee for a system an AI agent can write to unattended.
// Git turns the same plain files into a complete, revertible history for
// almost nothing, because the storage was already text designed to diff.
//
// Two deliberate limits. Committing is best-effort: a failure here must never
// turn a successful task write into an error, because the task file is already
// on disk and the commit is bookkeeping. And history is opt-in — `taskgo
// history init` — since silently turning someone's data directory into a Git
// repository is not a thing a task manager should do unasked.
package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

// ErrNotEnabled is returned when the data directory is not a Git repository.
var ErrNotEnabled = errors.New("history is not enabled here (run: taskgo history init)")

// commandTimeout bounds any git invocation. A hung git would otherwise hang
// the CLI and, worse, the MCP server mid-tool-call.
const commandTimeout = 10 * time.Second

// Enabled reports whether the data directory is a Git repository.
func Enabled(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil && info.IsDir()
}

func git(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	// Keep the commits attributable to taskgo without depending on, or
	// disturbing, the user's global Git identity.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=taskgo", "GIT_AUTHOR_EMAIL=taskgo@localhost",
		"GIT_COMMITTER_NAME=taskgo", "GIT_COMMITTER_EMAIL=taskgo@localhost",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Init turns the data directory into a Git repository and makes the first
// commit.
func Init(s *store.Store) error {
	root := s.Root()
	if Enabled(root) {
		return errors.New("history is already enabled here")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed")
	}

	if _, err := git(root, "init", "-q", "-b", "main"); err != nil {
		return err
	}

	// Locks and transient state are not history. Committing .lock would
	// produce a commit every time anything took the write lock.
	ignore := strings.Join([]string{
		"# taskgo runtime state — not history.",
		".lock",
		"claims.json",
		"sessions.json",
		"notified.json",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(ignore), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	return Commit(s, "taskgo: history enabled")
}

// Commit records the current state of the data directory.
//
// Best-effort by contract: callers pass a message and ignore the error,
// because the data is already written and a bookkeeping failure must not be
// reported as a failed task operation.
func Commit(s *store.Store, message string) error {
	root := s.Root()
	if !Enabled(root) {
		return ErrNotEnabled
	}

	if _, err := git(root, "add", "-A"); err != nil {
		return err
	}

	// Nothing staged is the normal outcome of a no-op write, not an error.
	if _, err := git(root, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}

	_, err := git(root, "commit", "-q", "-m", message)
	return err
}

// Entry is one commit.
type Entry struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	When    time.Time `json:"when"`
	Message string    `json:"message"`
}

// Log returns recent commits, newest first.
func Log(s *store.Store, limit int) ([]Entry, error) {
	root := s.Root()
	if !Enabled(root) {
		return nil, ErrNotEnabled
	}
	if limit <= 0 {
		limit = 20
	}

	out, err := git(root, "log",
		fmt.Sprintf("-%d", limit), "--date=iso-strict",
		"--pretty=format:%H\x1f%h\x1f%ad\x1f%s")
	if err != nil {
		// A repository with no commits yet is empty, not broken.
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}

	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, parts[2])
		entries = append(entries, Entry{
			Hash: parts[0], Short: parts[1], When: when, Message: parts[3],
		})
	}
	return entries, nil
}

// Undo reverts the most recent commit, and records the reversal as a new
// commit rather than rewriting history.
//
// Rewriting would be the wrong choice for an audit trail: "this was undone" is
// itself a fact worth keeping, and an agent's mistake plus its correction tells
// you more than the mistake never having appeared.
func Undo(s *store.Store) (Entry, error) {
	root := s.Root()
	if !Enabled(root) {
		return Entry{}, ErrNotEnabled
	}

	entries, err := Log(s, 1)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, errors.New("nothing to undo")
	}
	last := entries[0]

	// Uncommitted edits would be swept into the revert, so refuse rather than
	// quietly discard someone's hand edit.
	if _, err := git(root, "diff", "--quiet"); err != nil {
		return Entry{}, errors.New("the data directory has uncommitted changes; run `taskgo history save` first")
	}

	if _, err := git(root, "revert", "--no-edit", "--no-commit", last.Hash); err != nil {
		_, _ = git(root, "revert", "--quit")
		return Entry{}, err
	}
	if _, err := git(root, "commit", "-q", "-m", "taskgo: undo "+last.Short+" ("+last.Message+")"); err != nil {
		return Entry{}, err
	}

	// The Markdown is canonical, so the index has to be rebuilt from whatever
	// the revert restored.
	if _, err := s.Reindex(); err != nil {
		return Entry{}, fmt.Errorf("reverted, but reindex failed: %w", err)
	}
	return last, nil
}

// Attach wires automatic commits to the store, if history is enabled.
//
// Commits are best-effort and their errors are swallowed on purpose. The task
// file is already written by the time this runs; reporting a Git failure as a
// failed task operation would be a lie about what happened, and would make
// taskgo unusable the moment something was wrong with the repository.
func Attach(s *store.Store) {
	if !Enabled(s.Root()) {
		return
	}
	s.OnChange(func(e store.Event) {
		_ = Commit(s, message(e))
	})
}

// message turns an activity event into a commit subject, so `git log` reads
// like the activity log rather than like "update files".
func message(e store.Event) string {
	who := string(e.Actor)
	subject := string(e.Action)

	switch {
	case e.Task != 0 && e.Detail != "":
		return fmt.Sprintf("%s: %s #%d — %s", who, subject, e.Task, firstLine(e.Detail))
	case e.Task != 0:
		return fmt.Sprintf("%s: %s #%d", who, subject, e.Task)
	case e.Project != "":
		return fmt.Sprintf("%s: %s %s", who, subject, e.Project)
	default:
		return fmt.Sprintf("%s: %s", who, subject)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 72 {
		return s[:69] + "..."
	}
	return s
}
