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

- [ ] 7.1.1 — Define a single, unified **trace log** distinct from both the
      main JSONL transcript and any per-twin transcript — e.g.
      `sessions/traces/<session-id>.jsonl` — that records one entry per
      significant cross-agent event: routing decisions (Phase 4.5), twin
      subagent spawn/complete (Phase 6.15 plus a matching completion entry),
      clarify-phase triggers (Phase 3), and any hook/blocklist interventions
      (Workflow 2 §3.2.3). This is intentionally a thinner, flatter log than
      the full transcripts — built for scanning across agents, not reading
      one agent's full reasoning
- [ ] 7.1.2 — Each trace entry should include: timestamp, which
      agent/mode/twin-id it concerns, event type, and a short one-line
      description — enough to reconstruct the sequence of what happened
      without needing to open every individual transcript file
- [ ] 7.1.3 — Implement a `/trace` slash command (same Go-backed pattern as
      `/summary`/`/journey`) that renders the current session's trace log in
      the TUI — a flat, chronological list across all agents/modes/twins,
      answering "what actually happened, in order, across everything" in one
      view
- [ ] 7.1.4 — Wire trace-log writes into the existing emission points rather
      than duplicating logic: Phase 4.5's routing-decision logging, Phase
      6.15's twin-spawn logging (plus its natural completion counterpart),
      Phase 3's clarify triggers, and Workflow 2's hook interventions should
      all also write one line to the trace log, not just their own existing
      transcript/log
- [ ] 7.1.5 — Keep this deliberately lightweight — this is not a new
      database, not OpenTelemetry, not a metrics/dashboard system. It's a
      flat, append-only, human-readable file-based trace, consistent with
      every other part of Triad's design philosophy

### 7.2 Tests

- [ ] 7.2.1 — **Test:** run a task that goes through Orchestrator routing,
      triggers a clarify round, and spawns a twin subagent; confirm `/trace`
      shows all three events in correct chronological order in one place
- [ ] 7.2.2 — **Test:** confirm a twin subagent's start (6.15) and completion
      both appear as distinct, matched entries in the trace log, so a
      stuck/slow twin is visibly identifiable (started, never completed) just
      from `/trace` output
- [ ] 7.2.3 — **Test:** confirm the trace log stays flat and scannable even
      after several sessions — this is meant to be the fast, first place you
      look when something seems off, not something you need to grep through
      like the full transcripts

**Checkpoint:** you have one command (`/trace`) that shows a chronological,
cross-agent view of what happened in a session — routing decisions, clarify
triggers, and twin-subagent lifecycles — without needing to manually piece
it together from separate transcript files.

---
