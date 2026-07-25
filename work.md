# Triad — Workflow 4: Browser Tool Hardening (Full Chrome Robustness)

**Current state of project (July 2026):** v1 core, Workflow 2 (commands,
auto-commit, hooks, browser tools, subagents, web search), and Workflow 3
(Orchestrator, three modes, twin subagents, clarify phase, commit journey,
markdown memory + self-learning, observability/trace log) are all complete —
all 10 phases of Workflow 3 shipped. **This document replaces the earlier
sandboxed computer-use plan.** Scope decision: since ~90% of real use is
browser work and the rest is deliberately out of scope, there's no need for
a VM/screenshot/pixel-grounding subsystem — Workflow 2's existing
`browser_*` tools (Playwright, DOM-level) already do the actual job better
than raw computer use would. This document is a **hardening pass** on those
5 existing tools, not a new capability.

---

## 0. Research Grounding — Why This Approach Is Right, and What "Robust" Means

### 0.1 DOM-level control was already the correct call

Current research and production browser-agent frameworks (Reader, Browser
Harness, the "Agentic Compilation" line of academic work, and mainstream
Playwright practice) converge on the same architecture: use deterministic
DOM-level automation for known, stable interactions, and reserve
LLM/vision-based reasoning only for exception handling — recovering from a
broken selector, disambiguating a novel element, or exploring an unfamiliar
page. Pure vision-based agents (Claude Computer Use style) are explicitly
described as slower, more expensive, and less deterministic than DOM-first
approaches for anything repeatable. This confirms your original Workflow 2
decision was right, and it reframes what this hardening pass should
actually add: not smarter guessing, but better fallback and recovery when a
selector doesn't work the first time.

### 0.2 Flakiness has well-documented, specific root causes

A 2026 diagnostic playbook on Playwright flakiness identifies five concrete
root causes: async/timing races, locator drift (selectors that stop
matching after a UI change), session/state pollution between actions,
environment variance, and — named explicitly — AI-agent non-determinism.
The fixes the field converged on are specific, not general: replace manual
sleeps with condition-based waits, prefer role-based locators over CSS/XPath
tied to styling classes, isolate browser context/session state cleanly, and
never use absolute positional selectors. This document's hardening phases
map directly onto these five causes rather than inventing generic "make it
more robust" language.

### 0.3 The architecture that wins: DOM-first, LLM-as-exception-handler

The most directly applicable finding, from recent work on minimizing
LLM-rerun cost in web automation: the LLM should be invoked exclusively as
an exception handler for a broken step (an "invalidated selector"), not as
the thing re-deciding the entire plan on every single action. Control flow
stays deterministic in the runtime; the LLM gets called in only when
something concrete has actually failed. This maps closely onto Triad's
existing architecture — Coder proposes, Reviewer checks, execution happens
— and this document's fallback/recovery logic (Phase 3) should follow the
same shape: try the deterministic path first, invoke the model only to
recover from a specific, detected failure, not to reconsider every step
from scratch.

**Checkpoint:** you understand that this hardening pass targets five
specific, named failure causes (timing, locator drift, session pollution,
environment variance, agent non-determinism), and that recovery logic
should call the LLM only when a concrete failure is detected, not on every
action. Move to Phase 1 when ready.

---

## Phase 1 — Selector Strategy Audit & Upgrade

**Goal:** move your existing `browser_click`/`browser_type`/`browser_get_text`
tools toward the selector strategy the field has converged on, since this is
the single biggest lever for reliability (§0.2).

- [x] 1.1 — Audit current selector handling in `internal/browser`: confirm
      whether Coder is currently free to pass any CSS selector string
      (including fragile, layout-tied ones) with no guidance, or whether
      there's already a preference toward stable selectors
- [x] 1.2 — Update Coder's system prompt (the one used when browser tools
      are available) to follow the confirmed 2026 fallback chain, in order:
      **role+accessible-name first → visible text/label second → CSS
      class/attribute only as a last resort, never positional/nth-child**.
      This is Playwright's own documented recommendation ("prioritize
      user-visible attributes and explicit contracts such as role-based
      locators"), not just a general best practice — instruct Coder to
      follow this exact order rather than picking whichever selector type
      comes to mind first
- [x] 1.3 — If Playwright-go's locator API supports role-based queries
      (verify current API surface — don't assume feature parity with the
      JS/Python Playwright libraries, since Go bindings sometimes lag),
      wire `browser_click`/`browser_type` to accept a `strategy` hint
      (`role`, `text`, `css`) alongside the selector, so Coder's intent is
      explicit rather than everything being parsed as a raw CSS string
- [x] 1.4 — Never accept absolute positional selectors (e.g. "third button
      on the page," nth-child chains with no semantic anchor) — if Coder's
      proposed selector looks purely positional, this is a signal worth
      surfacing to Reviewer as a quality concern, not just executing it
      silently
- [x] 1.5 — **Test:** run a task against a page with realistic messy markup
      (reused classes, no IDs on some elements), confirm role/text-based
      targeting succeeds where a naive CSS-class selector would have been
      fragile
- [x] 1.6 — **Test:** confirm a genuinely positional-only selector proposal
      gets flagged rather than silently executed

**Checkpoint:** Coder defaults to stable, semantic selectors instead of
fragile CSS/positional ones, addressing the "locator drift" root cause
directly.

---

## Phase 2 — Waiting & Timing

**Goal:** address the "async/timing races" root cause — the single most
commonly cited flakiness cause across every source researched for this
document.

- [x] 2.1 — Audit current wait behavior: confirm whether `browser_click`/
      `browser_type`/`browser_get_text` currently rely on Playwright's
      built-in auto-waiting (actionability checks before interacting with an
      element) or whether any manual/fixed-delay waiting exists anywhere in
      the current implementation
- [x] 2.2 — Where manual delays exist, replace them with condition-based
      waits tied to an actual signal — element visible/attached, network
      idle, a specific text appearing — never a fixed `sleep(N)`, per the
      universally-converged finding that hard waits are both slower and
      still don't guarantee correctness
- [x] 2.3 — Add a new tool, `browser_wait_for(condition)`, giving Coder an
      explicit way to wait for a specific signal (e.g. "wait until text
      'Success' appears," "wait until element X is visible") rather than
      guessing that a fixed pause after `browser_click` is enough — this
      also makes waiting a visible, reviewable action in the transcript
      instead of invisible internal timing
- [x] 2.4 — Set an explicit, configurable default timeout for all
      wait/actionability checks (not unlimited) — a hung wait should
      surface as a clear failure back to Coder/Reviewer, not freeze the
      loop, mirroring the existing `run_command` timeout pattern from
      Workflow 2 §8.3
- [x] 2.5 — **Test:** a page with a deliberately delayed element (e.g.
      content that appears 2 seconds after page load) — confirm the tool
      correctly waits for it rather than failing immediately or guessing a
      fixed delay that happens to work today but might not tomorrow
- [x] 2.6 — **Test:** confirm the timeout from 2.4 actually triggers on a
      genuinely-never-appearing condition, surfacing a clear failure rather
      than hanging

**Checkpoint:** all waiting is condition-based and bounded by an explicit
timeout — no fixed sleeps anywhere in the browser tool implementation.

---

## Phase 3 — Selector Failure Recovery (The Exception-Handler Pattern)

**Goal:** implement the specific architecture validated in §0.3 — the model
gets invoked to recover from a detected, concrete failure, not to re-plan
continuously. This is the core "robustness" feature of this whole document.

- [ ] 3.1 — Detect selector failures precisely, as **two distinct failure
      types**, not one generic bucket: (a) zero matches — the selector found
      nothing, and (b) ambiguous match — the selector matched more than one
      element (Playwright's own locators are strict by default and throw a
      "strict mode violation" on this exact case, so this is a real,
      commonly-hit failure mode worth handling separately, not folding into
      the zero-match case)
- [ ] 3.2 — On a detected selector failure, before simply surfacing an error
      back to Coder to start over, attempt one cheap, deterministic
      recovery pass first — the recovery differs by failure type from 3.1:
      for **zero matches**, re-query the page for elements with similar
      text/role/aria-label to what was originally requested (a lightweight
      version of the "selector fallback" pattern used in production
      self-healing browser frameworks); for **ambiguous matches**, attempt
      to narrow automatically using available context (e.g. chain/filter by
      visible text or nearby container, mirroring Playwright's own
      `.filter()`/`.and()` disambiguation pattern) before giving up and
      asking the model. Both recovery paths here are plain DOM inspection
      code, not a model call
- [ ] 3.3 — Only if the deterministic recovery pass in 3.2 also fails,
      invoke Coder specifically to reconsider *this one failed step* —
      giving it the current page's relevant text/structure and the original
      failed selector, asking for a corrected target. This is the
      "LLM-as-exception-handler" pattern from §0.3: the model is solving one
      concrete, bounded problem, not replanning the whole task
- [ ] 3.4 — This recovery flow still goes through the normal Reviewer
      approval gate — a corrected selector proposal is itself a new
      proposed action, reviewed like any other, not auto-executed just
      because it's a "recovery"
- [ ] 3.5 — Cap recovery attempts per action (e.g. 2 attempts: one
      deterministic, one model-assisted) before surfacing a clean failure to
      the human — mirroring the existing loop-guard cap philosophy from
      Workflow 1 §4.3, so a genuinely broken page doesn't spin indefinitely
- [ ] 3.6 — **Test:** deliberately break a selector Coder would reasonably
      propose (e.g. rename a button's visible text slightly), confirm the
      deterministic recovery pass (3.2) catches simple zero-match cases
      without ever calling the model
- [ ] 3.7 — **Test:** deliberately create an ambiguous-match case (e.g. two
      buttons with the same accessible name in different sections), confirm
      the strict-mode-violation failure is detected as its own type and the
      filter/narrow-based recovery from 3.2 resolves it without a model call
      where the page structure makes disambiguation possible
- [ ] 3.8 — **Test:** break a selector badly enough that deterministic
      recovery fails, confirm the model-assisted recovery (3.3) is invoked
      exactly once, produces a corrected proposal, and that proposal still
      goes through Reviewer
- [ ] 3.9 — **Test:** confirm the cap from 3.5 triggers cleanly on a
      genuinely unrecoverable case (element truly doesn't exist on the
      page) rather than looping

**Checkpoint:** selector failures trigger a cheap deterministic recovery
attempt first, fall back to a single bounded model-assisted correction only
if needed, and never bypass Reviewer.

---

## Phase 4 — Session & State Isolation

**Goal:** address the "session/state pollution" root cause — since Triad's
browser tools currently share one long-lived page (locked in Workflow 2
§6.1's design notes), confirm this doesn't create cross-task contamination
as usage grows.

- [ ] 4.1 — Audit what state persists across tasks in the current shared-page
      design: cookies, localStorage, open tabs, login sessions, previous
      navigation history
- [ ] 4.2 — Decide, explicitly, whether this persistence is desired
      (e.g. staying logged into a test site across multiple tasks in one
      session is convenient) or should be reset at clear boundaries (e.g. a
      fresh browser context per top-level task, not per session) — this is
      a real design choice, not an oversight, and should be made
      deliberately rather than left as "whatever Playwright happens to do
      by default"
- [ ] 4.3 — If session boundaries should reset between tasks, implement this
      via Playwright's context/storageState mechanism (creating a fresh
      browser context, optionally seeded with a saved storageState for
      login persistence where that's genuinely wanted) rather than closing
      and relaunching the whole browser process each time, which is far more
      expensive
- [ ] 4.4 — **Test:** confirm state behaves as decided in 4.2 — either
      correctly persists across tasks in the same session, or correctly
      resets at task boundaries, whichever was chosen
- [ ] 4.5 — **Test:** run two sequential unrelated tasks in the same
      session, confirm no unexpected leftover state (a stale form fill, a
      leftover navigation) causes the second task to behave unexpectedly
      because of the first

**Checkpoint:** browser state behavior across tasks is an explicit, tested
decision, not an accidental side effect of the shared-page design.

---

## Phase 5 — Multi-Tab / Multi-Page Support 

**Goal:** revisit the "one shared page, not a new page per tool call"
decision from Workflow 2 §4.3, now that real usage may call for juggling
multiple tabs — but only build this if you've actually hit the need.

- [ ] 5.1 — Before building anything here, confirm with real usage whether
      you've actually needed multiple tabs/pages simultaneously. If you
      haven't hit this yet, skip this phase entirely rather than building
      speculative capability
- [ ] 5.2 — If needed: add `browser_new_tab()` and `browser_switch_tab(id)`
      tools, keeping the existing single-page tools' default behavior
      (operate on "the current tab") unchanged for backward compatibility
      with tasks that don't need multi-tab awareness
- [ ] 5.3 — Ensure Reviewer sees tab-switching as a visible, reviewable
      action like any other — not an invisible side effect
- [ ] 5.4 — **Test:** a task genuinely requiring two open tabs, confirm it
      works correctly through the full approval loop across both tabs

**Checkpoint:** multi-tab support exists only if real usage demonstrated the
need, and even then, single-tab tasks are unaffected.

---

## Suggested Overall Order

Phases 1-3 form the core reliability chain and should be done in order —
each builds on groundwork from the last. Phase 4 (session isolation) is
independent and can be done anytime. Phase 5 (multi-tab) is conditional —
skip it unless real usage has already demonstrated a need.

```
Phase 1 (Selector Strategy)      ← foundation
Phase 2 (Waiting & Timing)       ← foundation
Phase 3 (Failure Recovery)       ← depends on 1 and 2 being solid
Phase 4 (Session Isolation)      ← independent, do anytime
Phase 5 (Multi-Tab Support)      ← only if actually needed, skip otherwise
```

---

## Open Items

- Verify Playwright-go's current locator API surface for role-based
  queries (Phase 1.3) before committing to that exact implementation shape
  — Go bindings for Playwright have historically lagged the JS/Python
  libraries in feature parity
- Decide the exact recovery-attempt cap number in Phase 3.5 based on real
  usage rather than the illustrative "2" used in this document
- Revisit whether Phase 5 is ever actually needed — treated as optional/
  conditional rather than assumed required
- Watch for Coder incorrectly "fixing" a role-based locator failure by
  suggesting the target page's markup should have an ARIA role added to it
  — this masks a real markup/accessibility issue rather than fixing the
  actual problem, and only makes sense when Coder is editing the page's own
  source (not simply automating an existing, unrelated page it doesn't
  control)

## Explicit Non-Goals for This Phase

- No sandboxed VM, no raw screenshot-based computer use, no pixel-coordinate
  grounding — this entire capability class was deliberately dropped from
  scope in favor of hardening the existing DOM-level tools
- No general-purpose "self-healing" framework adoption (e.g. wrapping a
  third-party library like Browser Harness) — Phase 3's recovery logic is
  purpose-built and small, matching Triad's existing "plain structs, no
  heavy frameworks" philosophy
- No attempt to make browser tools reach non-Chrome, non-browser targets —
  that remains explicitly out of scope per your own stated 90%-browser
  framing