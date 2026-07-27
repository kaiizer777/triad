# Triad : Disk Hygiene & Retention

**Current state of project (July 2026):** v1 core, Workflow 2 (commands,
auto-commit, hooks, browser tools, subagents, web search), Workflow 3
(Orchestrator, modes, twin subagents, clarify phase, commit journey, memory +
self-learning, observability), and Workflow 4 (browser tool hardening) are
all complete and shipped. A disk-usage audit (see below) found several
locations that grow without any cap or retention policy, plus one
significant one-time git bloat issue from committed binaries. This document
fixes both, in small, single-session-sized phases — same discipline as
every prior workflow.

**Audit findings this document addresses (summary):**

| # | Location | Risk | Issue |
|---|---|---|---|
| 1 | `.git/objects/` (dist/ binaries) | HIGH, one-time | 38 MB of binaries committed to git history, bloating every clone permanently |
| 2 | `sessions/*.jsonl` (main, subagents, twins, traces) | HIGH, ongoing | No age-based cleanup, no file-count cap |
| 3 | `triad.log` | HIGH, ongoing | No rotation, no size cap, grows forever |
| 4 | `memory/daily/*.md` | HIGH, ongoing (deferred from Workflow 3) | Explicitly flagged as an open item — no auto-pruning |
| 5 | Git auto-commit objects, ongoing | MEDIUM, ongoing | No `git gc` ever runs, loose objects accumulate |

**Priority note, different from the audit's own suggested order:** the git
bloat issue (dist/ binaries in history) should be fixed **first**, before
anything else in this document, because every additional auto-commit made
while it remains unfixed adds to a repo that already needs its history
rewritten — the fix only gets more expensive the longer it's deferred.
Everything else here is ongoing hygiene and can be done in any order after
that.

---

## Phase 1 — Git History Cleanup (Do This First, Before Any Other Phase)

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
Phase 1 (Git History Cleanup)       ← mandatory, do first
Phase 2 (Session Retention)         ← independent
Phase 3 (Logger Rotation)           ← independent
Phase 4 (Memory Daily-Log Retention)← independent, closes a Workflow 3 open item
Phase 5 (Ongoing Git Hygiene)       ← independent, prevents Phase 1's issue recurring
```

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