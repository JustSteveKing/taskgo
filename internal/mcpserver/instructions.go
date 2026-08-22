package mcpserver

// instructions are sent to every client at initialize.
//
// Tool descriptions can only say what one tool does. They cannot say when to
// reach for it, and an agent reading nineteen of them will infer a workflow
// that is mostly "create tasks and never touch them again" — which is what
// taskgo looked like before this existed.
//
// Kept short on purpose: this lands in the context window of every agent that
// connects, and a page of prose competes with the work.
const instructions = `taskgo is a shared task list. A human is looking at the same tasks you are,
in a terminal UI that refreshes automatically, so anything you change is
visible to them within seconds.

How to work here:

CLAIM BEFORE YOU WORK. Call claim_task when you start on something. The human
sees who is on what, and another agent will not duplicate your work. It is
released automatically when you complete the task or disconnect.

ASK RATHER THAN GUESS. If a task is ambiguous, or a decision is really the
human's to make, call ask_human. It does not block — the task is flagged as
waiting on them and you should go and do something else, polling check_answer
later. Guessing on a decision that was theirs is worse than waiting.

BREAK BIG WORK DOWN. If a task is really several pieces, create subtasks with
parent set to the original id, rather than doing everything under one vague
title. The human can then see progress rather than a task that is "in progress"
for two days.

SAY WHAT YOU DID. Use add_note as you go, and note anything surprising you
found. The notes are the durable record of the work; the task title is only
its name.

CHECK BEFORE YOU START. list_claims shows what other agents hold. get_today
and get_overdue show what actually matters now, which is usually a better place
to start than the top of list_tasks.

Everything is a plain Markdown file the human can open and edit, so write notes
you would be happy for them to read.`
