# taskgo

A local-first task manager for humans **and** agents.

Tasks are plain Markdown files. A CLI, a TUI and a local MCP server all read
and write the same files, so a task an agent creates and a task you type are
identical on disk. No database, no daemon, no account.

```bash
taskgo add Fix the login redirect --project web --tag auth --due tomorrow
taskgo list
taskgo done login          # resolve by unique title substring
```

## Why plain files

The point is that you can always check. `cat` a task, `grep` the directory,
fix a mistake in your editor, `git diff` what changed. A system you are going
to hand write access to an AI agent should not also be a black box — the file
layout *is* the audit surface, and the activity log records whether each change
came from a human or an agent.

## Storage

```
~/.local/share/taskgo/
├── config.json
├── state.json          # derived index + id counter — rebuildable
├── activity.jsonl      # append-only log — NOT rebuildable
├── projects/<name>.json
└── tasks/<id>.md
```

A task file:

```markdown
---
id: 47
title: Fix login redirect
status: todo          # todo | doing | blocked | done
priority: high        # low | normal | high | urgent
due: "2026-08-25"
project: web
tags:
    - auth
created: 2026-08-22T10:14:00Z
updated: 2026-08-22T10:20:00Z
---

Free-form notes, in Markdown.
```

### Three properties that make this trustworthy

**Markdown is canonical; `state.json` is derived.** Writes update the Markdown
first. Edit a task file by hand, run `taskgo reindex`, and the change is picked
up — that round trip is a supported workflow, not an accident.

**Every write is atomic.** Temp file, fsync, rename, fsync the directory.
Rename is atomic within a filesystem, so a reader never sees a partial file —
which is why *reads take no lock at all*. Only writers lock.

**The activity log is append-only and never rebuilt.** It records things that
happened, which cannot be derived from current state. That is why it is a
separate file from the index: `reindex` regenerates `state.json` and must not
be able to destroy history.

## Commands

| Command | What it does |
|---|---|
| `add <title>...` | create a task |
| `list` | list tasks (`--all --project --status --tag --due --overdue --today --search`) |
| `show <ref>` | one task in full, notes included |
| `done` / `reopen <ref>` | change completion |
| `edit <ref>` | change fields, or open the file in `$EDITOR` with no flags |
| `note <ref> <text>...` | append a note without touching what is there |
| `delete <ref>` | remove a task (asks first) |
| `status` | summary |
| `activity` | who changed what, and when |
| `projects` / `projects new` | projects |
| `reindex` | rebuild `state.json` from the Markdown |
| `notify` | desktop notifications for due and overdue work |
| `completion <shell>` | shell completion script |

`<ref>` is an exact task id **or** a unique case-insensitive title substring.
It is deliberately *not* an id prefix: with sequential ids `4` is both a
complete id and a prefix of `47`, so there is no rule that resolves it safely.
An ambiguous title errors and lists the candidates rather than guessing —
silently completing the wrong task is worse than making you retype.

Every command takes `--json` for machine-readable output.

## Configuration

None required. Optionally `~/.local/share/taskgo/config.json`:

```json
{
  "defaultProject": "web",
  "editor": "nvim"
}
```

`TASKGO_DATA_DIR` overrides the store location and always wins over the config
file, so scripts and tests can pin it.

## Development

```bash
go build ./... && go vet ./... && go test -race ./...
```

The store's tests cover the things that break silently: Markdown round-trip
fidelity (including a notes body that itself contains a `---` line), reindex
faithfulness, and concurrent writers getting distinct ids.

That last one earned its keep immediately — `flock` is advisory and *per
process*, so it serialises the CLI against a running MCP server but does not
serialise goroutines inside one process. The store therefore holds an
in-process mutex as well; the two locks guard different things.

## MCP

```bash
claude mcp add taskgo -- taskgo mcp
```

Thirteen tools: `list_tasks` `get_task` `create_task` `update_task`
`complete_task` `reopen_task` `add_note` `search_tasks` `get_overdue`
`get_today` `list_projects` `create_project` `get_activity`.

Every change an agent makes lands in the same Markdown files the CLI reads, so
it shows up in `taskgo list` immediately — and every one is recorded as
`agent` in the activity log, which is how you tell later who did what.

Tools take task **ids**, never titles. An agent wanting "the login task" should
call `search_tasks` first and use the id it gets back; resolving a fuzzy
reference inside a tool call would mean guessing on the agent's behalf, and
completing the wrong task is not something the agent can undo.

The server holds no cached index — it re-reads on every call, so a task you add
from the CLI is visible to the agent on its next request.

## TUI

```bash
taskgo tui
```

Laid out like the lazy* family: numbered side panels narrowing what the main
panel shows, a detail pane that follows the cursor, and a footer whose keys
change with the focused panel.

```
╭─ 1 Views ────╮╭─ 3 Tasks (9) ─────────────────────────╮
│ All          ││  2  Ship the MCP server  @taskgo 3d late│
│ Today        ││  5  Fix the contact form @website       │
│ Overdue      ││ ...                                     │
╰──────────────╯╰─────────────────────────────────────────╯
╭─ 2 Projects ─╮╭─ Detail — #2 ─────────────────────────╮
│ (all)        ││ Ship the MCP server                    │
│ infra      3 ││ todo · normal · 3d late · @taskgo      │
╰──────────────╯╰─────────────────────────────────────────╯
 9 open · 10 total · 2 overdue · 3 today
 j/k move · space done · n new · e edit · ? help · q quit
```

| Key | |
|---|---|
| `1` `2` `3` | focus views, projects, tasks |
| `tab` / `shift+tab` | cycle panels |
| `h` / `l` | move between the side panels and the tasks |
| `j` `k`, `ctrl+d` `ctrl+u`, `g` `G` | move |
| `space` | complete / reopen |
| `s` `p` | cycle status / priority |
| `n` | new task, in the selected project |
| `e` | open the Markdown file in `$EDITOR` |
| `d` | delete, with confirmation |
| `/` | filter by title |
| `?` | help |

Views and projects compose rather than override, so "Overdue" plus a project
shows that project's overdue work. The selection bar dims on unfocused panels,
so it is always obvious which cursor the keyboard is driving.

It re-reads the store every couple of seconds while idle, so a task an agent
creates over MCP appears without you touching anything — but not while you are
typing a filter or a new title, because being two seconds stale beats having
the list move under your cursor.

**If the layout overflows the bottom of your terminal**, the terminal is
probably reporting more rows than it draws — a known problem on fractionally
scaled displays, where btop and friends overflow identically. `TestViewFitsTerminal`
in `internal/tui` checks the arithmetic against a range of sizes, so you can
tell the two apart.

## Status

Storage, CLI, MCP server, TUI, notifications and completions all work. Next: a
release build.

Tested: `internal/store`, `internal/mcpserver`, `internal/notify` and `cmd`.
The `cmd` tests drive the real command tree the way a user would, which is only
safe because flag values live on a per-invocation struct rather than in
package-level variables — otherwise one test's `--data-dir` would leak into the
next. `internal/tui` and `internal/config` have no tests yet.
