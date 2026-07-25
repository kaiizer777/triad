# Triad — Workflow 3: Orchestrator, Commit Journey & Memory

**Current state of project (July 2026):** v1 core and Workflow 2 (slash
commands, git auto-commit, hooks, browser tools, subagents, web search) are
all complete and shipped — 206+ tests passing, clean build. This document
covers the **next architectural layer**: an Orchestrator mode that routes
tasks by complexity/severity, a "twin subagent" pattern (an isolated mini
Coder+Reviewer pair), a mandatory upfront clarify phase across all modes, a
visual commit-journey report, and a markdown-based project/preference memory
system with a self-learning loop. This is the most architecturally
significant phase yet.

**Chunking note:** this document is split into **10 small phases**, each
sized to be completable (design review + implementation + tests) in roughly
one focused session, following the same discipline as Workflow 1 and
Workflow 2. Do not combine phases into one session even if you have extra
time — finishing one phase cleanly, with its own tests passing, is more
valuable than starting two. Each phase ends with a "Checkpoint" telling you
exactly what should be true before moving on.

---

## Phase 0 — Research Grounding (read only, no code)

**Goal:** understand *why* the phases below are shaped the way they are,
before touching any of them. No implementation in this phase — just context.

Current (2026) multi-agent orchestration practice and research converge on a
few hard-won points that directly shape the design below:

- **Dynamic/LLM-based routing is inherently non-deterministic** — the same
  input can produce different agent chains on different runs, which makes
  debugging materially harder than deterministic pipelines. The mitigation
  the field has converged on is **traceability**: every routing decision must
  be logged as a first-class, inspectable event, not inferred after the
  fact. This is why Phase 4 below makes "log every routing decision" a hard
  requirement, not a nice-to-have.
- **"Use the simplest topology that fits the task; add coordination
  controls only as coupling and autonomy rise."** This is the industry's
  own stated principle for when to add hierarchy/oversight — and it's the
  same principle you independently arrived at when you locked "confirm by
  default, autonomous only at the two extremes" for Orchestrator's routing.
  Phase 4 follows this explicitly: trivial tasks get the lightest topology
  (General Chat, no orchestration overhead), critical tasks get the heaviest
  (full Triad), and only the genuine middle ground pays the cost of a
  routing decision at all.
- **Context/memory files can actively hurt if they're large or
  LLM-generated.** A 2026 ETH Zurich / LogicStar.ai study found that both
  LLM-generated and even most developer-written context files reduced task
  success rates and increased inference cost by 20%+ compared to no context
  file at all, when they included anything beyond the minimum the agent
  couldn't otherwise infer. The finding: **small, aggressively curated,
  human-reviewed files help; large accumulating ones hurt.** This directly
  shapes Phases 7-8 — Triad's memory system is index + topic files, not one
  growing document, and staleness/pruning is a designed-in feature, not an
  afterthought.
- **The dominant real-world pattern for agent memory in 2026 is "daily log +
  curated topic files + small index," all in plain markdown, read at session
  start and written at session end** — no database, no vector search, by
  default. Several independent open-source projects (agentmemory,
  agent-context-system, memsearch) converge on essentially the same shape.
  This validates your "keep it .md for now" decision and gives us a concrete
  structure to copy rather than invent from scratch.
- **Named self-learning tools researched directly for this document**
  (Hermes Agent by Nous Research, pi-self-learning, CommandCode) confirm the
  structure above is right but reveal a real gap: none of them stop at
  passive storage — each has an **active extraction step** that mines
  corrections/mistakes/deviations out of a session automatically. pi-self-
  learning is the closest architectural match (pure git-backed markdown,
  extracts "what went wrong and how it was fixed" after every task, keeps a
  ranked core-learnings file distinct from raw daily logs) and is the direct
  model for the self-learning loop in Phase 9. Hermes goes further and
  writes reusable skill files from detected patterns; CommandCode tracks
  accept/reject/edit behavior into a persistent style profile. Both of those
  further steps are deliberately deferred (see the non-goals list at the end
  of this document) — this document adds extraction-and-surfacing, not
  autonomous skill-writing.
- **Multi-agent systems cost far more than they look like they should, and
  fail in specific, well-documented ways.** Anthropic's own published data
  shows multi-agent systems use approximately **15x the tokens** of a
  single-agent chat interaction — directly relevant given `mimo-v2.5-free`'s
  already-uncertain, unpublished free-tier rate limit. Separately, a
  cross-framework failure-taxonomy study (spanning AutoGen, CrewAI, and
  LangGraph) found **coordination failures account for ~37% of all
  multi-agent failures**, while a related analysis found **vague/incomplete
  task specifications cause the single largest failure category at ~42%** —
  larger than coordination bugs themselves. This is the concrete
  justification for Phase 3's clarify step and Phase 5's routing rubric
  existing as their own dedicated phases rather than afterthoughts, and it's
  why **Phase 7 (Observability)** exists at all: once Phase 6's twin
  subagents create a second, nested layer of nearly-invisible agent activity
  on top of the main loop, "I can't tell what actually happened" becomes the
  dominant risk, not any single logic bug.
- **The pattern that survived 2026 production use, across five major
  vendors (Anthropic, OpenAI, AutoGen, Cognition, LangChain) independently
  converging on it:** a dedicated system prompt per subagent (never reusing
  the parent's), a single structured brief as the first message (not
  free-form delegation), and a **summary string returned to the parent, not
  the full sub-transcript** — inlining a full transcript back into the
  parent both burns tokens at the 15x rate and pollutes the parent's
  context. Free-form "everyone in one shared thread" delegation (GroupChat-
  style) was tried industry-wide and lost to this isolated pattern. This
  directly validates the Twin Subagent design in Phase 6 as already
  following the pattern that won, not an experimental one.

**Checkpoint:** you understand why routing decisions must be logged, why
memory is structured as small files rather than one big one, what gap the
self-learning loop closes versus Hermes/pi-self-learning/CommandCode, and
why observability gets its own dedicated phase rather than being folded
into Twin Subagent's checklist. Nothing to test — move to Phase 1 when
ready.

---

## Phase 1 — Modes Foundation & the `/mode` Command

**Goal:** get the three-mode concept and the sticky mode-switching mechanism
working, before Orchestrator's actual routing intelligence exists. This lets
you manually test General Chat and Triad mode-switching in isolation first.

### 1.1 The three modes — what each one actually is

| Mode | What runs | When |
|---|---|---|
| **Orchestrator** (default) | A new top-level agent that receives every task first, judges complexity/severity, and routes to one of the other two modes (or handles trivial cases itself) | Default session mode |
| **General Chat** | A single agent (Coder-equivalent, no Reviewer, no approval loop) for genuinely simple, low-stakes requests | Routed to by Orchestrator, or selected directly by the human |
| **Triad** | The existing full propose→review→execute loop with Reviewer veto power | Routed to by Orchestrator for critical work, or selected directly, or reached via a "twin subagent" spawned by Orchestrator for medium tasks |

Critically: **General Chat and Triad already exist as concepts in your
codebase** — General Chat is essentially Coder-without-Reviewer-and-without-
the-loop, and Triad is what you've already built and hardened across two
full workflow phases. Phase 1 is mostly plumbing a mode switch between things
you already have; Orchestrator's actual judgment comes later in Phase 4.

### 1.2 Design

- [x] 1.2.1 — Add `current_mode` (`orchestrator` | `general` | `triad`) as a
      new field in session state, persisted alongside existing session data
      so it survives resume (Workflow 1 §7) — not just an in-memory flag
- [x] 1.2.2 — Implement General Chat mode as its own code path: a single
      agent call with no Reviewer and no approval loop — this may already be
      trivially close to what Coder-alone would look like; confirm and reuse
      rather than building a parallel implementation
- [x] 1.2.3 — Implement `/mode <name>` as a Go-backed built-in command (same
      category as `/summary` — check `internal/commands` for how built-in
      commands are distinguished from user `.md` template commands and
      follow that pattern). Setting a mode prints a confirmation, e.g.
      `"Mode set to: Triad — Orchestrator will not route until you change
      this."`
- [x] 1.2.4 — Implement `/mode` with no argument as a read-only report of the
      current mode — no state change
- [x] 1.2.5 — **Mode is sticky by design**: once set via `/mode general` or
      `/mode triad`, every subsequent task runs directly in that mode until
      the human explicitly runs `/mode` again. `/mode orchestrator` restores
      default routing behavior (routing logic itself lands in Phase 4 — for
      now, `orchestrator` mode can simply default to Triad as a placeholder,
      since Orchestrator's judgment doesn't exist yet)
- [x] 1.2.6 — **Forced mode is never silently overridden** by anything —
      confirm this holds even before Orchestrator's judgment logic exists,
      since it's a property of the mode-switching mechanism itself, not of
      Orchestrator

### 1.3 Tests

- [x] 1.3.1 — **Test:** `/mode general` followed by a task runs the single-
      agent path with no Reviewer/approval loop involved
- [x] 1.3.2 — **Test:** `/mode triad` followed by a task runs the existing
      full approval loop, unchanged from Workflow 1/2 behavior
- [x] 1.3.3 — **Test:** set a mode, kill and resume the session (Workflow 1
      §7 resume logic), confirm `current_mode` is correctly restored
- [x] 1.3.4 — **Test:** confirm `/mode orchestrator` and `/mode` (no
      argument) both behave as specified

**Checkpoint:** you can manually switch between General Chat and Triad mid-
session via `/mode`, the choice persists across resume, and nothing about
Triad's existing behavior changed. Orchestrator itself still does nothing
smart yet — that's Phase 4.


## Phase 2 — Mode Mismatch Notice

**Goal:** add the passive FYI note for when a forced mode looks mismatched to
a task. Small, self-contained, builds directly on Phase 1.

- [x] 2.1 — Implement a lightweight "does this task look mismatched to the
      current forced mode" check — this can be a simple heuristic for now
      (e.g. task length/keyword-based) since the real complexity judgment
      belongs to Orchestrator in Phase 4, not this mismatch-notice feature
- [x] 2.2 — When running in a forced mode (`general` or `triad`, not
      `orchestrator`) and the check flags a mismatch, append a single
      gentle, non-blocking note to the transcript (e.g. `"[System]: Note —
      you're in Triad mode; this looks trivial, /mode general would skip the
      review overhead."`)
- [x] 2.3 — Confirm this note is purely informational: it never pauses
      execution, never asks for confirmation, and never switches modes on
      its own — the task proceeds in the forced mode regardless
- [x] 2.4 — **Test:** set `/mode triad`, submit several trivial tasks,
      confirm each one still runs full Triad (no silent downgrade) and that
      the passive mismatch note appears without blocking execution
- [x] 2.5 — **Test:** set `/mode general`, submit a task that looks like it
      needs real oversight (e.g. touches multiple files or sensitive logic),
      confirm the reverse-direction note appears appropriately

**Checkpoint:** forced modes now give you a gentle heads-up when they seem
mismatched to the task, without ever taking control away from your explicit
choice.


## Phase 3 — The Clarify Phase

**Goal:** get the batched upfront clarifying-questions behavior working
across your existing modes (General Chat, Triad), before Orchestrator and
twin subagents exist to also need it. This is independent of Phases 1-2 and
can be built in parallel if you prefer, but is listed here since Phase 4
depends on it.

- [x] 3.1 — Implement a shared `clarify` step (e.g. `internal/clarify/
      clarify.go`) that any mode can call before starting real work: given a
      task description, the relevant agent(s) assess it for ambiguity
- [x] 3.2 — If ambiguity exists, **all** questions get batched into a single
      upfront round — not asked one at a time, not scattered mid-task
- [x] 3.3 — Work does not begin until the human answers, or explicitly says
      something equivalent to "proceed, use your best judgment" — at which
      point the agent(s) proceed using the most reasonable interpretation
      and note the assumption made, rather than blocking forever
- [x] 3.4 — Wire this into **General Chat** and **Triad** modes first (the
      two that already exist from Phase 1) — Orchestrator and twin-subagent
      wiring comes later in Phases 4 and 6, reusing this same shared step
      rather than duplicating clarify logic
- [x] 3.5 — **Test:** give General Chat mode a deliberately ambiguous task,
      confirm it produces a single batched clarify round rather than
      guessing or asking piecemeal
- [x] 3.6 — **Test:** repeat for Triad mode — confirm Coder/Reviewer clarify
      before the first proposed action, not mid-task
- [x] 3.7 — **Test:** confirm saying "just proceed" after a clarify round
      correctly unblocks work in both modes, using a stated best-guess
      interpretation rather than stalling indefinitely

**Checkpoint:** both existing modes now ask batched clarifying questions
upfront on ambiguous tasks, using one shared, reusable clarify step.




## Phase 4 — Orchestrator Routing Logic

**Goal:** give Orchestrator mode actual judgment — this is where the
`orchestrator` placeholder from Phase 1.2.5 gets replaced with real routing.
Twin subagents (Phase 6) don't exist yet — for this phase, "route to twin
subagent" can simply route to Triad as a stand-in, with the real twin-pair
behavior arriving in Phase 6.

- [x] 4.1 — Orchestrator receives the task first whenever `current_mode ==
      orchestrator` (the default)
- [x] 4.2 — Orchestrator **always states its routing reasoning out loud** in
      the transcript before acting — e.g. `"[Orchestrator]: This looks like
      a one-line typo fix — routing to General Chat."` — this is not
      optional, it's the traceability mitigation from Phase 0 and must never
      be skipped, even on "obvious" auto-proceed cases
- [x] 4.3 — **Auto-proceed, no human confirmation needed**, only at the two
      extremes:
      - Genuinely trivial (single-file typo/rename/one-liner, no
        architectural or security surface) → routes to **General Chat**
        immediately
      - Genuinely critical (touches auth, payments, deletion, matches any
        existing hook blocklist pattern from Workflow 2 §3.2.3, or anything
        Orchestrator itself flags as high-risk) → routes to **Triad**
        immediately, since more oversight is never the wrong default
- [x] 4.4 — **For everything in the genuine middle** → Orchestrator **must
      ask the human to confirm or override** before proceeding: `"[
      Orchestrator]: I'd route this to a twin-subagent pair — proceed, or
      would you prefer full Triad instead?"` (routes to Triad directly for
      now, per this phase's stand-in note above, until Phase 6 lands)
- [x] 4.5 — Log every routing decision (auto or confirmed) as its own
      transcript entry type, e.g. `Type: "routing_decision"`, containing:
      the task, the complexity judgment, the target mode, and whether it was
      auto-proceeded or human-confirmed
- [x] 4.6 — Wire the clarify step from Phase 3 in *before* Orchestrator's
      routing judgment — clarify ambiguity first, then route, not the other
      way around
- [x] 4.7 — **Test:** give Orchestrator a clearly trivial task, confirm it
      auto-routes to General Chat with a logged, stated reason
- [x] 4.8 — **Test:** give Orchestrator a clearly critical task (e.g.
      something matching the hook blocklist), confirm it auto-routes to
      Triad with a logged, stated reason
- [x] 4.9 — **Test:** give Orchestrator a genuinely ambiguous-complexity
      task, confirm it stops and asks for confirmation rather than silently
      picking
- [x] 4.10 — **Test:** confirm every routing decision (all three cases
      above) produces a `routing_decision` transcript entry with accurate
      contents


**Checkpoint:** Orchestrator mode now makes real, logged, traceable routing
decisions, correctly auto-proceeding at the extremes and confirming with you
in the middle. It still routes "medium" tasks to full Triad as a stand-in —
Phase 6 replaces that stand-in with real twin-subagent behavior.

---

## Phase 5 — Rubric for Orchestrator's Judgment (Refinement Pass)

**Goal:** this phase exists because Phase 4's "genuinely trivial" /
"genuinely critical" / "genuine middle" judgment needs concrete criteria, not
just vibes — this was flagged as an open item and deserves its own focused
pass rather than being bolted onto Phase 4's already-full scope.

- [x] 5.1 — Draft a short, concrete rubric Orchestrator's system prompt can
      apply consistently — e.g. file count touched, whether the task matches
      existing hook blocklist patterns (Workflow 2 §3.2.3), whether it's a
      new feature vs. a one-line fix, whether it touches auth/payment/
      deletion code paths
- [x] 5.2 — Update Orchestrator's system prompt to reference this rubric
      explicitly rather than leaving the judgment fully open-ended
- [x] 5.3 — Re-run Phase 4's tests (4.7-4.9) against a wider set of real or
      realistic tasks spanning the full trivial→critical range, and
      specifically probe the middle ground with tasks intentionally designed
      to be ambiguous under the rubric
- [x] 5.4 — **Test:** confirm the rubric produces consistent routing on
      repeated runs of the same or very similar task — inconsistency here is
      exactly the non-deterministic-routing risk flagged in Phase 0, so
      treat repeat-run drift as a real bug to investigate, not noise

**Checkpoint:** Orchestrator's routing judgment now follows a documented,
testable rubric instead of an unconstrained model guess, and you've
confirmed it's reasonably consistent on repeated similar inputs.





## Phase 6 — Twin Subagent

**Goal:** the most novel piece of this whole document — an isolated mini-
Triad (mini-Coder + mini-Reviewer pair with their own private approval
loop). Deliberately saved for its own phase, after Orchestrator's routing
(Phase 4-5) is stable, since it's the highest-risk piece and benefits from
everything around it already working.

- [x] 6.1 — Define a new `TwinSubagent` construct, distinct from the
      existing single `Subagent` (Workflow 2 §4): it spawns **two** agents
      together — a mini-Coder and a mini-Reviewer — not one
- [x] 6.2 — The twin pair gets its **own isolated transcript**
      (`sessions/twins/<id>.jsonl`), separate from both the main session
      transcript and from regular single-subagent transcripts
      (`sessions/subagents/`)
- [x] 6.3 — Orchestrator's handoff to the twin pair is **exactly one
      message** — the task description, optionally with the same bounded
      `context` string pattern used by `spawn_subagent` (Workflow 2 §4.2.2).
      Do not hand over the full main-session transcript
- [x] 6.4 — Once spawned, the mini-Coder and mini-Reviewer run **their own
      private propose→review→execute loop**, reusing the existing
      `internal/loop` approval-cycle logic (Workflow 1 §6.3) against their
      own isolated transcript — this is not new loop logic, it's the same
      loop pointed at a different transcript file
- [x] 6.5 — **The mini-Reviewer preserves the core invariant**: no tool
      access, ever, same as main-session Reviewer (Workflow 1 §6.2). Mini-
      Coder gets the same tool set as main Coder (file, shell, browser,
      spawn_subagent — but see 6.8 on nesting limits)
- [x] 6.6 — Wire the Phase 3 clarify step in immediately after the twin
      pair receives Orchestrator's one-message handoff, before their own
      loop starts
- [x] 6.7 — The twin pair's own auto-commit behavior (Workflow 2 §2) should
      still fire on their executed actions. Commit messages should be
      distinguishable from main-session commits (e.g. `[triad:twin #<id>]`
      prefix) so the commit journey (Phase 10) can visually distinguish
      twin-subagent work from main-loop work
- [x] 6.8 — **Recursion/nesting guard**: a twin subagent's mini-Coder must
      NOT be allowed to spawn another twin subagent or another single
      subagent. Depth stops at one level, same reasoning as the existing
      single-subagent depth guard (Workflow 2 §4.2.6)
- [x] 6.9 — When the twin pair's Reviewer and Coder agree the task is
      complete, produce **one summary** and append it to the **main**
      session transcript as a single `action_result`-style entry attributed
      to the twin pair (e.g. `Speaker: "Twin:add-rate-limiting"`) — same
      summary-only-return principle as the existing single-subagent pattern
- [x] 6.10 — Replace Phase 4's "route medium tasks to Triad as a stand-in"
      with real twin-subagent routing now that this construct exists
- [x] 6.11 — **Test:** give Orchestrator a genuinely medium-complexity task,
      confirm it proposes the twin-subagent route, confirm on approval the
      twin pair runs its own full propose→review→execute cycle privately,
      and confirm only a clean summary lands in the main transcript
- [x] 6.12 — **Test:** confirm a twin subagent's mini-Reviewer genuinely has
      no tool access (attempt to provoke a tool call from it and confirm
      it's rejected/impossible at the config level, not just by prompt)
- [x] 6.13 — **Test:** confirm the depth guard — attempt to have a twin
      pair's mini-Coder call `spawn_subagent` or spawn a nested twin, confirm
      it's blocked
- [x] 6.14 — **Hard cap on twin-subagent turns.** Add a turn/time cap on the
      twin pair's private propose→review→execute loop, similar in spirit to
      the existing loop-guard cap from Workflow 1 §4.3 — added here
      specifically because of the confirmed 15x token-overhead finding from
      Phase 0: an uncapped twin subagent on a free-tier, rate-limited model
      is the single highest-risk failure mode in this entire document, and
      it's cheaper to cap it now than to discover the problem after a stuck
      twin pair silently exhausts your daily rate limit
- [x] 6.15 — **Log twin-subagent start, not just completion.** Append a
      transcript entry to the **main** session the moment a twin pair is
      spawned (not only when its summary returns) — e.g. `"[System]: Twin
      subagent started for task: <description>"`. Right now the main
      transcript only sees a twin pair's *result*; if a twin pair hangs,
      loops, or burns rate limit silently, there's currently no visibility
      from the main session until it eventually returns or hits the cap from
      6.14. This is the minimum viable fix; full cross-agent observability
      is Phase 7
- [x] 6.16 — **Test:** confirm the turn cap from 6.14 actually triggers on a
      deliberately unresolvable twin-pair disagreement, and that hitting it
      surfaces cleanly to the main session rather than hanging silently
- [x] 6.17 — **Test:** confirm the start-of-twin log entry from 6.15
      appears in the main transcript immediately on spawn, well before the
      twin pair's eventual summary arrives

### Suggested package structure for Phases 1-6

```
internal/
├── orchestrator/
│   ├── orchestrator.go     # routing judgment, complexity/severity logic
│   ├── routing_log.go      # routing_decision transcript entry handling
│   └── mode.go             # current_mode state, /mode command logic
├── twinsubagent/
│   └── twinsubagent.go     # isolated mini-Triad spawn, transcript, summary
└── clarify/
    └── clarify.go          # shared clarify-phase logic, reused by all modes
```

**Checkpoint:** the full Orchestrator layer is complete — modes, mismatch
notices, clarify phase, routing judgment with a real rubric, and twin
subagents all working together, with a hard turn cap and start-of-spawn
logging in place as the minimum safety net. This is the single largest
chunk of Workflow 3; take a real break before Phase 7.



## Phase 7 — Observability

**Goal:** give yourself one place to see what actually happened across the
main session, Orchestrator's routing decisions, and any twin subagents —
before this becomes a real problem rather than a theoretical one. This phase
exists specifically because Phase 6 introduces a second, nested layer of
agent activity that the main transcript alone doesn't fully surface (beyond
the minimum start/completion logging added in 6.14-6.15).

### Why this phase exists (not folded into Phase 6)

Multi-agent research is blunt that debugging nested/parallel agent activity
without dedicated tracing is close to impossible after the fact — you can't
reconstruct "which agent did what, in what order, across which isolated
transcripts" from scattered logs alone. Rather than bolt a partial version of
this onto Phase 6's already-full scope, it gets its own phase, built right
after Twin Subagent exists (so there's real nested activity to observe) and
before Commit Journey (Phase 10), since `/journey` is a git-focused view, not
a cross-agent-activity view, and shouldn't be your only debugging tool.

### 7.1 Design

- [x] 7.1.1 — Define a single, unified **trace log** distinct from both the
      main JSONL transcript and any per-twin transcript — e.g.
      `sessions/traces/<session-id>.jsonl` — that records one entry per
      significant cross-agent event: routing decisions (Phase 4.5), twin
      subagent spawn/complete (Phase 6.15 plus a matching completion entry),
      clarify-phase triggers (Phase 3), and any hook/blocklist interventions
      (Workflow 2 §3.2.3). This is intentionally a thinner, flatter log than
      the full transcripts — built for scanning across agents, not reading
      one agent's full reasoning
- [x] 7.1.2 — Each trace entry should include: timestamp, which
      agent/mode/twin-id it concerns, event type, and a short one-line
      description — enough to reconstruct the sequence of what happened
      without needing to open every individual transcript file
- [x] 7.1.3 — Implement a `/trace` slash command (same Go-backed pattern as
      `/summary`/`/journey`) that renders the current session's trace log in
      the TUI — a flat, chronological list across all agents/modes/twins,
      answering "what actually happened, in order, across everything" in one
      view
- [x] 7.1.4 — Wire trace-log writes into the existing emission points rather
      than duplicating logic: Phase 4.5's routing-decision logging, Phase
      6.15's twin-spawn logging (plus its natural completion counterpart),
      Phase 3's clarify triggers, and Workflow 2's hook interventions should
      all also write one line to the trace log, not just their own existing
      transcript/log
- [x] 7.1.5 — Keep this deliberately lightweight — this is not a new
      database, not OpenTelemetry, not a metrics/dashboard system. It's a
      flat, append-only, human-readable file-based trace, consistent with
      every other part of Triad's design philosophy

### 7.2 Tests

- [x] 7.2.1 — **Test:** run a task that goes through Orchestrator routing,
      triggers a clarify round, and spawns a twin subagent; confirm `/trace`
      shows all three events in correct chronological order in one place
- [x] 7.2.2 — **Test:** confirm a twin subagent's start (6.15) and completion
      both appear as distinct, matched entries in the trace log, so a
      stuck/slow twin is visibly identifiable (started, never completed) just
      from `/trace` output
- [x] 7.2.3 — **Test:** confirm the trace log stays flat and scannable even
      after several sessions — this is meant to be the fast, first place you
      look when something seems off, not something you need to grep through
      like the full transcripts

**Checkpoint:** you have one command (`/trace`) that shows a chronological,
cross-agent view of what happened in a session — routing decisions, clarify
triggers, and twin-subagent lifecycles — without needing to manually piece
it together from separate transcript files.


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

- [x] 8.2.1 — Implement `memory/INDEX.md` as the **only** file read
      automatically at the start of every session, regardless of mode. Keep
      it to short pointers + a handful of "quick facts," not full content
- [x] 8.2.2 — Implement `memory/daily/<date>.md` as an **append-only**
      per-session log, written to at session end (or continuously, matching
      your existing "write immediately, don't batch" transcript philosophy)
- [x] 8.2.3 — Implement `memory/topics/*.md` as **curated** files, updated
      deliberately (not automatically dumped) when a genuinely recurring
      pattern or important decision emerges
- [x] 8.2.4 — Implement `memory/preferences.md` as a single small file for
      your personal preferences, separate from project facts
- [x] 8.2.5 — Add a memory-read step at session start: load `INDEX.md` (and
      only `INDEX.md`) into context for whichever mode is active. Topic files
      are fetched on demand only if the index points to something relevant
      to the current task
- [x] 8.2.6 — Add a manual memory-write path: a way for the human (or
      Coder/Reviewer, with human confirmation) to explicitly add an entry to
      a topic file — this is the manual path that Phase 9's `/learn` command
      will later build on top of, but it should work standalone first

### 8.3 Tests

- [x] 8.3.1 — **Test:** confirm `INDEX.md` alone is what gets loaded at
      session start, and that topic files are only pulled in when the index
      or task explicitly points to them
- [x] 8.3.2 — **Test:** run several sessions, confirm daily logs accumulate
      correctly without ever being edited/overwritten (append-only, same
      discipline as your JSONL transcript)
- [x] 8.3.3 — **Test:** confirm the manual topic-file write path works
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

- [x] 9.1.1 — At the end of every session (or every completed task, if
      finer granularity proves useful), run a lightweight extraction pass
      over that session's transcript: look specifically for **Reviewer
      objections that were later resolved** (Workflow 1 §6.3 — the
      propose→object→revise cycle) and for any explicit correction the human
      gave (a human message that changed Coder's direction mid-task)
- [x] 9.1.2 — Write each extracted item as a **raw entry in the daily log**
      (`memory/daily/<date>.md`, Phase 8.2.2) automatically — this part is
      safe to fully automate, since the daily log is explicitly the raw,
      unfiltered, append-only layer
- [x] 9.1.3 — **Do NOT automatically promote extracted items into
      `topics/*.md` or `INDEX.md`.** This is the deliberate line that
      preserves the "small and curated beats comprehensive" principle from
      Phase 0/8.1
- [x] 9.1.4 — Implement a **`/learn` review command**: at a natural
      checkpoint (session end, or on demand), Triad surfaces a short,
      human-reviewable digest of newly extracted daily-log items — e.g.
      *"3 corrections logged this session. Promote any to a topic file?"* —
      and the human decides what (if anything) gets promoted, building
      directly on the manual write path from Phase 8.2.6
- [x] 9.1.5 — When something is promoted via `/learn`, write it to the
      relevant `topics/*.md` file in the same curated style as existing
      manual entries — dated, concise, one clear statement of the lesson

### 9.2 Tests

- [x] 9.2.1 — **Test:** run a session with at least one Reviewer objection
      that gets resolved and one human mid-task correction, confirm both are
      correctly auto-extracted into that day's daily log with accurate
      before/after context
- [x] 9.2.2 — **Test:** confirm `/learn` correctly surfaces only new/
      unreviewed extracted items (not ones already promoted or already
      dismissed in a prior `/learn` pass), and that declining to promote an
      item doesn't delete it from the daily log
- [x] 9.2.3 — **Test:** confirm no code path exists that writes to
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

- [x] 10.2.1 — Add a `/journey` slash command (same pattern as your existing
      `/summary` — check `internal/commands` for how built-in Go-backed
      commands are distinguished from user `.md` template commands)
- [x] 10.2.2 — Data source: `git log` filtered to Triad-tagged commit
      messages (reuse the exact parsing/filtering logic already built for
      `/summary` — do not re-implement commit-message parsing a second time)
- [x] 10.2.3 — **TUI rendering**: a simple ASCII node-and-line view, one
      commit per row, chronological, showing: short hash, timestamp,
      one-line description, and a marker distinguishing main-loop commits
      from twin-subagent commits (using your existing `lipgloss` styling
      conventions)
- [x] 10.2.4 — **HTML export**: implement as a new command or flag (e.g.
      `/journey --export`) that writes a standalone HTML file with a nicer
      visual timeline of the same data — no new backend/server needed
- [x] 10.2.5 — Decide and implement a simple, tasteful visual style for the
      HTML export — a good candidate to consult the `frontend-design`
      conventions for if built as an artifact-style page

### 10.3 Tests

- [x] 10.3.1 — **Test:** run `/journey` on a session with a realistic mix of
      main-loop and twin-subagent commits, confirm both TUI and HTML views
      correctly distinguish the two and reflect accurate chronological order
- [x] 10.3.2 — **Test:** run on a session with zero commits yet, confirm a
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

---
