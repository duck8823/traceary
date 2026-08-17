# Cockpit UI/UX design (historical)

[日本語](./cockpit-ui-ux-design.ja.md)

> **Historical.** The operator cockpit (`traceary tui` / `traceary dashboard` and the bare TTY default that opened it) was removed in v0.35.0 (#1764) after the v0.34 deprecation window (#1687). Bare `traceary` always prints help. Use `traceary list`, `traceary search`, `traceary tail`, `traceary doctor`, and `traceary memory inbox review` for surviving interactive and script-friendly surfaces. The orphan local state file `~/.local/state/traceary/cockpit.json` (or `$XDG_STATE_HOME/traceary/cockpit.json`) is safe to delete manually.

This document previously recorded the v0.17–v0.18 reference-driven redesign target for the cockpit. Keep the path only as a historical pointer; do not treat it as current operator guidance.
