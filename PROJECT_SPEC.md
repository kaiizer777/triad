# Triad — Shared-Session Coder/Reviewer Dev Tool

**Status:** Fully Complete — v1 Core (transcript, approval loop, TUI, persistence) + Workflow 2 (slash commands, git auto-commit, subagents, browser tools) fully shipped; 206 tests passing, clean build, zero known blockers
**Owner:** Solo dev project
**Language:** Go
**Interface:** CLI/TUI


---

## 1. Problem Statement

Solo developers using a single AI coding agent for large or critical tasks have no
independent check inside the session itself. The human is the only reviewer, and
under time pressure or on long agentic runs, mistakes (hallucinated APIs, scope
creep, unsafe commands, silent wrong turns) can slip through before the human
notices — because the human is watching one stream, not verifying it against
anything.

Existing tools in the market (Copilot, Cursor, Codex, Claude Code, etc.) have
moved toward internal multi-agent pipelines (Planner → Architect → Implementer →
Tester → Reviewer), but these run **behind the platform**, not as a visible,
user-owned, self-hosted session the developer can watch and steer in real time.
Some developers already run two separate agent CLIs side-by-side manually as a
workaround — there is no lightweight tool that puts multiple agents and the human
**in one shared conversation** where correction happens live, conversationally,
mid-task.

## 2. Core Idea

A single shared chat session with **three participants**:

- **You** (the human) — gives tasks, can speak at any point, can override anyone
- **Coder** (agent) — proposes and executes actual work: file edits, shell commands
- **Reviewer** (agent) — watches every proposed action *before* it executes, can
  approve or object

All three read from and write to the **same transcript**, in order — like a group
chat / team channel — rather than the human relaying messages between two
separate, isolated agent sessions.

## 3. Why This Is a Real Gap (Market Check)

Research as of July 2026 confirms:

- Multi-agent role pipelines (planner/coder/reviewer) exist, but are internal to
  large commercial platforms (Copilot cloud agents, OpenAI Codex multi-agent
  worktrees, Cursor) — not something a solo dev owns, runs locally, or can freely
  inspect/modify.
- A meaningful share of developers already run two agent CLIs in parallel by hand
  as an informal check — validating the underlying need, but with no dedicated
  tool for shared, live, single-transcript visibility.
- Nothing found combines: (a) shared single transcript, (b) two independently
  modeled agents (different providers, so failure modes don't overlap), (c) a
  human peer inside the same thread, (d) fully self-hosted with free/open-weight
  models.

This is a legitimate, currently-unfilled niche — worth building for personal use,
with potential to open-source later.

## 3.1 What's Actually Built

Triad is fully implemented and verified with **206 passing unit and integration tests**. The current codebase consists of modular Go packages adhering strictly to core architectural invariants:

### Packages & Module Ownership
- **`main`**: CLI entrypoint, flag parsing, configuration loading (`config.yaml` / environment), logger setup, and execution runner.
- **`internal/agent`**: OpenAI-compatible API client, multi-agent model specs (`AgentConfig`), system prompt renderers, and JSON tool schemas (`write_file`, `read_file`, `run_command`, `browser_*`, `spawn_subagent`).
- **`internal/loop`**: Headless action-approval state machine (`propose` $\rightarrow$ `review` $\rightarrow$ `execute`), Reviewer veto evaluation, human interjection routing, subagent execution dispatch, and hook triggers.
- **`internal/transcript`**: Append-only JSON Lines session persistence (`Entry` model), file streaming (`sessions/<session-id>.jsonl`), session reloading, and transcript formatting.
- **`internal/tui`**: Responsive terminal interface built with `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2` (dual-column layout with sidebar when width $\ge 75$, scrollable transcript viewport with speaker badges, status bar, and prompt input bar).
- **`internal/commands`**: Markdown slash-command loader and frontmatter/template parser (`commands/*.md` with `{{args}}` expansion).
- **`internal/gitcommit`**: Automated git repository manager (`git init`), atomic file/command commit generation (`git add` + `git commit`), and `/undo` support (`git revert`).
- **`internal/subagent`**: Isolated subagent execution engine (fresh context window, restricted tool schemas, turn cap, summary-only return).
- **`internal/browser`**: Structured Playwright-Go browser manager (`browser_navigate`, `browser_click`, `browser_type`, `browser_get_text`, `browser_screenshot`).
- **`internal/logger`**: Centralized structured file logger (`triad.log`).

### Core Architectural Invariants
1. **Reviewer Has No Tool Access:** Reviewer operates as a pure text-in/text-out model (`HasTools: false`). Tool schemas are exclusively exposed to Coder.
2. **Strict Action Approval Loop:** Every proposed file write, shell command, browser navigation, or subagent spawn must be approved by Reviewer (or overridden by Human interjection) before execution.
3. **Config-Only Model Sourcing:** Model IDs (`mimo-v2.5-free`), base URLs (`https://opencode.ai/zen/v1`), and credentials are completely runtime-configurable via `config.yaml` or environment variables without source recompilation.
4. **Append-Only JSONL Persistence:** Session state and transcript entries are written line-by-line to disk in real time for crash resilience and session resumption.
5. **`tea.Cmd`-Only TUI Concurrency:** Asynchronous background actions (agent HTTP calls, tool execution, git commands) run strictly via Bubbletea commands to prevent UI thread data races.

## 4. Finalized Design Decisions

| Area | Decision |
|---|---|
| **Format** | Single shared transcript, 3 participants (You, Coder, Reviewer) — not two isolated sessions relayed by the human |
| **Turn granularity** | Round-robin **per atomic action**. Every individual file write or shell command goes through a Coder-propose → Reviewer-check cycle before executing. A single task (e.g. "add a webhook handler") loops through this multiple times — once per file/command — not once per whole task |
| **Reviewer visibility** | Full diffs / full command text before execution — never a summary or Coder's self-description of what it did |
| **Approval model** | **Veto power.** Reviewer's approval alone unlocks execution. No mandatory human-in-the-loop gate on any action, including destructive ones (explicitly chosen — full trust placed in Reviewer) |
| **Human role** | Can inject a message at any point in the transcript; your message overrides whatever state the other two were in. Not required to approve each step |
| **Coder tool scope** | File read/write **and** shell command execution (full v1 capability, not a restricted subset) |
| **Session lifecycle** | When Coder + Reviewer agree a task is complete, the session **idles** — stays open, waiting for your next task — rather than terminating |
| **Models** | Ideal end-state is two **different model providers/families** so the two agents don't share the same blind spots (same-model-different-prompt was considered and rejected as a permanent design). **Current v1 reality:** both agents run on the same model — `mimo-v2.5-free` via OpenCode Zen — since it's the only confirmed free/no-billing option available right now. This is a deferred compromise, not an abandoned goal: swapping Reviewer to a different free model/provider later is a config-only change (see Model sourcing row), no code changes needed |
| **Model sourcing** | **Not hardcoded.** Model name, base URL, and API key are all runtime config, not compiled into source. Any OpenAI-compatible chat completions endpoint must be swappable via config alone |
| **Current provider (v1)** | OpenCode Zen (`https://opencode.ai/zen/v1`), free/trial tier, no billing configured. Model: `mimo-v2.5-free` (verify exact model ID string via OpenCode's `/models` command before hardcoding into config, as the literal string may differ). Same model used for both Coder and Reviewer for now — differentiated only by system prompt and `HasTools` |
| **Reviewer tool access** | Reviewer has **no tool-calling support at all**, regardless of which model backs it — pure text in, text out. Only Coder is ever given the tool schema |
| **First build target** | CLI/TUI only. No web UI in v1 |

## 5. Conversation Flow (Reference Example)

Example task: *"Add a Razorpay webhook handler."*

```
You:      "Add a Razorpay webhook handler."

Coder:    Proposes plan (no tool call yet):
          "I'll create handlers/razorpay_webhook.go, verify the HMAC
           signature using the raw request body, parse the payload,
           then wire the route."

Reviewer: Checks the plan.
          "Make sure you verify against the raw body, not parsed JSON,
           or the signature check will fail. Otherwise fine."

Coder:    Proposes concrete action #1 — actual diff for
          razorpay_webhook.go (signature verification using raw body).

Reviewer: Reviews this specific diff.
          → Approve → file is written, result appended to transcript
          → Object  → Coder revises and re-proposes; loop repeats
                       for this same step until Reviewer approves

Coder:    Proposes action #2 — wiring the route into the router.

Reviewer: Reviews diff #2. Approve/object, same as above.

          ... repeats once per remaining atomic action ...

Coder:    "Task complete."
Reviewer: Confirms.

Session:  Idles, waiting for the next task from You.
```

At any point, **You** can type a message — it's inserted into the transcript
immediately and both agents read it on their next turn, regardless of where the
Coder/Reviewer approval cycle currently stands.

## 6. System Architecture (High Level)

### 6.1 Transcript

Append-only ordered log. Single source of truth all three participants read
before acting and write results/messages to.

Each entry:

```go
type Entry struct {
    ID        int
    Speaker   string // "You" | "Coder" | "Reviewer" | "System"
    Type      string // "message" | "proposed_action" | "action_result"
    Content   string // message text, diff, command, or execution output
    Timestamp time.Time
}
```

### 6.2 Agent Abstraction

Both agents are built from the same interface and config shape — only the
config values differ (model, endpoint, whether tools are attached).

```go
type AgentConfig struct {
    Name     string // "Coder" or "Reviewer"
    BaseURL  string // OpenAI-compatible endpoint
    APIKey   string // from env var
    Model    string // model identifier string
    HasTools bool   // true for Coder, false for Reviewer
}

type Agent interface {
    Respond(transcript []Entry) (Response, error)
}
```

- **Coder** config: `HasTools: true`, tool schema attached (`write_file`,
  `read_file`, `run_command`)
- **Reviewer** config: `HasTools: false`, plain chat completion, no schema

Both hit the same class of endpoint (OpenAI-compatible chat completions), so
one HTTP client implementation serves both — differing only by config.

### 6.3 Action-Approval Loop (Core Loop)

```
loop:
    if new human message pending:
        append to transcript, both agents see it next turn

    Coder.Respond(transcript)
        → appends plan/message and/or a proposed_action entry

    if proposed_action exists:
        Reviewer.Respond(transcript)  // sees full diff/command
            → approve: execute action, append action_result
            → object:  append objection; do NOT execute;
                       Coder must revise and re-propose (loop continues
                       on this same atomic action)

    if Coder signals done AND Reviewer confirms:
        session → idle, wait for next human task
```

### 6.4 Tool Execution

- `write_file`, `read_file`: Direct filesystem operations scoped to project working directory.
- `run_command`: Shell execution via `os/exec`, working directory pinned to project root.
- `browser_*` (`browser_navigate`, `click`, `type`, `get_text`, `screenshot`): Structured DOM-level browser control via Playwright-Go.
- `spawn_subagent`: Subagent launcher running in an isolated context with restricted tools, returning a summary to the main transcript.
- **Git Auto-Commit:** Every executed `write_file` or modifying `run_command` triggers an automated git commit (`git add` + `git commit`), establishing an audit trail and enabling `/undo`.
- No sandboxing/human confirmation layer in v1 (explicitly decided) — Reviewer's approval is the only gate.

### 6.5 TUI

- Responsive layout built with `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`.
- Dual-column structure when terminal width $\ge 75$ columns (left sidebar displaying session ID, working directory, system status, retry counters, model engine details; right viewport displaying the scrollable transcript with pill speaker badges `[ You ]`, `[ Coder ]`, `[ Reviewer ]`, `[ System ]`, and tool call panels). Collapses automatically to single-column layout on narrower viewports.
- Bottom-pinned pipeline dock, live status line/spinner, prompt input bar, and integrated slash-command handler (`/plan`, `/diff`, `/undo`, `/status`, `/strict`).
- Strict `tea.Cmd` async event model ensuring all background tool/LLM operations dispatch messages safely to the main update loop.

## 7. Known Risks (Accepted, Not Deferred)

- **No destructive-action gate.** Because Reviewer has full veto/approval power
  and there is no hardcoded human confirmation step, a bad Reviewer approval on
  a destructive shell command (e.g. force-push, recursive delete) will execute
  with no one blocking it except the human noticing in real time. This was
  explicitly chosen in favor of speed/autonomy over safety rails (mitigated after the fact by git auto-commit on every edit).
- **Round-robin ≠ true concurrency.** Turn execution is strictly round-robin per atomic
  action, not two agents genuinely acting simultaneously with live interrupts.
- **Free-tier rate limits.** OpenCode Zen's free/trial endpoints (no billing
  configured) have rate limits for `mimo-v2.5-free`. Handled gracefully via single-threaded round-robin execution and structured error handling.

## 8. Shipped & Remaining Non-Goals

### Shipped in Workflow 2 (Originally Deferred)
- Custom markdown slash commands (`/plan`, `/diff`, `/undo`, `/status`, `/strict`)
- Automatic per-action git commits and `/undo` via `git revert`
- Short-lived isolated subagent delegation (`spawn_subagent`)
- DOM-level browser automation tools (`browser_*`) via Playwright-Go

### Remaining Non-Goals
- No web UI (CLI/TUI only)
- No true simultaneous/interrupt-based turn-taking (strictly round-robin per atomic action)
- No hardcoded destructive-action confirmation step (Reviewer approval remains sole gate)
- No model hardcoding — all model/provider selection is runtime config
- No multi-session switcher dashboard — single active session per process instance

## 9. Known Rough Edges

The codebase is fully verified with 206 passing tests and clean builds. The following 10 unpolished items represent the complete list of acknowledged rough edges:

1. **API Rate-Limit Exponential Backoff:** Basic rate-limit error catching is active, but automated exponential backoff and retry policy for model APIs can be further refined.
2. **CI/CD Pipeline Setup:** Comprehensive unit and integration test suite runs cleanly locally (`go test ./...`), but automated GitHub Actions / CI workflow is not configured.
3. **Browser Manager Idle Shutdown:** Playwright browser subprocess operates on-demand without an automatic idle-timeout process teardown mechanism.
4. **Browser Binary Auto-Installation:** Missing Chromium binaries trigger a start-up warning notice, but background binary downloading/installation on first run is not automated.
5. **Git Commit Conflicts on `/undo`:** `/undo` executes `git revert`, but complex merge conflicts against dirty working trees require manual user resolution.
6. **Observability & Log Management:** System actions log to `triad.log`, but log rotation, file truncation, and structured metrics monitoring are not implemented.
7. **In-Memory TUI State Recovery:** Transcripts persist line-by-line to JSONL files for session recovery, but unsubmitted draft text in the input prompt is lost if the process terminates abruptly.
8. **`run_command` Per-Invocation Timeout Override:** A global execution timeout is configurable via `config.yaml`, but individual tool calls cannot specify custom per-command timeout overrides.
9. **Subagent Nesting Depth Enforcement:** Subagent recursion is strictly capped at depth 1; subagents cannot spawn nested subagents.
10. **Multi-Session Management Dashboard:** Triad operates on a single active session per process run; managing multiple sessions requires running separate terminal windows.