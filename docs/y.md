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