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

- [ ] 5.1 — Draft a short, concrete rubric Orchestrator's system prompt can
      apply consistently — e.g. file count touched, whether the task matches
      existing hook blocklist patterns (Workflow 2 §3.2.3), whether it's a
      new feature vs. a one-line fix, whether it touches auth/payment/
      deletion code paths
- [ ] 5.2 — Update Orchestrator's system prompt to reference this rubric
      explicitly rather than leaving the judgment fully open-ended
- [ ] 5.3 — Re-run Phase 4's tests (4.7-4.9) against a wider set of real or
      realistic tasks spanning the full trivial→critical range, and
      specifically probe the middle ground with tasks intentionally designed
      to be ambiguous under the rubric
- [ ] 5.4 — **Test:** confirm the rubric produces consistent routing on
      repeated runs of the same or very similar task — inconsistency here is
      exactly the non-deterministic-routing risk flagged in Phase 0, so
      treat repeat-run drift as a real bug to investigate, not noise

**Checkpoint:** Orchestrator's routing judgment now follows a documented,
testable rubric instead of an unconstrained model guess, and you've
confirmed it's reasonably consistent on repeated similar inputs.


---
