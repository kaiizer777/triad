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