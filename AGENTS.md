# AGENTS.md

This file provides guidance to coding agents (Claude Code and others) when
working with code in this repository.

taskgo is a local-first task manager whose storage is plain Markdown files. A CLI,
a TUI and an MCP server are three front ends over the same directory.

Two documents, two audiences — keep them that way. `README.md` is for humans:
what taskgo does, the commands, the keys, the file format, and the reasoning a
user benefits from. This file is for agents working on the code: architecture,
invariants and the traps. Don't move agent-facing detail into the README, and
don't duplicate the README's user documentation here.

(A third: `internal/mcpserver/instructions.go` is what agents *using* taskgo over
MCP are told at connect time. Different audience again.)

## Commands

```bash
go build ./... && go vet ./... && go test -race ./...
gofmt -l .                           # CI fails on any output here

go test ./internal/store/            # one package
go test -run TestTaskMarkdownRoundTrip ./internal/store/
go test -race -run TestConcurrent ./internal/store/   # the id-allocation race

go run . --data-dir /tmp/tg add Try this   # never touch the real store while testing
go run . --data-dir /tmp/tg tui
go run . mcp                               # stdio; started by an MCP client, not by hand

GOOS=darwin go build ./...           # supported
GOOS=windows go build ./...          # must fail; see unsupported_platform.go
```

Go 1.25 is the floor, set by the MCP SDK rather than by anything here.

`--data-dir` and `TASKGO_DATA_DIR` both override the store root; the env var also
beats `config.json`, which is what lets tests and scripts pin the store.

## Architecture

**Everything goes through `internal/store`.** The CLI (`cmd/`), the MCP server
(`internal/mcpserver`) and the TUI (`internal/tui`) never touch the filesystem
directly. That is what makes a task an agent created and a task a human typed
identical on disk. Logic that lives in `cmd/` is logic the other two front ends
cannot reach — so business rules belong in the store, and `cmd/` stays flag
parsing plus rendering.

**Markdown is canonical; `state.json` is derived.** `internal/store/task.go`
owns the frontmatter round trip, `index.go` owns the derived index and the id
counter. Writes update the Markdown first. `Reindex` rebuilds `state.json` by
scanning `tasks/*.md`; hand-editing a task file and reindexing is a supported
workflow, so any new task field must be added in three places: the `Task`
struct's frontmatter, `IndexEntry`/`entryFor` if listings need it without opening
files, and the `Update` struct.

**The id counter is the exception: it is a high-water mark, not derived.** The
task files know only the ids that still exist, so rebuilding from them alone
rewinds the counter over deleted ids and issues them a second time — which
silently merges two tasks in `activity.jsonl` and in Git history. `Reindex`
therefore takes the furthest of three sources and never moves backwards: the
scanned files, `previousNextID()` (the old counter, read without the version
check because a *larger* number is always safe), and `highestTaskID()` (the
activity log, the only source that survives `state.json` being deleted).
`TestReindexNeverRewindsTheIDCounter` guards this.

**`indexVersion` is a real guard.** `readIndex` refuses an index from a newer
taskgo rather than half-reading it; version 0 means pre-versioning and is
accepted. Bump `indexVersion` when the shape of `IndexEntry` changes meaning,
not when a field is merely added.

**`activity.jsonl` is append-only and never rebuilt.** It records events that
cannot be derived from current state, which is precisely why it is a separate
file from the index — `reindex` must not be able to destroy history.

**Two locks guard two different things** (`store.go`). `flock` is advisory and
*per process*: it serialises the CLI against a running MCP server but lets
goroutines inside one process straight through, so a concurrent MCP server would
allocate the same id twice. The `sync.Mutex` is the half flock does not solve.
The lock is coarse on purpose — it wraps a whole mutation so id allocation,
Markdown write, index update and activity append all land or none do. Packages
that keep their own file in the data directory (`claim`, `agents`) use the
exported `store.WithWriteLock` rather than inventing a second lock.

**Reads take no lock at all**, because every write is atomic (temp file, fsync,
rename, fsync the directory). Use `store.WriteFileAtomic` for anything new
written into the data directory.

**Actor is a required parameter** on every mutating store call
(`Create(actor, …)`), not a global — so the compiler asks who did it. The CLI
passes `ActorHuman`, the MCP server `ActorAgent`. That distinction is the whole
point of the split and shows up in `activity.jsonl` and in Git commit subjects.

### Agent-facing subsystems

Three separate packages answer three questions that cannot be derived from each
other, which is why they are not one:

- `internal/agents` — *who is connected*. `sessions.json`, written by the MCP
  server on connect. Entries carry the server's pid; readers check whether that
  process is alive, which beats a timeout for detecting a killed server.
- `internal/claim` — *who is working on what now*. `claims.json`, leases with an
  expiry (`DefaultTTL` implicit on write, `ExplicitTTL` for `claim_task`).
  Ephemeral, so deliberately not in the task files — an hour of lease churn would
  be Git noise unrelated to task content.
- `internal/history` — *what can be undone*. Opt-in Git repository over the data
  directory (`taskgo history init`). Wired in via `store.OnChange` from
  `app.openStore`, which keeps the store from importing history (a cycle).
  Committing is best-effort: a Git failure must never turn a successful task
  write into an error.

Questions (`ask_human` / `taskgo answer`) live in the store instead, on the task:
`Task.Question`/`AskedBy` are frontmatter, carried into `IndexEntry` so a listing
can show what is waiting on you without opening every file. The Q&A exchange is
appended to the notes body, which is the durable record.

Subtasks are `Task.Parent`, with `checkParent` rejecting cycles and depth beyond
`maxDepth`. Deleting a parent promotes its children to the deleted task's own
level — rewriting their frontmatter, not just the index — so no task file is
left pointing at an id that does not resolve.

**Platforms: Linux and macOS.** `unsupported_platform.go` fails the build
anywhere else, deliberately. Three things are not portable and all three are
load-bearing: `agents.Session.Alive` signals a pid, `taskgo edit` shells out via
`sh -c`, and notifications are `notify-send` plus a systemd user timer. Adding a
platform means providing all of them, not deleting the guard file.

### MCP server

21 tools in `internal/mcpserver`, registered by group in `New`. Tools take task
**ids, never titles** — fuzzy resolution belongs on the human's side (`cmd`'s
`store.Resolve`), because an agent completing the wrong task cannot undo it.

`instructions.go` is sent at initialize and lands in every connected agent's
context window: it tells agents to claim before working, ask rather than guess,
break big work down and note what they did. Tool descriptions can only say what
a tool does, not when to reach for it. Keep it short — it competes with the work.

The agent's name comes from the MCP handshake's `ClientInfo`, not a tool
argument, so it reflects what connected rather than what a caller typed
(`session.go`). The server caches no index; it re-reads on every call, so CLI
changes are visible to the agent immediately.

### TUI

lazygit-shaped: numbered focusable panels (`panelViews`, `panelProjects`,
`panelAgents`, `panelTasks`) narrowing a main list, plus a non-focusable detail
pane that follows the task cursor. Views and projects **compose** rather than
override. It reloads from the store every `refreshEvery` (2s) so agent changes
appear on their own — but not while the user is typing in a text input.

`layout_test.go`'s `TestViewFitsTerminal` checks the layout arithmetic across
terminal sizes; if the UI overflows in a real terminal but that test passes, the
terminal is misreporting its row count (common on fractionally scaled displays).

## Testing conventions

- `cmd` tests drive the **real** command tree via `run`/`mustRun` helpers. This
  is only safe because flag values live on the per-invocation `app` struct rather
  than package-level variables — keep it that way, or one test's `--data-dir`
  leaks into the next.
- `mcpserver` tests connect a real client to a real server over an in-memory
  transport, so schema validation is exercised, not bypassed.
- `store.now` is swappable so tests can pin timestamps.
- Every test uses `t.TempDir()` as the store root.
- `internal/tui` is driven through `Update`, which is a pure function of
  (model, msg): `key()` sends a keystroke, `run()` executes the returned command
  and feeds its message back, `reload()` applies a fresh load. Assert on the
  store afterwards rather than on rendered output — the text is styled and will
  churn, while what landed on disk is the actual contract. `selectView` walks
  the Views panel by name so a test does not encode view ordering.
- `internal/agents` fakes a dead process by running `true` and reusing its pid
  once it has exited, rather than inventing a number that might belong to
  something real.
- Shelling out is testable with a fake: `cmd/editor_test.go` writes a small
  `sh` script that rewrites the file, points `$EDITOR` at it, and asserts the
  hand edit reached the index. Prefer that to leaving a path uncovered — but
  keep the script POSIX. `sed -i` takes a mandatory backup suffix on BSD sed,
  so it fails on macOS; redirect and `mv` instead. CI's macOS job is what
  catches this, and it is the reason that job exists.
- Coverage is not the target, but the gaps left are deliberate: `stdio.Run`
  and `tui.Run` (they own the process), `notify.Send` (fires real desktop
  popups — `--dry-run` covers the decision behind it), and `main`/`Execute`
  (they call `os.Exit`).

Two things worth knowing before writing a test here, because both have already
caught a wrong assumption:

- **Completing a task removes it from the default view.** Reopening with space
  happens from the Done view, not in place.
- **The list sort is not the priority field first.** A pending question outranks
  everything, then a due date, then priority, then id. See `sortEntries`.
- **A claim never stops being explicit.** `Take` does
  `existing.Explicit || explicit`, so a later implicit write cannot weaken a
  lease the agent asked for. Test the two states on two tasks, not one.
- **lipgloss emits no escape codes under `go test`**, so every style renders
  identically and colour cannot be asserted. Test the words; leave the colour.
