# Triad — Issues.md

Reported bugs, phased for one-session-per-phase execution.

---

## Coding Agent Prompt — Focus & Discipline

You are working on one phase at a time from a checklist. Before writing any code: read the full phase, restate the root cause in your own words, and confirm your fix actually targets that root cause — not a symptom next to it. Do not touch code outside the phase's stated scope, even if you notice unrelated issues; note them separately instead of fixing them mid-phase. Work checkbox by checkbox, in order — do not jump ahead or batch multiple checkboxes into one untested change. After each checkbox, verify it actually works (run the test, reproduce the fix) before marking it done and moving on; a checked box you haven't verified is a lie. If a task is ambiguous, ask one sharp clarifying question instead of guessing and building on top of a wrong assumption. Never declare a phase complete until every checkbox is checked AND the Checkpoint at the bottom is true — the checkpoint is the actual definition of done, not the last checkbox.

---

## Phase 1: No retry on tool/command failure — needs a general retry mechanism

**Problem:** Any tool execution that errors (shell commands, `cd`, file ops, subagent calls, etc.) just fails once and stops — there's no retry anywhere in the execution path. `cd` failing is just one example of a systemic gap: any transient failure in any tool kills the action outright instead of attempting recovery.

**Root cause (likely):** Tool execution (`internal/agent` tool dispatch → `run_command`/`write_file`/`read_file`/`browser_*`/`spawn_subagent`/etc.) has no shared retry wrapper. Each call is fire-once; a failure just propagates straight to an `action_result` with an error, with no attempt loop around it.

- [x] Reproduce across tool types: confirm shell command failure, file op failure, and at least one other tool type (browser/subagent) all fail with zero retry attempts
- [x] Design a single shared retry wrapper in `internal/agent` (or a new `internal/retry`) that wraps ALL tool execution, not just `run_command` — one mechanism, applied uniformly across `write_file`, `read_file`, `run_command`, `browser_*`, `spawn_subagent`, `spawn_twin_subagent`
- [x] Fixed retry count: 5 attempts before giving up, with short backoff between attempts (e.g. exponential or fixed short delay — keep it simple, this isn't the focus)
- [x] Distinguish retryable vs non-retryable failures: transient/environment errors (timeouts, temporary I/O, flaky network for browser tools) should retry; clear logic/user errors (bad path that will never resolve, syntax errors, permission denied) should NOT burn all 5 retries pointlessly — fail fast on those instead
- [x] Surface retry attempts in the transcript/trace as they happen (e.g. "attempt 2/5 failed, retrying") so the human isn't staring at a frozen UI wondering what's happening
- [x] On exhausting all 5 retries, surface a clear final error to the transcript — never silent failure
- [x] Unit test: transient failure retries up to 5 times then succeeds on a later attempt
- [x] Unit test: transient failure exhausts all 5 retries then surfaces a clear error
- [x] Unit test: non-retryable failure (e.g. bad path) fails fast without burning retries
- [x] Verify this wrapper applies to `cd`-in-`run_command` case as one instance of the fix, not a special case
- [x] Check interaction with `internal/gitcommit`: a `write_file` that fails then succeeds on retry must still result in exactly one commit, not zero (if commit is skipped on the failed attempt) or duplicate/broken commits (if partial state got committed before the retry)

**Checkpoint:** Any tool call in Triad — not just shell commands — retries up to 5 times on transient failure before giving up, distinguishes that from non-retryable errors, and always surfaces a clear final result to the transcript instead of silently dying on the first error.

---

## Phase 2: Skill selection is gated but Coder ends turn without selecting anything

**Problem:** The mandatory gate already blocks coding actions pre-selection, but the Coder agent's response simply ends its turn without calling Stage 1 selection at all — no section chosen, no action attempted, session stalls.

**Root cause (likely):** The gate is reactive — it only rejects when Coder *tries* a coding action pre-selection. It has no fallback for when Coder's turn ends with no tool call and no selection made at all. Likely also a prompt/tool-exposure issue: Coder is told selection is required but still has `write_file`/`run_command`/etc. available, so it can choose to end its turn doing neither.

- [ ] Reproduce: start a coding task in Triad/Orchestrator mode, confirm Coder's turn ends with no Stage 1 selection and no re-prompt — session just idles
- [ ] Add turn-end check in `internal/loop`: if Coder's turn completes with selection still pending and zero tool calls made, do not idle — force a re-prompt in the same cycle instructing Coder to select a section first
- [ ] Cap forced re-prompts (e.g. max 2) to avoid infinite stalls; on cap exhaustion, surface a clear error to the human instead of silently idling
- [ ] Narrow the tool list exposed to Coder pre-selection: remove `write_file`/`run_command`/`spawn_subagent`/etc. from the available schema entirely until Stage 1 completes, instead of exposing them and rejecting after the fact — removes the option to end turn doing nothing
- [ ] Add trace log event (`skill_selection_stalled`) for forced re-prompts, visible via `/trace`
- [ ] Integration test: mock Coder response with no tool call pre-selection → confirm loop re-prompts instead of idling
- [ ] Integration test: mock Coder ignoring re-prompt twice → confirm cap triggers and surfaces a clear error instead of hanging

**Checkpoint:** A Coder turn that ends without selecting a section never silently stalls — it's either forced via a narrowed tool list, retried with a bounded re-prompt, or surfaced as a clear error.

---

## Phase 3: General-mode escalation nudge — make it actionable

**Problem:** The `[System]` note suggesting `/mode triad` for oversight on complex/sensitive tasks is purely cosmetic text — no actual mechanism backs it, so it reads as a dead hint the user can't act on meaningfully (or worse, the underlying complexity/risk detection has no teeth).

**Root cause (likely):** The nudge is a one-way notification emitted by the Orchestrator's complexity/sensitivity heuristic, with no callback wired to actually switch modes or track whether the user acted on it. It's a print statement, not a UI affordance.

- [ ] Confirm current behavior: does anything happen if the user ignores the note and continues in General mode with a genuinely complex/sensitive task? (likely: nothing — no re-check, no escalation)
- [ ] Decide UX: should this be (a) a one-shot nudge only, (b) a nudge that re-fires if task complexity increases mid-session, or (c) an inline quick-action to switch modes without typing `/mode triad`
- [ ] If (c): wire a keybinding or inline confirm prompt in the TUI that switches mode directly from the nudge, rather than requiring the user to type the command
- [ ] Ensure the complexity/sensitivity heuristic that triggers this note is actually logged to `/trace` (so users can see *why* it fired, not just that it fired)
- [ ] Test: trigger a complex/sensitive task in General mode, confirm the nudge fires exactly once per task (not spammed every turn) and the trace shows the trigger reason

**Checkpoint:** The nudge either offers a real one-step escalation path or is at minimum traceable and non-repetitive — no more silent dead-end hints.

---

## Phase 4: TUI fails to render tool call output (e.g. `ask_question`)

**Problem:** A tool call (e.g. `ask_question`) executes — the transcript/log shows it happened — but nothing renders in the TUI. The chat shows "used tool call" with no visible content or question.

**Root cause (likely):** Missing case in the Bubbletea render switch for this entry/tool type, or the tool's result message isn't wrapped in a `tea.Cmd` that triggers a view update (silent state update with no re-render trigger).

- [ ] Reproduce: trigger `ask_question` (or equivalent clarify-phase tool), confirm transcript/JSONL has the entry but TUI shows nothing
- [ ] Audit `internal/tui` render switch for all tool/entry types — confirm every `Type` value used in `internal/transcript.Entry` has a corresponding render branch
- [ ] Specifically check `ask_question`/clarify-phase entries — confirm they emit a proper `tea.Cmd` message rather than a direct state mutation
- [ ] Fix missing render branch and/or missing `tea.Cmd` dispatch
- [ ] Add regression test or manual checklist covering every tool type (`write_file`, `read_file`, `run_command`, `browser_*`, `spawn_subagent`, `spawn_twin_subagent`, clarify/`ask_question`) rendering correctly in TUI
- [ ] Confirm scrollable viewport auto-scrolls to reveal newly rendered tool entries (not just that they exist off-screen)

**Checkpoint:** Every tool call type — especially `ask_question` — visibly renders its content in the TUI, with no silent "nothing happened" gaps.