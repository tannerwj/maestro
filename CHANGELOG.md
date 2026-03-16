# Changelog

## v0.1.0

First cut of the local Maestro daemon.

Highlights:

- GitLab project issue intake
- GitLab epic bucket intake via linked child issues
- Linear project intake
- Claude and Codex execution
- Manual approval flow for Claude in the local TUI
- Persisted local state with retry and restart recovery
- Tracker lifecycle writeback and reconciliation
- Agent packs for reusable prompt/context bundles
- Stall detection and lifecycle hooks
- Operator commands for inspection, reset, and workspace cleanup

Known gaps:

- Codex manual approval is not yet live-validated because the current local app-server build does not emit approval requests reliably in this environment
- Slack, Teams, web UI, API server, and dynamic config reload are still out of scope
- `hooks.before_remove` is reserved but not implemented yet
