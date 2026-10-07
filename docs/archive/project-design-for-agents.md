# Designing Projects for Agent Execution (archived)

> General issue-writing notes, not Maestro runtime documentation. For current Maestro behavior, start with the [docs index](../README.md).

How to structure projects, milestones, and issues so that autonomous agents can pick up work, execute it cleanly, and hand it back for review — without getting stuck, going out of scope, or producing unverifiable results.

This guide is tool-agnostic. The principles apply whether you use Linear, GitHub Issues, Jira, or plain markdown files.

---

## Core Principle

**An agent is a single-session, single-branch worker.** It picks up one issue, implements it, verifies it, opens a PR, and stops. Design every issue with that constraint in mind.

---

## Project Structure

### Milestones

A milestone is a user-visible outcome. Not a theme, not a sprint, not a time box — an outcome.

**Good milestones:**
- "Player can walk around the starting area and talk to NPCs"
- "API serves paginated search results with <200ms p95 latency"
- "User can sign up, log in, and reset their password"

**Bad milestones:**
- "Backend work" (theme, not outcome)
- "Sprint 4" (time box, not outcome)
- "Refactoring" (activity, not outcome)

**Rules:**
- Each milestone should be demonstrable — you can show it to someone and they see the value
- Order milestones so each one builds on the last and is independently valuable
- Put polish and integration milestones before expansion milestones. A small thing that works beats a big thing that doesn't.
- 4–10 issues per milestone is the sweet spot. Fewer means the issues are too big. More means the milestone is too broad.

### The "Fix → Feel → Feature" Pattern

When inheriting or continuing a project, resist the urge to add new features first. Instead:

1. **Fix** — make what exists actually work (integration bugs, wiring gaps, silent failures)
2. **Feel** — make it feel right (polish, timing, feedback loops)
3. **Feature** — then expand

This pattern prevents the common failure mode of building floor after floor on a broken foundation.

---

## Issue Design

### The One-Agent Rule

Every issue must be completable by one agent in one session on one branch. If you can't picture a single agent finishing it and opening a clean PR, the issue is too big.

### Anatomy of an Agent-Ready Issue

```
Title: <verb> <concrete outcome>

Problem: Why this work exists. One paragraph max.

Scope:
- What to change, create, or wire together
- Which files/modules are in play

Out of scope:
- Adjacent work that is explicitly NOT part of this issue
- This prevents agents from expanding into neighboring tickets

Acceptance criteria:
- Observable outcomes, not activities
- "X happens when Y" not "implement X"

Verification:
- Exact commands to run
- Expected output or behavior
- Both automated (test commands) and manual (what to look for)

Blocked by: [list of issue IDs that must merge first]
```

### Title Convention

Start with a verb. Name one concrete outcome.

**Good:** "Fix MetalView keyboard focus — first responder wiring"
**Bad:** "Input system improvements"

**Good:** "Implement bomb mechanics — place, throw, chain explosion"
**Bad:** "Combat items"

### What Makes an Issue Too Big

An issue is too big if ANY of these are true:

- It spans multiple modules with different test strategies
- It mixes infrastructure, feature, and refactor work
- It contains unresolved architectural decisions ("choose between X and Y")
- It cannot be verified with a short, explicit set of commands
- It has more than one independently reviewable outcome
- You'd describe it with "and also" ("implement X **and also** Y")

### What Makes an Issue Too Small

An issue is too small if:

- It's a one-line change with no verification needed
- It would be faster to just do it than to describe it
- It creates overhead without reducing risk

### Splitting Large Issues

When you find an oversized issue, split it using one of these patterns:

**By layer:** Extract data → Load data → Render data → Wire into UI
**By concern:** State management → Metal integration → Command dispatch
**By variant:** Enemy A → Enemy B → Enemy C (one per actor/entity)
**By phase:** Stub → Implement → Polish → Integrate

Keep the original as an umbrella issue (labeled `umbrella` or `needs-human`) that links to its children. Or cancel it and replace entirely.

### The Umbrella Pattern

For work that's too far in the future to decompose, create a single umbrella issue:

```
Title: Implement Water Temple — water levels, Longshot, Morpha boss
Labels: needs-human, umbrella
Description: Decompose into agent-sized issues when this milestone begins.
Key work:
- ...bullet list of major pieces...
```

Decompose it into agent-ready issues when the milestone is next up. Not before — requirements change.

---

## Dependencies and Parallelism

### Setting Blockers

A blocker means "this issue's branch cannot start until the blocker's PR is merged to main." Only set blockers when there's a true code dependency:

- Issue B modifies a file or API that Issue A creates → B blocked by A
- Issue B tests a behavior that Issue A implements → B blocked by A
- Issues A and B touch different modules with no shared interface → **no blocker, run in parallel**

**Common mistake:** Setting blockers based on logical sequence ("we should do A before B") rather than code dependency. If B can compile and pass tests without A's changes, there's no blocker.

### Maximizing Parallelism

Look for these patterns:

- **Extraction vs Runtime:** Data extraction issues and runtime implementation issues touch different modules. Run them in parallel.
- **Independent actors/entities:** Each enemy, NPC, or feature that has its own files can be implemented in parallel.
- **UI vs Core:** UI wiring and core logic often have clean boundaries. Parallel until the integration issue.
- **Test infrastructure:** Test setup, fixtures, and harness work is always parallel with feature work.

### The Integration Issue

After parallel work converges, create a small integration issue that wires the pieces together and runs the end-to-end verification. This is typically the last issue in a milestone.

---

## Verification Design

### Every Issue Needs Verification

No exceptions. "Manual testing" is acceptable for UI work, but be specific:

**Bad:** "Test that it works"
**Good:** "Launch app, press W, verify player position changes in debug sidebar"

### Verification Tiers

1. **Automated tests** — `swift test --filter XTests` or `npm test -- --grep "X"`. Preferred. Include exact commands.
2. **CLI verification** — `run-extractor --scene X && verify --content Y`. For pipeline/build tool work.
3. **Harness/script verification** — deterministic input script → capture output → assert state. For integration work.
4. **Manual verification** — "Launch app, do X, observe Y." Last resort, but acceptable for UI/UX. Be specific about what to look for.

### The Real-Source Rule

For parser, extractor, or integration work: **fixture tests are necessary but not sufficient.** If the issue touches real data parsing, require the agent to also run the real command against real source data before marking done.

Fixtures give false confidence. A fixture you invented will match your parser. Real data will surprise you.

### Verification Notes Format

Require agents to include this in their handoff:

```
Validation:
- `<command>` → pass/fail, one-line proof
- `<command>` → pass/fail, one-line proof

Docs:
- updated: `<path>` because `<reason>`
- or: not needed: `<reason>`

Follow-ups:
- none
- or: `<issue id>`: `<gap found during implementation>`
```

---

## Priority and Ordering

### Priority Levels

| Level | Meaning | Example |
|---|---|---|
| **Urgent (P1)** | Blocks other work or fixes a broken critical path | Fix keyboard input, implement core renderer |
| **High (P2)** | Key feature for the milestone's outcome | Player movement, NPC dialogue |
| **Medium (P3)** | Important but milestone works without it | Audio wiring, HUD polish |
| **Low (P4)** | Nice-to-have, do if time permits | Particle effects, ambient sounds |

### Execution Order Heuristic

Within a milestone, work this order:
1. P1 blockers (unblock everything else)
2. P2 issues that enable the most parallel downstream work
3. P2 issues on the critical path to milestone completion
4. P3/P4 as agents are available

---

## Labels

Use labels to communicate what an issue is and what state it's in:

| Label | Meaning |
|---|---|
| `agent-ready` | Scoped, unblocked, verification written. An agent can start now. |
| `needs-human` | Requires a human decision before agent work can begin |
| `umbrella` | Tracking issue replaced by smaller children |
| Module labels (`OOTCore`, `Backend`, etc.) | Which codebase area is affected |

### The `agent-ready` Checklist

An issue may carry `agent-ready` only when ALL of these are true:

- [ ] Title names one concrete outcome
- [ ] Scope and out-of-scope are explicit
- [ ] Dependencies are available in the repo
- [ ] Exact verification commands are written
- [ ] Issue is single-branch sized
- [ ] No unresolved architectural decisions

Remove `agent-ready` when any condition is false.

---

## The Agent Worker Contract

Define this once for your project and put it in AGENTS.md:

1. Pick one issue from the ready queue
2. Create a fresh branch from main
3. Implement only that issue
4. Run the issue's verification commands
5. Update docs if behavior or setup changed
6. Open or update the PR
7. Move the issue to review
8. **Stop.** Do not continue to the next issue.

### Rework Rules

When a reviewer sends an issue back:

- Continue on the existing branch and PR
- Address only the review findings
- Push a focused follow-up commit
- Only create a fresh branch if the PR was closed or the branch is corrupted

If the same issue bounces back 3+ times for the same failure, the issue's verification contract is broken. Tighten it before retrying.

---

## Common Anti-Patterns

### "Implement everything" issues
**Problem:** "Implement the user system" — too big, no clear done state.
**Fix:** Split into: registration, login, password reset, session management. Each independently verifiable.

### Silent failure tolerance
**Problem:** Code paths that return `nil` or `continue` without logging. Agent delivers "working" code that silently drops data.
**Fix:** Add a diagnostic logging issue early. Make silent failures visible before building on top of them.

### Fixture-only testing
**Problem:** Agent writes tests against invented fixtures that match the parser. Real data breaks it.
**Fix:** Require at least one test against real source data. Put the exact real-source command in the issue.

### Scope creep via "while I'm here"
**Problem:** Agent fixes the issue and also refactors neighboring code, adds error handling, improves types.
**Fix:** Out-of-scope section in every issue. "While I'm here" work goes into a follow-up issue.

### Blocker chains that prevent parallelism
**Problem:** Every issue blocks the next one in a long chain. Only one agent can work at a time.
**Fix:** Only set blockers for true code dependencies. Many issues that feel sequential can actually run in parallel because they touch different files.

---

## Milestone Decomposition Checklist

When starting a new milestone:

1. **Define the outcome** — what can the user see/do when this milestone is done?
2. **List the work** — brain dump everything needed
3. **Group by concern** — extraction, core logic, UI, integration, polish
4. **Split into issues** — each one passes the one-agent rule
5. **Set blockers** — only where there's a true code dependency
6. **Identify parallel tracks** — which issues can run simultaneously?
7. **Mark `agent-ready`** — only issues with full scope + verification
8. **Mark `needs-human`** — issues requiring architectural decisions
9. **Create an integration issue** — the last issue that proves the milestone works end-to-end

---

## Summary

| Principle | Rule |
|---|---|
| One agent, one issue, one branch | Never bundle independently reviewable work |
| Outcome-based milestones | "User can X" not "Sprint 4" |
| Fix → Feel → Feature | Make it work before making it bigger |
| Explicit verification | Exact commands, expected output, no ambiguity |
| Real-source testing | Fixtures are necessary but not sufficient |
| Parallel by default | Only block on true code dependencies |
| Decompose just-in-time | Umbrella issues for future work, detail when approaching |
| Out-of-scope is mandatory | Prevents agents from expanding into adjacent work |
