# Release Guide

## Goal

The `v0.1` bar is a stable local daemon build that can:

- poll one or more configured sources
- dispatch bounded active runs according to `defaults.max_concurrent_global` and `agent_types[].max_concurrent`
- survive restart with local state
- show approvals in the TUI
- write lifecycle state back to the tracker

## Release Checklist

- `go test ./...`
- `make build`
- `make smoke-gitlab` on a real GitLab issue
- `make smoke-linear` on a real Linear issue
- `make smoke-many-sources` on a real mixed GitLab + Linear setup
- `maestro inspect runs --config <demo-config>`
- `maestro reset issue --config <demo-config> <issue>`
- `maestro cleanup workspaces --config <demo-config> --dry-run`
- review [CHANGELOG.md](../CHANGELOG.md)
- review known gaps in [README.md](../README.md)

## Build Release Archives

Create cross-platform archives under `dist/<version>/`:

```bash
make release VERSION=v0.1.0
```

That produces:

- macOS amd64
- macOS arm64
- Linux amd64
- Linux arm64
- `checksums.txt`

## Versioning

`maestro version` is set from `-ldflags "-X main.version=..."`.

Normal local builds use `git describe --tags --always --dirty`.

For a tagged release:

```bash
git tag v0.1.0
make release VERSION=v0.1.0
```

## Suggested Release Notes Shape

- what changed for operators
- what tracker/harness combinations are known-good
- what is still intentionally missing from `v0.1`
- the shortest path to a demo using the configs in `demo/`
