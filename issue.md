# Triad : Disk Hygiene & Plan-Gate Restoration

## Work Order — do these in sequence, top to bottom

Each step is one commit, builds on the previous, and has a verify command
you run before moving on. Times are estimates for a focused single-session
piece of work. **Do not skip the verify step — it catches the kind of
broken-build mess that derailed the previous attempt.**

| # | Step | What | Est. | Verify before moving on |
|---|------|------|------|-------------------------|
| 1 | **Phase 6.2** | `Loop.activeCycleTask` fix — one new field, four assignment sites, one read site. Unblocks the clarify regression standalone, even if the rest of Phase 6 never lands. | ~30 min | `go test ./internal/loop/... -run TestClarify -count=1 -v` (all green) |
| 2 | **Phase 6.6** | `TestTUI_ViewHeightOverflow` border-char fix — one-line test OR style change. Unblocks the TUI test suite. | ~5 min | `go test ./internal/tui/... -count=1 -timeout 60s` (all green) |
| 3 | **Phase 6.1** | Restore `transcript.Plan`, `PlanItem`, status constants, `EncodePlan`/`DecodePlan`, `AppendPlanSnapshot`. One file, no test changes. | ~20 min | `go build ./internal/transcript/...` and `go test ./internal/transcript/...` |
| 4 | **Phase 6.3** | Restore the rest of the loop-side gate: `pendingPlan`, `planBypassed`, `planPreTextCount`, `planGateDisabled` fields; `PlanRequiredForTask` + `extractPlanFromToolCall` + `extractPlanItemID` + `heuristicBindPlanItem` + `writePlanSnapshot` + `LatestApprovedPlan` + `markPlanItem{InProgress,Done}` + `SetPlanGateDisabled`; wire into `runActiveCycle`; restore `plan_test.go`. **Default: `planGateDisabled: true` in `New` so all pre-existing tests keep passing.** | ~90 min | `go test ./internal/loop/... -run TestPlan -count=1 -v` (8 plan tests green) AND `go test ./internal/loop/... -count=1` (no regressions) |
| 5 | **Phase 6.4** | Mirror the gate in the TUI: `currentPlan`/`planRequired`/`planBypassed`/`planPreTextCount` fields on `Model`, gate wiring in `update.go`, `submit_plan` tool branch, per-action `bindActionToPlanItem`, mark-in-progress on proposal, mark-done on result. | ~60 min | `go test ./internal/tui/... -count=1` (no regressions) |
| 6 | **Phase 6.5** | `renderProposedPlan` in `view.go` + `case transcript.TypeProposedPlan` in `renderTranscript`. | ~30 min | Manual: run the TUI, type a non-trivial task, confirm the plan card renders after Coder calls `submit_plan`, confirm checklist icons update as items move through pending → in_progress → done |
| 7 | **Phases 2, 3, 4, 5** | Disk hygiene (session retention, log rotation, daily-log archival, periodic `git gc`). **Fully independent of Phase 6 — do any time, in any order, or skip entirely if not a current concern.** | ~4 hrs total, can be split across many sessions | Each phase's checkpoint commands in its own section below |

**After step 6: project ships, TUI is safe to use on real coding projects.**

**If you only have 30 min:** do step 1 (Phase 6.2) and stop. Even alone
it removes the clarify-spin blocker and the project is no longer unsafe.

**If you have an hour:** do steps 1 + 2 + 3. End state: clean test
suites, transcript types in place, ready for the rest.

---

## TL;DR (start here)

**Current ship-blocker:** `TestClarify_ProceedUnblocksTriad` hangs because
the plan gate (Phase 6) misclassifies `/proceed` as a real task. Until
Phase 6.2 lands, the headless TUI path is unsafe to use on real coding
projects — every clarify reply will spin forever.

**Today, do this in order:**
1. **Phase 6.2** (alone) — `Loop.activeCycleTask` fix. One commit, one
   field, four assignment sites, one read site. ~30 min. Fixes
   `TestClarify_ProceedUnblocksTriad` and `TestClarify_RealAnswersUnblockToo`
   even if the rest of Phase 6 never lands.
2. **Phase 6.6** — `TestTUI_ViewHeightOverflow` border-char mismatch.
   One-line test fix. ~5 min. Unblocks `go test ./internal/tui/...`.
3. **Phase 6.1, 6.3, 6.4, 6.5** — restore the rest of the plan-gate
   feature. These can ship incrementally.
4. **Phases 2, 3, 4, 5** — disk hygiene. Independent of everything
   above; do any time, in any order.

**After the above, the project ships and you can use the TUI on your
main projects.**

---

## Current state of project (July 2026)

v1 core, Workflow 2 (commands, auto-commit, hooks, browser tools,
subagents, web search), Workflow 3 (Orchestrator, modes, twin
subagents, clarify phase, commit journey, memory + self-learning,
observability), and Workflow 4 (browser tool hardening) are all
complete and shipped.

**What's left to ship:**

- **Plan-First gate (Phase 6)** — the most important unfinished
  feature; needed for safe TUI use on real projects. See "Plan-First
  Gate / TUI Regressions" section below.
- **Disk hygiene (Phases 2-5)** — independent, ongoing maintenance.

A disk-usage audit (Phase 1) found several locations that grow without
any cap or retention policy, plus one significant one-time git bloat
issue from committed binaries (Phase 1 — already done). This document
fixes both, in small, single-session-sized phases.

**Audit findings (Phases 1-5 address):**

| # | Location | Risk | Issue | Phase | Status |
|---|---|---|---|---|---|
| 1 | `.git/objects/` (dist/ binaries) | HIGH, one-time | 38 MB of binaries in git history | 1 | ✅ done |
| 2 | `sessions/*.jsonl` (main, subagents, twins, traces) | HIGH, ongoing | No age-based cleanup, no file-count cap | 2 | ⬜ |
| 3 | `triad.log` | HIGH, ongoing | No rotation, no size cap, grows forever | 3 | ⬜ |
| 4 | `memory/daily/*.md` | HIGH, ongoing (deferred from Workflow 3) | No auto-pruning | 4 | ⬜ |
| 5 | Git auto-commit objects, ongoing | MEDIUM, ongoing | No `git gc` ever runs | 5 | ⬜ |

**Plan-gate findings (Phase 6 addresses):** see "Plan-First Gate /
TUI Regressions (Handoff)" section below.

**Current test status (`go test ./...`):**
- 13/15 packages pass
- `internal/tui` — fails on `TestTUI_ViewHeightOverflow` (pre-existing
  border-char mismatch, Phase 6.6 fixes)
- `internal/browser` — slow (~127s, likely pre-existing
  Playwright/Chromium env-dependent; verify on a clean run before
  assuming)

---

## Phase 1 — Git History Cleanup (DONE — reference only)

**Goal:** remove the committed `dist/` binaries from git history entirely,
stop future binaries from ever being committed, and confirm the repo is
clean before any more auto-commits pile onto it.

- [x] 1.1 — **Back up the repo first** (a plain copy of the whole
      `.git` directory, or push to a remote you control) — history rewriting
      is destructive and irreversible if something goes wrong
      ✅ Backed up to `triad-backup-20260727-154840/` before any changes
- [x] 1.2 — Add `dist/` (and any other build-output paths currently
      tracked, e.g. `bin/triad.js` if that's also a build artifact rather
      than source) to `.gitignore`, if not already present
      ✅ `dist/` added to .gitignore. `bin/triad.js` confirmed as source
      (1KB Node.js wrapper), not a build artifact — left tracked.
- [x] 1.3 — Use `git filter-repo` (not the older, deprecated
      `filter-branch`) to strip `dist/` from the *entire* git history, not
      just the current working tree — confirm `git filter-repo` is
      installed and check current usage syntax before running it, since a
      mistyped path filter can silently remove the wrong thing
      ✅ Ran `git filter-repo --path dist/ --invert-paths --force`.
      Three binaries removed: triad-darwin-arm64 (12MB), triad-darwin-x64
      (13MB), triad-linux-x64 (12MB). Total ~36MB of blobs purged.
- [x] 1.4 — After filter-repo runs, confirm `.git` size actually shrank
      (`du -sh .git` before/after) and that the working tree still builds
      and runs correctly — a history rewrite should never change what files
      currently exist on disk, only the history behind them
      ✅ .git: 23MB → 808KB (97% reduction). `go build ./...` passes.
      dist/ files preserved on disk. Original commit hash e6cd306 no longer
      in history.
- [x] 1.5 — Decide where built binaries should actually live going forward
      — GitHub Releases is the standard answer for distributing compiled
      binaries without bloating the source repo; confirm this fits your
      workflow before committing to it
      ✅ Decision: **GitHub Releases**. The `dist/` directory with platform
      binaries should be built in CI and attached as release artifacts.
      The `bin/triad.js` Node.js wrapper stays in source (it's 1KB and
      is the runtime entry point, not a build output).
- [x] 1.6 — If this repo has already been pushed to a remote (GitHub), a
      history rewrite requires a **force push**, and anyone else with a
      clone needs to re-clone rather than pull — confirm this is acceptable
      (likely fine for a solo project, but worth stating explicitly since
      force-pushing rewritten history is not reversible for existing clones)
      ✅ Solo project — force push is acceptable. Remote was behind local
      (no divergence). Will force-push after all Phase 1 tests pass.
- [x] 1.7 — **Test:** confirm `git log` no longer shows the binary-adding
      commit's original bloat, confirm a fresh `git clone` of the repo is
      meaningfully smaller than before
      ✅ `git log --all --oneline -- "dist/*"` returns nothing — zero
      commits touch dist/ in rewritten history. Total blob size: 5.6MB
      (was ~42MB). Original commit hash e6cd306 fully rewritten away.
      Cannot test fresh clone until force-push completes.
- [x] 1.8 — **Test:** run the full existing test suite after the rewrite to
      confirm nothing in the current working tree was accidentally altered
      by the filter operation
      ✅ `go test ./...` — 14 packages pass, 2 pre-existing failures
      (internal/loop: plan_test.go references undefined symbols; internal/tui:
      TestTUI_ViewHeightOverflow border rendering bug). Both confirmed
      pre-existing in backup copy. Zero new failures introduced by rewrite.

**Checkpoint:** git history no longer carries the committed binaries, future
binaries are gitignored, and the repo builds/tests cleanly post-rewrite.
This must be done before starting Phase 2, so ongoing auto-commits don't
keep adding to a repo that still needs this fix.

---

## Phase 2 — Session Transcript Retention

**Goal:** add age-based cleanup for the four JSONL locations that
currently have zero retention policy: main sessions, subagent transcripts,
twin-subagent transcripts, and trace logs.

- [ ] 2.1 — Add a configurable retention window (default: 30 days, per the
      audit's finding that files older than this are never re-read in
      normal operation) as a new config value, not hardcoded
- [ ] 2.2 — Implement a cleanup pass covering all four locations:
      `sessions/*.jsonl`, `sessions/subagents/*.jsonl`,
      `sessions/twins/*.jsonl`, `sessions/traces/*.jsonl` — checking file
      modification time (or a date embedded in the filename, whichever your
      current naming convention uses) against the retention window
- [ ] 2.3 — Decide and implement the actual cleanup action: straight
      deletion, or compress-and-archive (e.g. into
      `sessions/archive/<year>-<month>.tar.gz`) before deleting the
      originals — archiving is safer if you ever want to look back further
      than 30 days, at very low ongoing cost, since these are already small
      files
- [ ] 2.4 — Decide when this cleanup pass runs — on every startup (simplest,
      matches how `browser.IsChromiumInstalled()` already does a startup
      check), on a manual `/cleanup` command, or both. A manual command is
      good insurance even if startup-automatic is the default, so cleanup
      can be triggered on demand
- [ ] 2.5 — Confirm the **current or active session's own files are never
      touched** by this cleanup, even if somehow older than the window
      (e.g. a very long-running session) — only fully-completed, inactive
      sessions should ever be candidates for cleanup
- [ ] 2.6 — **Test:** create synthetic old session/subagent/twin/trace files
      with backdated timestamps, run cleanup, confirm only the ones past the
      retention window are removed/archived and everything else is
      untouched
- [ ] 2.7 — **Test:** confirm the active/current session is never
      accidentally cleaned up mid-use
- [ ] 2.8 — **Test:** if archiving was chosen over straight deletion,
      confirm the archive is valid and its contents match what was removed

**Checkpoint:** all four transcript-style JSONL locations have a real,
tested retention policy instead of growing forever, and the active session
is always protected from cleanup.

---

## Phase 3 — Logger Rotation

**Goal:** cap `triad.log` so it can never grow without bound.

- [ ] 3.1 — Decide the rotation strategy: simple rename-on-startup (current
      `triad.log` → `triad.log.1`, previous `.1` → `.2`, etc., keeping a
      small fixed number of generations) is the simplest fix and matches
      what the audit already suggested — a full rotation library
      (lumberjack-style) is a heavier dependency than this project likely
      needs for a single log file
- [ ] 3.2 — Implement a size check (not just a startup-time rotation) — if
      `triad.log` exceeds a configured cap (e.g. 10 MB) *during* a long-
      running session, not just at the next startup, it should rotate then
      too, so a single very long session can't blow past the cap before the
      next restart
- [ ] 3.3 — Set a maximum number of retained rotated files (e.g. keep
      `triad.log` through `triad.log.5`, delete anything older) so rotation
      itself doesn't become its own unbounded-growth problem
- [ ] 3.4 — **Test:** force the log past the size cap mid-session (write
      enough synthetic log entries), confirm rotation triggers correctly
      without losing in-flight log writes or corrupting the active file
- [ ] 3.5 — **Test:** confirm the retained-generations cap in 3.3 actually
      deletes the oldest rotated file once the limit is exceeded

**Checkpoint:** `triad.log` is capped in both size and rotation-history
depth — it can no longer grow without bound regardless of session length or
total lifetime usage.

---

## Phase 4 — Memory Daily-Log Retention

**Goal:** close the open item explicitly flagged in Workflow 3 (`memory/
daily/*.md` was deliberately built append-only with no pruning, and the
open item there says to decide a policy later — this is that later).

- [ ] 4.1 — Confirm the design principle from Workflow 3 §0/§8.1 still
      holds: this is about **retention/archival of old raw daily logs**,
      not about changing the append-only-during-normal-use behavior. Daily
      logs should still be written freely during real use; this phase only
      addresses what happens to them once they're old
- [ ] 4.2 — Implement compression (not deletion) for daily logs older than
      the same retention window used in Phase 2 (default 30 days) — e.g.
      gzip individual old daily files, or roll them into a monthly archive
      file. Compression, not deletion, is the right choice here since daily
      logs are the raw material Phase 8's `/learn` promotion flow reads from
      — losing them entirely removes the audit trail behind any already-
      promoted topic-file entries
- [ ] 4.3 — Confirm compressed/archived daily logs are excluded from the
      normal `/learn` extraction read path (Workflow 3 §9) — only
      recent, uncompressed daily logs should be scanned for new learnable
      items, since compressed history has presumably already been reviewed
- [ ] 4.4 — **Test:** create synthetic daily log files older than the
      retention window, run the archival pass, confirm they're compressed
      (not deleted) and no longer picked up by `/learn`'s extraction scan
- [ ] 4.5 — **Test:** confirm recent (within-window) daily logs are
      completely unaffected and still function exactly as before

**Checkpoint:** the open item from Workflow 3 is now closed — daily memory
logs have an explicit, tested archival policy, and the `/learn` flow
correctly ignores archived history.

---

## Phase 5 — Ongoing Git Hygiene

**Goal:** prevent the loose-object bloat pattern from Phase 1 from
recurring gradually over time, now that history has been cleaned once.

- [ ] 5.1 — Add a periodic `git gc --auto` invocation to the auto-commit
      path (Workflow 2 §2) — e.g. every N commits (50 is a reasonable
      starting point, matching the audit's own suggestion), not on every
      single commit, since `git gc` itself has a real cost and shouldn't run
      on every action
- [ ] 5.2 — Make this configurable (the commit-count interval, and whether
      it's enabled at all) rather than hardcoded, consistent with this
      project's existing config-over-code philosophy
- [ ] 5.3 — Confirm `git gc --auto` running mid-session doesn't interfere
      with or block the active approval loop — this should run
      asynchronously/in the background where possible, not stall Coder's
      next action while garbage collection happens
- [ ] 5.4 — **Test:** simulate reaching the commit-count threshold, confirm
      `git gc --auto` actually fires and `.git` object count/size trends
      down afterward (packed objects instead of accumulating loose ones)
- [ ] 5.5 — **Test:** confirm a `git gc` running mid-session doesn't cause
      any observable delay or failure in the next proposed action's
      execution

**Checkpoint:** git object bloat is now actively managed going forward, not
just fixed once in Phase 1 — the repo should stay lean across months of
continued daily use.

---

## Suggested Overall Order

Phase 1 is a hard prerequisite — do it before anything else, including
before any further real usage of the tool if possible, since every commit
made in the meantime adds to what eventually needs cleaning up. Phases 2-5
are independent of each other and can be done in any order once Phase 1 is
complete.

```
Phase 1 (Git History Cleanup)        ← DONE ✅
Phase 2 (Session Retention)          ← independent
Phase 3 (Logger Rotation)            ← independent
Phase 4 (Memory Daily-Log Retention) ← independent, closes Workflow 3 open item
Phase 5 (Ongoing Git Hygiene)        ← independent, prevents Phase 1 recurring
Phase 6.1 (transcript types)         ← do first in Phase 6
Phase 6.2 (activeCycleTask fix)      ← do second, alone — unblocks
                                       TestClarify_ProceedUnblocksTriad
                                       even if the rest of Phase 6 never lands
Phase 6.3 (loop-side gate)           ← do third
Phase 6.4 (TUI model + update)       ← do fourth
Phase 6.5 (plan card render)         ← do fifth
Phase 6.6 (TUI border test fix)      ← do last, do once
```

(See the TL;DR at the top for the recommended work order. Phases 2-5
are independent of Phase 6 and can be done any time after Phase 6.2
ships.)

---

## Open Items

- ✅ **RESOLVED (Phase 1):** Repo has been pushed to GitHub (kaiizer777/triad).
  Force push is acceptable — solo project, no other contributors. Will
  force-push after this commit lands.
- Decide the exact retention window number (30 days is the audit's
  suggestion, based on "nothing older is re-read in normal operation" — but
  confirm this matches your actual usage pattern before locking it in)
- Decide whether archived/compressed data (Phase 4) should ever be fully
  deleted after some much longer period (e.g. 1 year), or kept indefinitely
  in compressed form — this document only specifies compression, not
  eventual deletion

---

# Plan-First Gate / TUI Regressions (Handoff)

**Why this section exists:** the plan-first (submit_plan) gate is the
remaining unfinished Workflow 4-ish feature needed to make the TUI safe
to use on real coding projects. A previous session implemented most of it
but left the project in a non-shippable state: the loop and tui had
half-applied changes, the build was broken, and two real test
regressions were unresolved.

**This section is the single source of truth for finishing that work
in small, verifiable steps. Each phase below must end with a green
`go test ./internal/loop/...` and `go test ./internal/tui/...` before
the next one starts.**

**Current state (post-revert):**
- `go build ./...` — clean
- `go test ./internal/loop/...` — passes (all green, ~13s)
- `go test ./internal/tui/...` — fails on `TestTUI_ViewHeightOverflow`
  ONLY (border-char mismatch: test expects `╰`/`─`/`╯`, `lipgloss.ThickBorder()`
  renders `┗`/`┛`). This is **pre-existing** — the other agent's
  Phase 1 audit (this file, line 88-91) confirmed it. Not caused
  by the plan-gate work.
- `go test ./internal/browser/...` — slow / 127s timeout. Almost
  certainly pre-existing (Playwright/Chromium env-dependent). The
  other agent's audit only explicitly named the two failures above,
  so treat this as "pre-existing but not yet confirmed" — check it
  on a clean run before assuming.

**What's missing from the tree right now (everything below must be
re-applied from scratch — none of it is in `main`):**

| # | File | What was there | Why it has to come back |
|---|------|----------------|-------------------------|
| 1 | `internal/transcript/entry.go` | `TypeProposedPlan` constant, `Plan` and `PlanItem` structs, `PlanItemPending/InProgress/Done` status constants, `EncodePlan`/`DecodePlan` helpers, `AppendPlanSnapshot` helper | Gate cannot write plan snapshots without these — they're the wire format |
| 2 | `internal/loop/loop.go` | Plan-gate wiring in `runActiveCycle` (submit_plan branch, plan-rejection branch, pre-plan stall-guard, per-action `bindActionToPlanItem` / `markPlanItemInProgress` / `markPlanItemDone`), `PlanRequiredForTask` package helper, `extractPlanFromToolCall` / `extractPlanItemID` / `heuristicBindPlanItem` / `writePlanSnapshot` / `LatestApprovedPlan` helpers, `activeCycleTask` field (see root-cause below), `clearCycleState` defer | Without (1)+(2) the gate doesn't exist; the rest of the codebase is waiting for these |
| 3 | `internal/tui/model.go` | `currentPlan`, `planRequired`, `planBypassed`, `planPreTextCount` fields, `SetPlanGateDisabled` (TUI mirror), `RestoreSessionState` recovery via `loop.LatestApprovedPlan` | TUI needs to render plan card and gate Coder's first move |
| 4 | `internal/tui/update.go` | Plan gate wiring at cycle start, `submit_plan` tool-call branch, non-plan tool rejection, item binding in Coder-response handler, mark-done in `toolResultMsg` handler, TUI-side `bindActionToPlanItem` | TUI's per-action gate |
| 5 | `internal/tui/view.go` | `renderProposedPlan` function, `case transcript.TypeProposedPlan` in `renderTranscript` switch | Visible plan card in the chat |
| 6 | `internal/loop/plan_test.go` | 8 gate tests (TestPlanRequired_TrivialBypass, TestExtractPlanItemID_*, TestHeuristicBindPlanItem, TestPlanFieldWasPresent, TestMarkPlanItemStatusInPlace, TestLatestApprovedPlan_*, TestPlanResumeFromJSONL, TestPlanGate_NonTrivialTriadTaskRequiresPlan, TestPlanGate_PlanObjectionTriggersRevision, TestPlanGate_PlanRejectionWhenStalling, TestPlanGate_TrivialTaskBypassesPlanInTriadMode, TestPlanGate_HeuristicBindingEmitsComplianceNote) | These were the only thing that proved the gate worked — must pass before any "ship" claim |

**Root cause of `TestClarify_ProceedUnblocksTriad` (the real bug
worth fixing regardless of the rest):**

The plan gate's `requiresPlan()` reads
`Loop.mostRecentHumanTask()`, which walks the transcript from the
end and returns the latest `[You]` entry. After a clarify round
followed by `/proceed`, the latest `[You]` is `/proceed` — not
the original task. `ClassifyTask("/proceed")` returns
`TierMiddle` because the string contains `/` (treated as a path
separator), so `planRequired = true`, the gate rejects the
`task_complete` call, and the loop spins forever re-prompting
Coder.

**Minimum fix (do this first, do it alone, do not touch anything
else in the same commit):** add a `Loop.activeCycleTask string`
field. Set it in `Run()` when a fresh task arrives in the
non-orchestrator path, in the orchestrator routing path, and
preserve it across clarify replies. In `runActiveCycle`, read
`activeCycleTask` for the plan-classification; fall back to
`mostRecentHumanTask()` only on resume (where `activeCycleTask`
is empty until a fresh message arrives). Then
`TestClarify_ProceedUnblocksTriad` and
`TestClarify_RealAnswersUnblockToo` both pass without changing
the test fixtures.

A previous attempt landed this fix and the test passed — verify
it before re-attempting the rest of the gate.

**Root cause of `TestTUI_ViewHeightOverflow` (NOT a real
regression):** the test asserts the bottom of the view contains
`╰`, `─`, or `╯`. The current `RightCardContainer` and
`SidebarContainer` styles use `lipgloss.ThickBorder()` which
renders `┗` and `┛`. This is a test/style mismatch, not
regression from the plan-gate work. Two possible fixes, pick
one when convenient:
  (a) update the test's expected-chars to include `┗` and `┛`
  (b) change `RightCardContainer` / `SidebarContainer` to a
      rounded border (e.g. `lipgloss.RoundedBorder()`)
Either is fine — fix is independent of the plan gate.

---

## Phase 6 — Plan-First Gate Restoration (Do Phase 6.1 First, Alone)

**Goal:** get the headless plan-gate work back into the tree, with
all 8 plan tests green, without breaking any existing test.

### Phase 6.1 — Restore the transcript types (one file, one commit)

- [ ] 6.1.1 — Re-add to `internal/transcript/entry.go`:
      - `TypeProposedPlan` constant
      - `Plan` struct (`Revision int`, `Items []PlanItem`, with JSON tags)
      - `PlanItem` struct (`ID int`, `Text string`, `Status string`)
      - Status constants: `PlanItemPending = "pending"`,
        `PlanItemInProgress = "in_progress"`, `PlanItemDone = "done"`
      - `EncodePlan(p *Plan) (string, error)` and
        `DecodePlan(s string) (*Plan, error)` helpers
      - `AppendPlanSnapshot(t *Transcript, p *Plan, reason string) error`
        that writes a `TypeProposedPlan` entry with the JSON-encoded
        plan as Content
- [ ] 6.1.2 — `go build ./internal/transcript/...` must pass
- [ ] 6.1.3 — `go test ./internal/transcript/...` must stay green

**Checkpoint:** transcript package builds and tests pass. Nothing
else has changed. STOP HERE before Phase 6.2.

### Phase 6.2 — Restore `Loop.activeCycleTask` (the minimum-viable fix)

**This is the highest-value commit of Phase 6 because it unblocks
`TestClarify_ProceedUnblocksTriad` standalone, even if the rest of
the gate never lands. Do it before any other plan-gate work.**

- [ ] 6.2.1 — Add `activeCycleTask string` field to `Loop` struct
- [ ] 6.2.2 — In `Run()`, in the orchestrator-routing branch: set
      `l.activeCycleTask = msg` BEFORE calling `runOrchestratorRouting`
- [ ] 6.2.3 — In `Run()`, in the orchestrator-confirm-reply branch:
      capture the original task from `l.pendingOrchestratorConfirm.task`
      into `l.activeCycleTask` BEFORE calling `resolveOrchestratorConfirm`
      (which clears the pending struct)
- [ ] 6.2.4 — In `Run()`, in the non-orchestrator clarify branch:
      only set `l.activeCycleTask = msg` when `l.pendingClarify == nil`
      (i.e. it's a fresh task, not a clarify reply). Replies leave
      the previously-set value in place
- [ ] 6.2.5 — In `runActiveCycle`, replace `activeTask :=
      l.mostRecentHumanTask()` with `activeTask := l.activeCycleTask; if
      activeTask == "" { activeTask = l.mostRecentHumanTask() }` so
      resume (where `activeCycleTask` is empty) still works
- [ ] 6.2.6 — `go test ./internal/loop/... -run TestClarify -count=1`
      must show all clarify tests green, including
      `TestClarify_ProceedUnblocksTriad` and
      `TestClarify_RealAnswersUnblockToo`

**Checkpoint:** clarify suite is fully green. STOP HERE before
Phase 6.3.

### Phase 6.3 — Restore the rest of the loop-side plan gate

- [ ] 6.3.1 — Add to `Loop` struct: `pendingPlan *transcript.Plan`,
      `planBypassed bool`, `planPreTextCount int`,
      `planGateDisabled bool` (last one defaults to `true` — see
      the "opt-in gate" note below)
- [ ] 6.3.2 — Add `SetPlanGateDisabled(bool)` method
- [ ] 6.3.3 — Add package helpers: `PlanRequiredForTask(mode, task)
      bool`, `extractPlanFromToolCall(tc, revision) (*Plan, error)`,
      `extractPlanItemID(args, plan) (int, bool)`,
      `heuristicBindPlanItem(plan) (int, bool)`,
      `writePlanSnapshot(plan, reason) error`,
      `LatestApprovedPlan(entries) *Plan`,
      `markPlanItemInProgress(id) error`,
      `markPlanItemDone(id) error`
- [ ] 6.3.4 — In `runActiveCycle`, at the top: recover plan from
      transcript if `pendingPlan == nil`, classify the original
      task to compute `planRequired`, reset pre-text count, set
      `planBypassed` if gate is skipped
- [ ] 6.3.5 — In `runActiveCycle`'s plain-text branch: bump
      `planPreTextCount` and trip a stall guard after
      `maxPlanPreTextMessages` (1) plain-text messages without a
      plan
- [ ] 6.3.6 — In `runActiveCycle`'s tool-call branch: handle
      `submit_plan` (decode plan, write snapshot, run reviewer
      gate, set `pendingPlan` on approval)
- [ ] 6.3.7 — In `runActiveCycle`'s tool-call branch: add the
      plan-required gate `if planRequired && pendingPlan == nil
      && !planGateDisabled { reject; continue }` — the
      `!planGateDisabled` clause is what keeps pre-existing tests
      passing without per-test opt-outs
- [ ] 6.3.8 — In `runActiveCycle`'s tool-call branch: after
      approval, `bindActionToPlanItem` and `markPlanItemInProgress`
      on the bound item; on successful execution,
      `markPlanItemDone`. Both write a snapshot via
      `AppendPlanSnapshot`
- [ ] 6.3.9 — Add `defer l.clearCycleState()` at the top of
      `runActiveCycle` to reset all per-cycle plan state
- [ ] 6.3.10 — Restore `internal/loop/plan_test.go` (8 tests, see
      table above). With the `planGateDisabled: true` default
      from 6.3.1, `makeTestLoopWithMode` must call
      `l.SetPlanGateDisabled(false)` to enable the gate for the
      gate-active tests

**Checkpoint:** all 8 plan tests green AND all pre-existing
loop tests still green. STOP HERE before Phase 6.4.

### Phase 6.4 — Mirror the gate in the TUI (model + update)

- [ ] 6.4.1 — In `internal/tui/model.go` add fields:
      `currentPlan *transcript.Plan`, `planRequired bool`,
      `planBypassed bool`, `planPreTextCount int`
- [ ] 6.4.2 — In `NewModel` / `RestoreSessionState`, call
      `loop.LatestApprovedPlan(entries)` and populate
      `currentPlan`
- [ ] 6.4.3 — In `internal/tui/update.go`, at the start of every
      active cycle (in the humanInputMsg handler): recover
      `currentPlan`, reset `planPreTextCount`, compute
      `planRequired = loop.PlanRequiredForTask(m.currentMode,
      msg.content)`
- [ ] 6.4.4 — In `update.go`, add a `submit_plan` branch in the
      tool-call handling
- [ ] 6.4.5 — In `update.go`, add the plan-rejection branch
      (mirrors loop's: emit a System note and a revised
      `proposed_action`, don't burn a retry)
- [ ] 6.4.6 — In `update.go`, after a `proposed_action` is
      emitted, call `m.bindActionToPlanItem(toolCall)` and
      `loop.MarkPlanItemStatusInPlace(m.currentPlan, id,
      transcript.PlanItemInProgress)`; on successful execution
      in the `toolResultMsg` handler, mark done
- [ ] 6.4.7 — TUI plan tests (none in main yet — add a small one
      in `internal/tui/tui_test.go` for at least:
      `currentPlan` recovered on resume from a TypeProposedPlan
      entry, and plan-gate rejection when `planRequired &&
      currentPlan == nil`)

**Checkpoint:** TUI model and update paths handle plans. STOP
HERE before Phase 6.5.

### Phase 6.5 — Render the plan card in the TUI

- [ ] 6.5.1 — In `internal/tui/view.go`, add
      `renderProposedPlan(content string, width int) string` —
      uses `transcript.DecodePlan`, renders a header pill
      (`▸ PLAN` or `▸ PLAN (revised from initial · #N)` for
      revisions), a separator rule, a progress count
      (`N/M done`), and one line per item with `▢` / `▷` / `✓`
      icons matching item status
- [ ] 6.5.2 — In `renderTranscript`, add a `case
      transcript.TypeProposedPlan: body =
      m.renderProposedPlan(cleanContent, m.viewport.Width())` —
      same height budget as `renderProposedAction` for a
      comparable content size, no extra trailing blank lines
- [ ] 6.5.3 — Manually verify in the running TUI that a plan
      entry shows up after Coder calls `submit_plan` and that the
      checklist updates as items move through `pending` →
      `in_progress` → `done`

**Checkpoint:** plan card is visible in the TUI. STOP HERE.

### Phase 6.6 — Fix the pre-existing `TestTUI_ViewHeightOverflow`

Pick ONE of:
- [ ] 6.6a — Update the test's expected border chars to include
      `┗` and `┛` (the chars `lipgloss.ThickBorder()` actually
      renders). One-line change.
- [ ] 6.6b — Change `RightCardContainer` and `SidebarContainer`
      in `model.go` `DefaultStyles()` to use
      `lipgloss.RoundedBorder()` so the rendered chars are `╰`
      and `╯`. May need a quick visual check against the design
      language (the project's "premium / editorial" theme
      already uses rounded borders elsewhere — verify).

**Checkpoint:** `go test ./internal/tui/...` is fully green.
Phases 2-5 of the disk-hygiene doc can then proceed.

---

## Opt-in vs always-on gate (design decision, locked)

The previous attempt made the gate default to OFF in
`Loop.New` (`planGateDisabled: true`) and used
`SetPlanGateDisabled(false)` to opt in. This was the right call
because:

- It keeps every pre-existing test passing without per-test
  opt-out lines (15+ test files would otherwise need
  `l.SetPlanGateDisabled(true)` added individually).
- The headless loop is only used by tests; production goes
  through the TUI which has its own gate in `update.go` that
  is always on.
- Future code that adds a non-test headless caller just needs
  to call `SetPlanGateDisabled(false)` once on construction.

**Don't flip the default.** If you want the gate always-on
later, the right move is a build tag (`//go:build production`)
on a separate constructor, not a behavior change to `New`.

---

## Pitfalls (learned the hard way in the previous attempt)

- **Don't refactor the constructor and the struct field list
  in the same edit.** The previous attempt accidentally removed
  5 struct fields (`pendingPlan`, `planBypassed`,
  `planPreTextCount`, `activeCycleTask`, `planGateDisabled`)
  by over-zealously replacing a block. Always re-read the
  struct after a constructor change to confirm the fields are
  still there.
- **Don't bulk-update inline test constructors mid-debug.**
  Adding `l.SetPlanGateDisabled(true)` to 10+ inline
  `l := loop.New(...); l.CurrentMode = ...` blocks is
  error-prone and can mask the actual problem. The opt-in
  default in `New` makes this unnecessary.
- **Verify the build after every single edit, not after every
  phase.** `go build ./...` takes 2 seconds and catches the
  struct-mismatch class of bug immediately.
- **One commit per phase.** If a phase is too big to commit
  cleanly, the phase is too big. Split it.
- **Don't run `mavis-trash` on `internal/loop/plan_test.go` and
  expect to find it again.** It goes to the Windows Recycle Bin
  under `C:\$Recycle.Bin\S-1-5-21-...` and finding it is
  non-trivial. Move it instead.
- **The other agent's Phase 1 history rewrite nuked the
  un-committed plan-gate work.** That's the root cause of why
  everything looked like it had "disappeared" mid-session. If
  the other agent has to run `filter-repo` again in the future,
  expect any in-flight un-committed work to be lost — commit
  early and often, especially across long sessions.

---

## Suggested Order (recap)

```
Phase 1 (Git History Cleanup)        ← DONE ✅
Phase 2 (Session Retention)          ← independent
Phase 3 (Logger Rotation)            ← independent
Phase 4 (Memory Daily-Log Retention) ← independent
Phase 5 (Ongoing Git Hygiene)        ← independent
Phase 6.1 (transcript types)         ← do first
Phase 6.2 (activeCycleTask fix)      ← do second, alone — unblocks
                                      TestClarify_ProceedUnblocksTriad
                                      on its own
Phase 6.3 (loop-side gate)           ← do third
Phase 6.4 (TUI model + update)       ← do fourth
Phase 6.5 (plan card render)         ← do fifth
Phase 6.6 (TUI border test fix)      ← do last, do once
```

**Ship-blocker priority:** 6.2 alone is enough to remove the
clarify-regression that's currently blocking daily TUI use of
the headless path. Everything else is the plan-gate feature,
which can ship incrementally after 6.2 lands.

---

## Verification commands (cheat sheet)

Use these to confirm each phase is actually done:

```bash
# After every edit — catches struct-mismatch / missing-import bugs immediately
go build ./...

# After any loop change
go test ./internal/loop/... -count=1 -timeout 60s

# After any TUI change
go test ./internal/tui/... -count=1 -timeout 60s

# After any plan-gate change
go test ./internal/loop/... -run TestPlan -count=1 -v

# The clarify regression — must pass after Phase 6.2 lands
go test ./internal/loop/... -run TestClarify_ProceedUnblocksTriad -count=1 -v

# Full suite — should be fully green after Phase 6.6 lands
go test ./... -count=1 -timeout 120s
```

---

## Open Items (cross-phase, still unresolved)

- **Retention window (Phase 2):** the audit suggested 30 days based on
  "nothing older is re-read in normal operation." Confirm this matches
  your actual usage pattern before locking it in during Phase 2.1.
- **Archived-data eventual deletion (Phase 4):** this document only
  specifies compression of old daily logs, not eventual deletion. Decide
  whether compressed data should be fully deleted after some much
  longer period (e.g. 1 year) or kept indefinitely in compressed form.
- **`internal/browser` 127s test timeout:** not yet confirmed as
  pre-existing. Verify on a clean run before assuming; if it's a
  regression, fix before Phase 6.6 so the final `go test ./...` is
  fully green.
- **Phase 1 force-push:** history rewrite completed locally but not
  yet pushed to the GitHub remote. Do the force-push once you're ready
  to publish.