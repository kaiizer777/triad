# Triad — Shared-Session Coder/Reviewer Dev Tool

**Status:** Fully Complete — v1 Core + Workflow 2 shipped; 206 tests passing, clean build, zero known blockers  
**Owner:** Solo dev project | **Language:** Go | **Interface:** CLI/TUI  

---

## 1. Problem Statement & Market Gap

Solo developers using a single AI coding agent lack in-session verification. Human oversight under time pressure often misses hallucinated APIs, scope creep, or unsafe commands. While commercial platforms offer internal multi-agent pipelines (Planner → Implementer → Reviewer), they run as closed cloud black boxes. Developers manually running separate agent CLIs side-by-side validates the need for a single, transparent, shared transcript where human and agents collaborate in real time.

Triad fills this gap with a lightweight, self-hosted 3-participant chat (Human, Coder, Reviewer) on a single shared transcript with independent model support.

## 2. Core Idea

A single shared chat session with **three participants**:
- **You (Human)** — gives tasks, injects messages at any point, can override state.
- **Coder (Agent)** — proposes and executes atomic actions (file edits, shell commands, browser tools, subagents).
- **Reviewer (Agent)** — inspects every proposed action *before* execution with veto/approval authority.

All participants read from and write to the same append-only transcript in real time.

## 3. Architecture & Implementation Status

Triad is fully implemented and verified with **206 passing unit and integration tests**.

### Packages & Module Ownership
- **`main`**: CLI entrypoint, flag parsing, `config.yaml`/env configuration loading, logger setup, runner.
- **`internal/agent`**: OpenAI-compatible client, `AgentConfig` specs, system prompt renderers, and tool schemas (`write_file`, `read_file`, `run_command`, `browser_*`, `spawn_subagent`).
- **`internal/loop`**: Action-approval state machine (`propose` → `review` → `execute`), Reviewer veto evaluation, human interjection routing, subagent execution dispatch, hook triggers.
- **`internal/transcript`**: Append-only JSONL session persistence (`Entry` model), streaming (`sessions/<session-id>.jsonl`), session reloading, transcript formatting.
- **`internal/tui`**: Terminal UI (`bubbletea/v2`, `lipgloss/v2`) with dual-column layout (sidebar when width ≥ 75, scrollable transcript viewport, speaker badges, status bar, prompt input).
- **`internal/commands`**: Markdown slash-command loader and template parser (`commands/*.md` with `{{args}}` expansion).
- **`internal/gitcommit`**: Automated git repository manager (`git init`), per-action commit generator (`git add` + `git commit`), session commit report generator (`GetSessionSummary`), and `/undo` support (`git revert`).
- **`internal/subagent`**: Isolated subagent engine (fresh context, restricted tools, turn cap, summary-only return).
- **`internal/browser`**: Playwright-Go browser manager (`browser_navigate`, `click`, `type`, `get_text`, `screenshot`).
- **`internal/logger`**: Structured file logger (`triad.log`).

### Core Architectural Invariants
1. **Reviewer Has No Tool Access:** Reviewer operates strictly text-in/text-out (`HasTools: false`). Tool schemas are exclusive to Coder.
2. **Strict Action Approval Loop:** Every file write, shell command, browser action, or subagent spawn requires Reviewer approval (or Human override) before execution.
3. **Config-Only Model Sourcing:** Model IDs (`mimo-v2.5-free`), endpoints (`https://opencode.ai/zen/v1`), and credentials are runtime-configurable via `config.yaml` or env vars without recompilation.
4. **Append-Only JSONL Persistence:** Session state and transcript entries persist line-by-line in real time for crash resilience.
5. **`tea.Cmd`-Only TUI Concurrency:** Async operations (LLM HTTP calls, tool execution, git commands) dispatch strictly via Bubbletea commands to prevent UI thread data races.

## 4. Finalized Design Decisions

| Area | Decision |
|---|---|
| **Format** | Single shared transcript, 3 participants (You, Coder, Reviewer). |
| **Turn Granularity** | Round-robin per atomic action (per file edit or shell command). |
| **Reviewer Visibility** | Full diffs and complete command text before execution. |
| **Approval Model** | **Veto power.** Reviewer approval unlocks execution; human can interject anytime. |
| **Coder Tool Scope** | File read/write, shell command execution, browser tools, subagent spawning. |
| **Session Lifecycle** | Session idles after task completion, staying open for the next prompt. |
| **Model Sourcing** | Runtime-configurable via `config.yaml`/env. Default v1 provider: OpenCode Zen (`mimo-v2.5-free`). |
| **Reviewer Tool Access** | Pure text-in / text-out (`HasTools: false`). |
| **First Build Target** | Terminal CLI/TUI only (no web UI in v1). |

## 5. Conversation Flow

```text
You:      "Add a Razorpay webhook handler."

Coder:    Proposes plan: "Create handlers/razorpay_webhook.go with HMAC verification..."
Reviewer: Checks plan: "Ensure verification uses raw request body. Approved."

Coder:    Proposes action #1 — diff for razorpay_webhook.go.
Reviewer: Reviews diff → Approve → file written, result appended.

Coder:    Proposes action #2 — wire route into router.
Reviewer: Reviews diff #2 → Approve → route wired.

Coder:    "Task complete."
Reviewer: Confirms.
Session:  Idles, awaiting next task.
```

## 6. System Architecture

### 6.1 Transcript Entry Model
```go
type Entry struct {
    ID        int
    Speaker   string // "You" | "Coder" | "Reviewer" | "System"
    Type      string // "message" | "proposed_action" | "action_result"
    Content   string // message text, diff, command, or execution output
    Timestamp time.Time
}
```

### 6.2 Action-Approval Core Loop
```text
loop:
    if new human message: append to transcript (overrides state)
    Coder.Respond(transcript) → appends plan/message or proposed_action
    if proposed_action:
        Reviewer.Respond(transcript)
            → approve: execute action, append action_result, trigger git commit
            → object:  append objection; Coder must revise and re-propose
    if Coder signals done AND Reviewer confirms:
        session → idle
```

### 6.3 Tool Execution & Commands
- `write_file`, `read_file`: Working directory scoped filesystem ops.
- `run_command`: Pinned shell execution via `os/exec`.
- `browser_*`: Playwright-Go DOM automation (`navigate`, `click`, `type`, `get_text`, `screenshot`).
- `spawn_subagent`: Isolated subagent execution with summary return.
- **Git Integration & Slash Commands:** Automated git commits per action, `/undo` (`git revert`), `/summary` (local session commit report without LLM calls), `/status`, `/plan`, `/diff`, `/strict`.

## 7. Known Risks (Accepted)
- **No destructive-action gate:** Reviewer approval is sole gate prior to execution (mitigated post-hoc by per-action git auto-commits).
- **Round-robin execution:** Per-action sequential turns, not parallel live agent interrupts.
- **Free-tier rate limits:** Handled via single-threaded turn execution and retry backoff.

## 8. Shipped & Non-Goals

### Shipped in Workflow 2
- Built-in and custom slash commands (`/plan`, `/diff`, `/undo`, `/status`, `/summary`, `/strict`).
- Automatic per-action git commits, local `/summary` report, and `/undo` via `git revert`.
- Subagent delegation (`spawn_subagent`) and browser automation (`browser_*`).

### Non-Goals
- No web UI (CLI/TUI only).
- No hardcoded destructive confirmation step.
- No model hardcoding.

## 9. Known Rough Edges
1. **API Backoff Refinement:** Basic rate-limit handling active; exponential backoff can be tuned further.
2. **CI/CD Workflow:** Local tests pass cleanly (`go test ./...`); automated GitHub Actions workflow not configured.
3. **Browser Idle Teardown:** Playwright browser operates on-demand without auto idle shutdown.
4. **Browser Binary Auto-Download:** Missing binaries produce startup warnings rather than background auto-download.
5. **Git Merge Conflicts on `/undo`:** Complex reverts against dirty worktrees require manual resolution.
6. **Log Management:** Logging to `triad.log` without automatic log rotation or truncation.
7. **Draft Input Recovery:** JSONL persists transcript; unsubmitted prompt drafts in input bar are not persisted across crashes.
8. **Per-Invocation Command Timeouts:** Global timeout configurable via `config.yaml`; per-tool timeout overrides omitted.
9. **Subagent Nesting Depth Cap:** Capped at depth 1 (subagents cannot spawn nested subagents).
10. **Single Session Instance:** Single active session per CLI process.