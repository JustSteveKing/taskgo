//go:build !linux && !darwin

package main

// taskgo supports Linux and macOS. This file exists so that other platforms
// fail here, at compile time, with a message naming the reason — rather than
// producing a binary that runs and then quietly misbehaves.
//
// The parts that are not portable are load-bearing rather than cosmetic:
// agent liveness is decided by sending signal 0 to the MCP server's pid,
// `taskgo edit` shells out through `sh -c`, and notifications are notify-send
// with a systemd user timer. A build that skipped all four would show an empty
// agent roster, fail to open an editor, and never notify — which is worse than
// not building.
//
// Adding a platform means providing those four, not deleting this file.
const _ = taskgo_supports_linux_and_macos_only
