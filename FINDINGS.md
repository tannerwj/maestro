# Security and performance findings

Reviewed at `f6a77da38e1eaabe1d27b83a6bc9039032c3d5fd` on 2026-10-02. The full evidence, threat model, benchmarks, and original reproduction commands are in [the review](review/security-performance-2026-10-02.md). This file tracks the remediation developed on `codex/security-performance-remediation` and the remaining work.

| ID | Severity | Status | Finding and disposition |
|---|---|---|---|
| S1 | High | Fixed | `repo:` packs now reject symlinks in the pack path, prompt, context root/files, and harness config directories before host-side reading/copying. The isolated symlink reproduction now fails closed. Concurrent local mutation between validation and reading is still outside this check; a hostile local process with write access to a workspace needs a stronger file-descriptor-based boundary. |
| S2 | High | Open | Claude manual approval still reruns the prompt under `bypassPermissions` after one approval. A correct fix must route each exact tool call through Claude's native permission host/hook and Maestro's approval channel; a prompt rerun cannot preserve that guarantee. Do not rely on Claude manual mode as a per-action boundary until this is implemented and tested. |
| S3 | High | Open | Docker `network_policy: allowlist` still sets proxy variables on bridge networking. Direct sockets can bypass it. Namespace/daemon-level egress isolation and a Docker-backed test are required. This host has no running Docker daemon, so no container behavior is claimed. Use `network_policy: none` or external egress controls when isolation is required. |
| S4 | Medium | Fixed | Unsafe API requests now reject foreign `Origin` and `Sec-Fetch-Site: cross-site`. The unauthenticated loopback server rejects non-loopback HTTP Host names on all routes, including reads, to block DNS-rebinding access. The browser smoke checks forged Origin, legitimate same-origin POST, and hostile Host. Clients without browser origin headers can still use the loopback API as the local operator. |
| S5 | Medium | Fixed with migration note | Workspace and branch keys now encode every unsafe identifier byte injectively, so the reproduced `team/repo#1` / `team_repo#1` pair gets distinct paths. Before fetching an existing clone, Maestro verifies its `origin` matches the requested repository and preserves it on mismatch. Previous workspaces with punctuation retain their old paths and are not moved automatically; inspect and preserve local work before migrating them. Cross-process locking is not part of this change. |
| S6 | Medium | Partly fixed | Stdout/stderr now stream directly to private files, each capped at 16 MiB plus a truncation marker. Only 4 KiB tails remain in RAM and error messages. State directories, run directories, logs, and rotated backups use owner-only modes; API output tails pass through existing redaction. Raw owner-only log files can still contain arbitrary secrets that the harness prints. Existing backup files are tightened as they rotate. |
| S7 | Medium | Partly fixed | The dashboard uses an authenticated streaming `fetch` for SSE; the API no longer accepts long-lived keys in query strings. The initial dashboard URL may still bootstrap a key via `api_key`, and non-loopback plaintext HTTP, ephemeral key logging, and raw config access under a full-control key remain deployment concerns. Use a TLS-terminating trusted proxy and protect the API key. |

## Performance and reliability

| ID | Status | Finding and disposition |
|---|---|---|
| P1 | Open | Tracker GET/link requests scale with dispatch candidates and active runs. Correctness-sensitive caching and rate-limit handling need a separate design and fake-tracker latency/failure fixtures. |
| P2 | Partly fixed | Per-run output no longer grows without bound in RAM or disk. The hermetic smoke emits 17 MiB on one stream and checks the 16 MiB cap, truncation marker, and owner-only file modes. Peak RSS remains to be measured. |
| P3 | Open | State saves still marshal and fsync the full snapshot and copy backups. The permission changes do not reduce write amplification. |
| P4 | Partly fixed | Dashboard run lookup now uses a per-render ID index, and redundant selection effects were removed. SSE still marshals a snapshot per client per second. |
| P5 | Open | Healthy workspace reuse still fetches origin every time. Freshness requirements need to be defined before reducing fetches. |
| E2E | Fixed | The fake GitLab tracker now returns issue links correctly, and the smoke fixture recognizes the new workspace keys and current Codex sandbox spelling. `make smoke-hermetic` passes. |
| Supply chain | Fixed at this lockfile revision | A fresh online `npm audit` found 11 alerts (8 high, 2 moderate, 1 low) in the frontend toolchain. `npm audit fix` updated compatible lockfile dependencies; the subsequent audit reports zero. This is a point-in-time advisory result. |

## Verification

Run from the repository root:

```bash
go test ./...
make smoke-hermetic
go run ./review/repro_repo_pack_symlink
go run ./review/repro_workspace
cd web
npm ci
npm run build
npm run lint
npm run test:smoke
npm audit
```

The two `go run` commands use disposable local fixtures and now assert that the original exploit paths are rejected. The browser smoke runs the local demo server and includes the CSRF/Host checks. No live tracker, Slack, model, or production endpoint is needed.

## Rollback and migration

If the new workspace key prevents a continuation, stop dispatch for that issue, preserve the old directory, and move or selectively copy the reviewed local work into the newly named workspace. Check its origin and branch before restarting. Reverting the branch restores the previous paths but also restores the collision and symlink vulnerabilities. The output cap deliberately truncates unusually noisy logs; raise `runLogMaxBytes` only with an explicit storage budget. Keep existing state and backup files private while older versions remain in service.
