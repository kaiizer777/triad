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

## Phase 3 — The Clarify Phase

**Goal:** get the batched upfront clarifying-questions behavior working
across your existing modes (General Chat, Triad), before Orchestrator and
twin subagents exist to also need it. This is independent of Phases 1-2 and
can be built in parallel if you prefer, but is listed here since Phase 4
depends on it.

- [ ] 3.1 — Implement a shared `clarify` step (e.g. `internal/clarify/
      clarify.go`) that any mode can call before starting real work: given a
      task description, the relevant agent(s) assess it for ambiguity
- [ ] 3.2 — If ambiguity exists, **all** questions get batched into a single
      upfront round — not asked one at a time, not scattered mid-task
- [ ] 3.3 — Work does not begin until the human answers, or explicitly says
      something equivalent to "proceed, use your best judgment" — at which
      point the agent(s) proceed using the most reasonable interpretation
      and note the assumption made, rather than blocking forever
- [ ] 3.4 — Wire this into **General Chat** and **Triad** modes first (the
      two that already exist from Phase 1) — Orchestrator and twin-subagent
      wiring comes later in Phases 4 and 6, reusing this same shared step
      rather than duplicating clarify logic
- [ ] 3.5 — **Test:** give General Chat mode a deliberately ambiguous task,
      confirm it produces a single batched clarify round rather than
      guessing or asking piecemeal
- [ ] 3.6 — **Test:** repeat for Triad mode — confirm Coder/Reviewer clarify
      before the first proposed action, not mid-task
- [ ] 3.7 — **Test:** confirm saying "just proceed" after a clarify round
      correctly unblocks work in both modes, using a stated best-guess
      interpretation rather than stalling indefinitely

**Checkpoint:** both existing modes now ask batched clarifying questions
upfront on ambiguous tasks, using one shared, reusable clarify step.

---
