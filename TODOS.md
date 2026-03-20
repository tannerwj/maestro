# TODOS

## Code Changes

These are infrastructure/code changes to the Maestro daemon itself.

### [Done] Implement workspace `none` strategy with temp dir fallback

**What:** Allow `workspace: none` in agent type config. Workspace manager creates an isolated temp directory under `workspace.root` instead of git-cloning a repo.

**Why:** Non-coding agent packs (ops, DBA, security) don't need a cloned repo. validate.go:58 currently rejects anything except `git-clone`, blocking the entire non-coding expansion.

**Changes:**
- `internal/config/validate.go`: Allow `workspace: "none"` alongside `"git-clone"`
- `internal/workspace/manager.go`: Add `PrepareEmpty()` or branch in `Prepare()` to create temp dir
- Skip `source.Repo` requirement when agent workspace is `none` (validate.go:122-124)
- Temp dir follows same lifecycle as git workspaces (created before run, cleaned up after terminal state)

**Tests needed:** Temp dir creation, cleanup after run, harness receives temp dir as cwd, disk-full error handling.

**Depends on:** Nothing. Can ship independently.

---

### [Done] Implement agent pack config pre-population

**What:** Agent packs can ship `claude/` and `codex/` directories that get copied into the workspace before the agent starts. This gives each pack full control over its agent environment (MCP servers, permissions, instructions, skills).

**Why:** Current packs only define a prompt. Non-coding packs need MCP servers (postgres, kubectl, NVD API), custom permissions, and domain-specific instructions. Passing these as CLI flags is fragile and harness-specific. Native config directories work with both Claude Code and Codex.

**Changes:**
- `internal/config/agent_packs.go`: Resolve pack `claude/` and `codex/` directory paths during pack loading
- `internal/workspace/manager.go`: After workspace prep (clone or temp dir), copy `pack/claude/` → `workspace/.claude/` and `pack/codex/` → `workspace/.codex/`
- Remove dead `Tools` and `MCPs` fields from `config/types.go` AgentPackConfig (or keep as documentation-only metadata)

**Pack structure after this change:**
```
agents/<pack>/
├── agent.yaml
├── prompt.md
├── context/
├── claude/          ← copied to workspace/.claude/
│   ├── CLAUDE.md
│   └── settings.json
└── codex/           ← copied to workspace/.codex/
    └── skills/
```

**Tests needed:** Pack dir copy for both strategies, missing pack dir = no-op, existing .claude/ in git workspace preserved vs overwritten (decision needed), symlink handling, permission errors.

**Depends on:** Workspace `none` strategy (for non-git packs).

---

### [Done] Support repo-embedded agent packs

**What:** Allow agent packs to live inside the target repo (e.g., `.maestro/`) instead of only on the local filesystem. Config declares the source via `pack: repo:.maestro` prefix.

**Why:** Teams should own their agent behavior in their own repo — like `.github/workflows/` for GitHub Actions. Changes to agent prompts, MCP configs, and context are versioned with the code. Different branches can have different agent configs.

**Design:**
- `pack: agents/code-pr` → local pack (resolved at config load time, current behavior)
- `pack: repo:.maestro` → repo pack (resolved after workspace clone from that path in the repo)
- `.maestro/` is the conventional default path when `repo:` is used without a subpath
- Orchestration fields (harness, workspace, approval_policy, max_concurrent) ALWAYS come from maestro.yaml — they're needed before clone
- Agent environment fields (prompt.md, context/, claude/, codex/) come from the repo pack
- If both local and repo packs exist for the same agent type, the one specified in maestro.yaml wins

**Changes:**
- `internal/config/agent_packs.go`: Detect `repo:` prefix, defer resolution, store repo-relative path
- `internal/config/validate.go`: Skip prompt file stat check for `repo:` packs (file doesn't exist yet)
- `internal/workspace/manager.go`: After clone, resolve repo pack and merge into agent config
- `internal/orchestrator/dispatch.go`: Pass resolved pack to workspace prep step

**Two-phase resolution flow:**
```
Phase 1 (config load):  maestro.yaml → harness, workspace, approval_policy
Phase 2 (post-clone):   repo/.maestro/ → prompt.md, context/, claude/, codex/
```

**Tests needed:** repo: prefix parsing, post-clone resolution, missing .maestro/ dir = error, merge precedence, template validation after clone.

**Depends on:** Pack config pre-population (for claude/codex dir copying).

---

### [Done] Fix mustMarshalRaw panic in Codex adapter

**What:** Replace `panic(err)` at `internal/harness/codex/adapter.go:574` with error return.

**Why:** `mustMarshalRaw()` is called 10+ times during approval/message RPC building. If any input is non-serializable, the entire Maestro daemon crashes. This is the only `panic()` in production code paths.

**Changes:**
- Rename to `marshalRaw()`, return `(json.RawMessage, error)`
- Update all 10+ call sites to propagate the error
- `buildApprovalRequest()` and `buildMessageRequest()` already return errors — thread the new error through

**Tests needed:** Test with non-serializable input, verify error propagation instead of crash.

**Depends on:** Nothing.

---

### [Done] Add approval timeout

**What:** Configurable timeout for pending approval requests. Default 24h. Run fails if approval not resolved within timeout.

**Why:** If a user is on PTO or misses a Slack notification, the agent hangs indefinitely waiting for approval. No current timeout mechanism — approvals only expire on daemon restart.

**Changes:**
- `internal/config/types.go`: Add `ApprovalTimeout` field to `AgentTypeConfig`
- `internal/orchestrator/approvals.go`: Check timeout in approval watcher goroutine
- `internal/orchestrator/state.go`: Expire timed-out approvals on restart recovery

**Depends on:** Nothing.

---

### [Done] Extract duplicated mergeEnv to harness/env.go

**What:** `mergeEnv()` is identical in `claude/adapter.go:402` and `codex/adapter.go:796`. Extract to `internal/harness/env.go`.

**Why:** DRY violation. Both are 6-line identical functions.

**Depends on:** Nothing.

---

### [Done] Use permissive default for approved tool permissions (Claude adapter)

**What:** Replace hardcoded tool name switch in `claude/adapter.go:367` (`approvedPermissionMode()`) with a permissive default for any approved tool.

**Why:** The current code maps specific tool names (write, edit, notebookedit, multiedit) to permission modes. If Claude Code adds new tools, they silently get wrong permissions. Since the user already approved the tool, over-permitting is low-risk.

**Depends on:** Nothing.

---

### [Done] Add template parse validation in config

**What:** After checking that the prompt file exists (validate.go:73), parse it as a Go template to catch syntax errors at config load time.

**Why:** A broken template currently passes validation and fails at runtime when the first issue is dispatched. Config-time errors are always better than runtime surprises.

**Changes:**
- `internal/config/validate.go`: After `os.Stat(agent.Prompt)`, call `template.ParseFiles()` with a no-op FuncMap
- Return validation error on parse failure

**Depends on:** Nothing.

---

### [Done] Add HTTP client timeouts to tracker adapters

**What:** Set `Timeout: 30 * time.Second` on HTTP clients in `gitlab/client.go:28` and `linear/client.go:30`. Add `context.WithTimeout` around `Poll()` calls in `loop.go:43`.

**Why:** Both clients are initialized with `&http.Client{}` (no timeout). If the tracker API hangs, the entire tick loop blocks. The live tests add timeouts via context, but production code doesn't.

**Depends on:** Nothing.

---

### [Done] Add workspace failure + workspace:none tests

**What:** Test git clone failures (bad URL, auth failure, branch conflict), cleanup after partial failure, and the new workspace:none temp dir lifecycle.

**Why:** Workspace is the most common production failure point (network flake, expired token, repo deleted). Currently zero tests for error paths.

**Depends on:** Workspace `none` strategy.

---

### [Done] Add failing-tracker test for lifecycle sync errors

**What:** Create a tracker stub that fails on specific label/comment operations. Verify the orchestrator doesn't corrupt run state when lifecycle writes fail.

**Why:** `tracker_sync.go` swallows errors from label add/remove and comment posting. No test validates that partial lifecycle failures don't leave runs in inconsistent state.

**Depends on:** Nothing.

---

### [P2] State file backup/rotation

**What:** Keep rolling backups of `runs.json` (e.g., `runs.json.1`, `runs.json.2`).

**Why:** If `runs.json` is corrupted, there's no fallback. `Load()` fails, state is lost. A rolling backup provides recovery at minimal cost.

**Depends on:** Nothing.

---

### [P2] Approval double-resolve protection

**What:** Add a `Resolved bool` field to approval requests. `ResolveApproval()` checks the flag before processing.

**Why:** If the Slack bridge has a race condition (user clicks approve twice quickly), the same approval could be resolved twice. Currently no guard against this.

**Depends on:** Nothing.

---

### [P3] Harness registry pattern

**What:** Replace the hardcoded switch statement in `service.go` and `validate.go` with a harness registry. Harness adapters self-register.

**Why:** Adding a 3rd harness currently requires changes in 3 places. A registry makes it zero-touch in the orchestrator. Not needed until a 3rd harness exists.

**Depends on:** A concrete 3rd harness use case.

---

## Agent Packs

These are new agent packs for non-coding workflows. Each is a self-contained directory under `agents/`.

### [P0] vuln-triage — Vulnerability triage and assessment

**What:** Agent pack that reads vulnerability/CVE issues, assesses exploitability in context, assigns contextual severity, and closes or escalates.

**Why:** Highest volume, lowest risk (Tier 1, analysis only). Good first proof that non-coding packs work. Security teams drown in scanner-generated tickets — most are noise.

**Pack structure:**
```
agents/vuln-triage/
├── agent.yaml           # workspace: none, approval: auto, harness: claude-code
├── prompt.md            # 3P pattern: Persona + Policy + Procedure
├── context/
│   └── severity-matrix.md
└── claude/
    └── CLAUDE.md        # Agent persona + domain rules
```

**Key prompt patterns:**
- Persona: Senior security engineer
- Policy: Read-only analysis, never execute remediation, close informational-only vulns autonomously
- Procedure: Parse CVE → check if affected version exists → assess exploitability → rate severity → close or escalate
- Evidence chain: Every recommendation cites the data source

**Depends on:** workspace:none strategy, pack config pre-population.

---

### [P0] query-optimizer — PostgreSQL query optimization

**What:** Agent pack that receives slow-query issues, runs EXPLAIN ANALYZE, identifies missing indexes and inefficiencies, and proposes optimizations.

**Why:** High-value for DBA teams. Based on proven patterns from Xata Agent (open-source PostgreSQL AI agent). Read-only analysis with optional index migration generation.

**Pack structure:**
```
agents/query-optimizer/
├── agent.yaml           # workspace: git-clone (for migration files), approval: auto
├── prompt.md            # DBA persona + read-only-first policy
├── context/
│   ├── pg-antipatterns.md
│   └── index-guidelines.md
└── claude/
    ├── CLAUDE.md        # DBA agent instructions
    └── settings.json    # PostgreSQL MCP server config (read-only connection)
```

**Key prompt patterns:**
- Procedure: Run EXPLAIN ANALYZE → check index usage stats → analyze table bloat → propose fix → generate migration file
- Safety: Read-only DB connection by default. Write operations (CREATE INDEX) generate migration files, not direct DDL.

**Depends on:** Pack config pre-population (for MCP server config in settings.json).

---

### [P0] access-reviewer — IAM/access review and compliance

**What:** Agent pack that reviews current access lists against policy, identifies stale/over-privileged accounts, generates remediation report.

**Why:** Compliance teams need this quarterly. Mostly read + report with sub-issue creation. High value, low risk.

**Pack structure:**
```
agents/access-reviewer/
├── agent.yaml           # workspace: none, approval: auto
├── prompt.md            # Compliance analyst persona
├── context/
│   ├── least-privilege-policy.md
│   └── review-checklist.md
└── claude/
    └── CLAUDE.md        # Access review instructions
```

**Key prompt patterns:**
- Procedure: Pull access lists → compare against role definitions → flag stale accounts (no login in N days) → flag over-privileged → generate report → create sub-issues for remediation
- Output: Structured markdown report posted to the issue

**Depends on:** workspace:none strategy, pack config pre-population.

---

### [P1] incident-response — Incident analysis and remediation

**What:** Agent pack that reads incident issues, correlates with monitoring/deployment data, proposes and optionally executes remediation.

**Why:** Highest impact ops pack. Start with Tier 1 (analyze and recommend), layer in Tier 2 actions (restart, scale, rollback) once trust is established.

**Pack structure:**
```
agents/incident-response/
├── agent.yaml           # workspace: git-clone (for runbook repo), approval: destructive-only
├── prompt.md            # SRE persona + tiered action policy
├── context/
│   ├── runbook-index.md
│   └── escalation-matrix.md
└── claude/
    ├── CLAUDE.md        # Incident response rules
    └── settings.json    # Monitoring API MCP, deployment API MCP
```

**Depends on:** workspace:none or git-clone of a runbook repo. Pack config pre-population.

---

### [P1] schema-migration-reviewer — Database migration safety review

**What:** Agent pack that reviews migration files for backwards compatibility, lock duration estimates, data loss risk.

**Why:** High-value for teams with large databases. Catches common anti-patterns (long-running locks, missing CONCURRENTLY, breaking changes).

**Depends on:** Pack config pre-population.

---

### [P2] db-grant-manager — Database access grant management

**What:** Agent pack that parses access requests, validates against a policy matrix, generates and optionally executes GRANT/REVOKE SQL.

**Why:** High volume for DBA teams. Tier 3 (human-in-the-loop for writes).

**Depends on:** workspace:none strategy, pack config pre-population, approval flow working end-to-end.

---

### [P2] firewall-rule-reviewer — Firewall change request assessment

**What:** Agent pack that parses proposed firewall rules, checks for conflicts/compliance violations, generates risk assessment.

**Why:** PwC demonstrated 95% reduction in firewall review timelines with AI assessment. High compliance value.

**Depends on:** workspace:none strategy, pack config pre-population.

---

### [P2] runbook-executor — Structured runbook execution

**What:** Agent pack that parses a referenced runbook, validates preconditions, executes steps sequentially with verification.

**Why:** Operational automation. Requires strong safety model (step-by-step verification, halt on unexpected output).

**Depends on:** workspace:none or git-clone of runbook repo, pack config pre-population, approval flow.

---

### [P3] change-request-processor — Generic change management

**What:** Agent pack that parses change requests, validates completeness, assesses risk, routes to appropriate approver.

**Why:** Generic across domains. Lower priority because it's less specialized.

**Depends on:** workspace:none strategy.

---

### [P3] doc-updater — Documentation freshness

**What:** Agent pack that reads code changes and updates README, runbooks, wiki pages.

**Why:** Everyone needs it but no one prioritizes it. Good generic pack.

**Depends on:** git-clone workspace (needs the repo).
