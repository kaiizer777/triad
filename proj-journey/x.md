

---



---



---

## Phase 8 — Memory Structure (Storage Only, No Auto-Learning Yet)

**Goal:** get the index + topics + daily-log storage structure working and
tested, deliberately *before* adding the self-learning auto-extraction step
in Phase 9. Fully independent of Phases 1-7 — can be done any time, including
before them if you'd rather build memory first.

### 8.1 Design principle

**Small, curated, and structured beats large and comprehensive** — see
Phase 0 for the research this is grounded in.

### 8.2 Structure

```
memory/
├── INDEX.md              # pointer file, read every session, kept small
│                            (target: well under 150-200 lines)
├── preferences.md         # your personal preferences (style, communication)
├── daily/
│   └── 2026-07-25.md      # raw, append-only, verbatim, per-session log
└── topics/
    ├── architecture.md    # curated: key architecture decisions + why
    ├── conventions.md      # curated: naming/style/testing conventions
    └── <topic>.md          # curated, one file per recurring theme
```

- [ ] 8.2.1 — Implement `memory/INDEX.md` as the **only** file read
      automatically at the start of every session, regardless of mode. Keep
      it to short pointers + a handful of "quick facts," not full content
- [ ] 8.2.2 — Implement `memory/daily/<date>.md` as an **append-only**
      per-session log, written to at session end (or continuously, matching
      your existing "write immediately, don't batch" transcript philosophy)
- [ ] 8.2.3 — Implement `memory/topics/*.md` as **curated** files, updated
      deliberately (not automatically dumped) when a genuinely recurring
      pattern or important decision emerges
- [ ] 8.2.4 — Implement `memory/preferences.md` as a single small file for
      your personal preferences, separate from project facts
- [ ] 8.2.5 — Add a memory-read step at session start: load `INDEX.md` (and
      only `INDEX.md`) into context for whichever mode is active. Topic files
      are fetched on demand only if the index points to something relevant
      to the current task
- [ ] 8.2.6 — Add a manual memory-write path: a way for the human (or
      Coder/Reviewer, with human confirmation) to explicitly add an entry to
      a topic file — this is the manual path that Phase 9's `/learn` command
      will later build on top of, but it should work standalone first

### 8.3 Tests

- [ ] 8.3.1 — **Test:** confirm `INDEX.md` alone is what gets loaded at
      session start, and that topic files are only pulled in when the index
      or task explicitly points to them
- [ ] 8.3.2 — **Test:** run several sessions, confirm daily logs accumulate
      correctly without ever being edited/overwritten (append-only, same
      discipline as your JSONL transcript)
- [ ] 8.3.3 — **Test:** confirm the manual topic-file write path works
      correctly and doesn't corrupt existing entries in that file

**Checkpoint:** memory storage works — index, topics, daily log, manual
writes — but nothing is automatically extracted yet. That's Phase 9.

---

## Phase 9 — Self-Learning Loop

**Goal:** add the active extraction step that closes the gap versus Hermes/
pi-self-learning/CommandCode (see Phase 0). Depends on Phase 8's storage
structure already working.

### Why this phase exists

Phase 8 gives Triad a memory *structure* that already matches what current
tools converge on. What it doesn't yet have is an **active extraction
step** — a mechanism that actually mines corrections and mistakes out of a
session, rather than relying on you to manually decide what belongs in a
topic file (Phase 8.2.6's manual path). Three real 2026 tools solve this
differently, and each teaches something worth borrowing (see Phase 0 for
full detail): **pi-self-learning**'s extract-then-rank pattern is the direct
model here; **Hermes**'s skill-file generation and **CommandCode**'s
accept/reject style-tracking are both deliberately deferred (see the
non-goals list at the end of this document).

### 9.1 Design — auto-extract, but never auto-promote

- [ ] 9.1.1 — At the end of every session (or every completed task, if
      finer granularity proves useful), run a lightweight extraction pass
      over that session's transcript: look specifically for **Reviewer
      objections that were later resolved** (Workflow 1 §6.3 — the
      propose→object→revise cycle) and for any explicit correction the human
      gave (a human message that changed Coder's direction mid-task)
- [ ] 9.1.2 — Write each extracted item as a **raw entry in the daily log**
      (`memory/daily/<date>.md`, Phase 8.2.2) automatically — this part is
      safe to fully automate, since the daily log is explicitly the raw,
      unfiltered, append-only layer
- [ ] 9.1.3 — **Do NOT automatically promote extracted items into
      `topics/*.md` or `INDEX.md`.** This is the deliberate line that
      preserves the "small and curated beats comprehensive" principle from
      Phase 0/8.1
- [ ] 9.1.4 — Implement a **`/learn` review command**: at a natural
      checkpoint (session end, or on demand), Triad surfaces a short,
      human-reviewable digest of newly extracted daily-log items — e.g.
      *"3 corrections logged this session. Promote any to a topic file?"* —
      and the human decides what (if anything) gets promoted, building
      directly on the manual write path from Phase 8.2.6
- [ ] 9.1.5 — When something is promoted via `/learn`, write it to the
      relevant `topics/*.md` file in the same curated style as existing
      manual entries — dated, concise, one clear statement of the lesson

### 9.2 Tests

- [ ] 9.2.1 — **Test:** run a session with at least one Reviewer objection
      that gets resolved and one human mid-task correction, confirm both are
      correctly auto-extracted into that day's daily log with accurate
      before/after context
- [ ] 9.2.2 — **Test:** confirm `/learn` correctly surfaces only new/
      unreviewed extracted items (not ones already promoted or already
      dismissed in a prior `/learn` pass), and that declining to promote an
      item doesn't delete it from the daily log
- [ ] 9.2.3 — **Test:** confirm no code path exists that writes to
      `topics/*.md` or `INDEX.md` without going through an explicit `/learn`
      human decision — this is a correctness invariant worth a real test,
      given how central it is to avoiding the documented failure mode from
      Phase 0

**Checkpoint:** sessions now automatically surface candidate lessons for you
to review and promote, without ever silently bloating your curated memory
files.

---

## Phase 10 — Commit Journey Visualization

**Goal:** a read-only reporting feature over data you already produce. Fully
independent of every other phase in this document — can be done any time,
including first.

### 10.1 What this is (locked scope)

A **read-only reporting feature** over the `[triad] entry #N` /
`[triad:twin #<id>]` auto-commit history from Workflow 2 §2. Locked as:
chronological, linear timeline (not branch/revert-aware), rendered two ways
— a quick ASCII view inside the TUI, and a richer exportable HTML view.

### 10.2 Design

- [ ] 10.2.1 — Add a `/journey` slash command (same pattern as your existing
      `/summary` — check `internal/commands` for how built-in Go-backed
      commands are distinguished from user `.md` template commands)
- [ ] 10.2.2 — Data source: `git log` filtered to Triad-tagged commit
      messages (reuse the exact parsing/filtering logic already built for
      `/summary` — do not re-implement commit-message parsing a second time)
- [ ] 10.2.3 — **TUI rendering**: a simple ASCII node-and-line view, one
      commit per row, chronological, showing: short hash, timestamp,
      one-line description, and a marker distinguishing main-loop commits
      from twin-subagent commits (using your existing `lipgloss` styling
      conventions)
- [ ] 10.2.4 — **HTML export**: implement as a new command or flag (e.g.
      `/journey --export`) that writes a standalone HTML file with a nicer
      visual timeline of the same data — no new backend/server needed
- [ ] 10.2.5 — Decide and implement a simple, tasteful visual style for the
      HTML export — a good candidate to consult the `frontend-design`
      conventions for if built as an artifact-style page

### 10.3 Tests

- [ ] 10.3.1 — **Test:** run `/journey` on a session with a realistic mix of
      main-loop and twin-subagent commits, confirm both TUI and HTML views
      correctly distinguish the two and reflect accurate chronological order
- [ ] 10.3.2 — **Test:** run on a session with zero commits yet, confirm a
      clean "nothing to show yet" state in both renderings rather than an
      empty or broken view

**Checkpoint:** you can visualize your full commit history, in the TUI for a
quick glance and as an HTML export for deeper inspection, correctly
distinguishing twin-subagent work from main-loop work once Phase 6 exists.

---

## Suggested Overall Order

Phases 8 and 10 (Memory structure, Commit journey) have **zero dependency**
on Phases 1-7 (Orchestrator/twin-subagent/observability) and can be done
first, last, or interleaved — whichever fits your energy/interest on a given
session. Phase 9 depends on Phase 8. Phases 1→2→3→4→5→6→7 are meant to be
done in that exact order, since each depends on the state/mechanism the
previous phase built (Phase 7/Observability specifically needs Phase 6's
twin subagents to exist, since that's what creates the nested activity worth
tracing). A reasonable overall sequence:

```
Phase 10 (Commit Journey)       ← independent, good warm-up
Phase 8 (Memory Structure)      ← independent
Phase 9 (Self-Learning Loop)    ← depends on Phase 8
Phase 1 (Modes + /mode)         ← start of the Orchestrator chain
Phase 2 (Mismatch Notice)
Phase 3 (Clarify Phase)
Phase 4 (Orchestrator Routing)
Phase 5 (Routing Rubric)
Phase 6 (Twin Subagent)         ← highest risk, do it fresh
Phase 7 (Observability)         ← do right after Phase 6, while it's fresh
```

---

## Open Items (apply across phases)

- Decide whether twin-subagent commits (Phase 6.7) should be visually
  distinguished only in the commit journey view, or also get a distinct git
  branch — a branch would make true isolation stronger but reopens the
  "linear timeline only" scope decision from Phase 10
- Extraction quality for the self-learning loop (Phase 9.1.1) depends on
  `mimo-v2.5-free` correctly identifying which Reviewer objections/human
  corrections were actually meaningful lessons vs. routine back-and-forth —
  worth watching empirically in real use rather than trying to perfect the
  extraction prompt speculatively
- Decide retention/pruning policy for `memory/daily/` — unlike `INDEX.md` and
  `topics/`, daily logs are append-only and will accumulate indefinitely;
  decide whether old daily logs are ever archived/deleted or just left to
  grow (git history keeps them recoverable either way)
- Phase 7's trace log and Phase 9's self-learning extraction both read the
  same underlying transcripts for different purposes (cross-agent sequence
  vs. lesson-worthy corrections) — worth a quick check once both exist that
  they aren't duplicating parsing logic that could instead be shared

## Explicit Non-Goals (this entire document)

- No SQLite, no vector DB (ChromaDB or otherwise) for memory — deferred,
  revisit only if `.md`-based memory demonstrably stops scaling
- No automatic summarization/consolidation pipeline that rewrites daily logs
  into topic files without a human decision point (Phase 9's `/learn` gate
  is the permanent design, not a temporary stand-in)
- No Hermes-style **skill-file generation** (writing new reusable tool/
  workflow definitions from detected patterns) — Phase 9 stops at surfacing
  learnings for a human to promote; it does not go further and write new
  executable skills/commands on its own
- No CommandCode-style automatic accept/reject/edit style-profile tracking
  — worth revisiting once Phase 9's simpler extraction is proven in real use
- No full observability platform (OpenTelemetry, dashboards, external
  tracing services) in Phase 7 — the trace log is intentionally a flat,
  file-based, human-readable log consistent with the rest of Triad's design
  philosophy, not a production-grade tracing system