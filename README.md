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

## Status

Working: storage and CLI. Next: the MCP server (`taskgo mcp`), then the TUI.
