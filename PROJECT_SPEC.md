# Triad — Shared-Session Coder/Reviewer Dev Tool

**Status:** Fully Complete — v1 Core + Workflow 2 + Workflow 3 (Orchestrator, Twin Subagents, Memory, Commit Journey) + Workflow 5 (Task-Driven Skill Injection System) shipped; 16 internal packages, clean build, zero known blockers  
**Owner:** Solo dev project | **Language:** Go | **Interface:** CLI/TUI  

---

## 1. Problem Statement & Market Gap

Solo developers using a single AI coding agent lack in-session verification. Human oversight under time pressure often misses hallucinated APIs, scope creep, or unsafe commands. While commercial platforms offer internal multi-agent pipelines (Planner → Implementer → Reviewer), they run as closed cloud black boxes. Developers manually running separate agent CLIs side-by-side validates the need for a single, transparent, shared transcript where human and agents collaborate in real time.

Triad fills this gap with a lightweight, self-hosted 3-participant chat (Human, Coder, Reviewer) on a single shared transcript with independent model support, backed by task complexity routing, isolated subagents, domain skill injection, and plain markdown project memory.

## 2. Core Idea

A single shared chat session with **three core participant roles**:
- **You (Human)** — gives tasks, injects messages at any point, can override state or forced mode.
- **Coder (Agent)** — proposes and executes atomic actions (file edits, shell commands, browser tools, subagents).
- **Reviewer (Agent)** — inspects every proposed action *before* execution with veto/approval authority.

All participants read from and write to the same append-only transcript in real time.

## 3. Architecture & Implementation Status

Triad is fully implemented and verified with all package unit and integration tests passing cleanly across 16 internal packages.

### Packages & Module Ownership
- **`main`**: CLI entrypoint, flag parsing, `config.yaml`/env configuration loading, logger setup, runner.
- **`internal/agent`**: OpenAI-compatible client, `AgentConfig` specs, system prompt renderers, and tool schemas (`write_file`, `read_file`, `run_command`, `browser_*`, `spawn_subagent`, `spawn_twin_subagent`).
- **`internal/browser`**: Playwright-Go browser manager (`browser_navigate`, `click`, `type`, `get_text`, `screenshot`).
- **`internal/clarify`**: Upfront batched clarifying questions engine across all execution modes.
- **`internal/commands`**: Markdown slash-command loader, parser, and command suite (`/plan`, `/diff`, `/undo`, `/status`, `/summary`, `/strict`, `/mode`, `/trace`, `/learn`, `/journey`, `/skill`).
- **`internal/gitcommit`**: Automated git repository manager (`git init`), per-action commit generator (`git add` + `git commit`), session commit report generator (`GetSessionSummary`), and `/undo` support (`git revert`).
- **`internal/journey`**: Visual linear commit timeline renderer for TUI (ASCII) and exportable standalone HTML file (`/journey`, `/journey --export`).
- **`internal/learn`**: Reviewer objection & human mid-task correction auto-extractor and human-gated memory promotion gate (`/learn`).
- **`internal/logger`**: Structured file logger (`triad.log`).
- **`internal/loop`**: Multi-mode propose-review-execute action-approval state machine (`orchestrator`, `general`, `triad`), Reviewer veto evaluation, human interjection routing, subagent execution dispatch, hook triggers.
- **`internal/memory`**: Markdown memory storage engine (`INDEX.md`, `daily/<date>.md`, `topics/*.md`, `preferences.md`).
- **`internal/skills`**: Task-driven domain skill injection system (`skills/*.md`), YAML frontmatter loader, Stage 1/2 two-stage funnel, Main vs Mini tiering, 3-section cap.
- **`internal/subagent`**: Isolated single-subagent engine (fresh context, restricted tools, depth 1 cap, summary-only return).
- **`internal/tracelog`**: Unified cross-agent trace log engine (`sessions/traces/<session-id>.jsonl`).
- **`internal/transcript`**: Append-only JSONL session persistence (`Entry` model), streaming (`sessions/<session-id>.jsonl`), session reloading, transcript formatting.
- **`internal/tui`**: Terminal UI (`bubbletea/v2`, `lipgloss/v2`) with dual-column layout (sidebar when width ≥ 75, scrollable transcript viewport, speaker badges, status bar, prompt input, skill/journey/trace rendering).
- **`internal/twinsubagent`**: Isolated mini-Triad twin pair engine (mini-Coder + mini-Reviewer, private JSONL transcript `sessions/twins/<id>.jsonl`, summary-only return, depth 1 cap, hard turn cap).

### Core Architectural Invariants
1. **Reviewer Has No Tool Access:** Reviewer operates strictly text-in/text-out (`HasTools: false`). Tool schemas are exclusive to Coder.
2. **Strict Action Approval Loop:** Every file write, shell command, browser action, or subagent spawn requires Reviewer approval (or Human override) before execution in Triad mode.
3. **Config-Only Model Sourcing:** Model IDs (`mimo-v2.5-free`), endpoints (`https://opencode.ai/zen/v1`), and credentials are runtime-configurable via `config.yaml` or env vars without recompilation.
4. **Append-Only JSONL Persistence:** Session state and transcript entries persist line-by-line in real time for crash resilience.
5. **`tea.Cmd`-Only TUI Concurrency:** Async operations (LLM HTTP calls, tool execution, git commands) dispatch strictly via Bubbletea commands to prevent UI thread data races.
6. **Orchestrator Traceability & Rubric-Driven Routing:** Every task routing decision is stated out loud in the transcript and logged as a first-class `routing_decision` event in trace log.
7. **Twin Subagent Isolation & Caps:** Twin subagent mini-Reviewers have no tool access (`HasTools: false`), recursion depth is capped at 1 (twins cannot spawn subagents), and loops have a hard turn cap to protect token budget.
8. **Two-Stage Skill Funnel & Hard 3-Section Cap:** Mandatory Stage 1 section scan before coding actions, Main (1st touch per session, 5–8k tokens) vs Mini (subsequent touches, 2–4k tokens) tiering, and a hard ceiling of 3 sections per task.
9. **Human-Gated Self-Learning:** Reviewer objections and human corrections auto-extract into append-only daily logs (`memory/daily/`); promotion into curated topic files (`topics/*.md`) or `INDEX.md` requires explicit human confirmation via `/learn`.

## 4. Finalized Design Decisions

| Area | Decision |
|---|---|
| **Format** | Single shared transcript, 3 core participants (You, Coder, Reviewer). |
| **Execution Modes** | Three modes: `orchestrator` (default dynamic router), `general` (General Chat, single agent, no review loop), `triad` (full propose-review-execute loop). Sticky via `/mode`. |
| **Turn Granularity** | Round-robin per atomic action (per file edit or shell command). |
| **Reviewer Visibility** | Full diffs and complete command text before execution. |
| **Approval Model** | **Veto power.** Reviewer approval unlocks execution; human can interject anytime. |
| **Coder Tool Scope** | File read/write, shell command execution, browser tools, subagent spawning (`spawn_subagent`, `spawn_twin_subagent`). |
| **Session Lifecycle** | Session idles after task completion, staying open for the next prompt. Mode & active skills persist across session resume. |
| **Model Sourcing** | Runtime-configurable via `config.yaml`/env. Default v1 provider: OpenCode Zen (`mimo-v2.5-free`). |
| **Reviewer Tool Access** | Pure text-in / text-out (`HasTools: false`). |
| **First Build Target** | Terminal CLI/TUI only (no web UI in v1). |
| **Clarify Phase** | Shared upfront batched clarifying questions asked across all modes before execution begins. |
| **Twin Subagents** | Isolated mini-Triad pair (mini-Coder + mini-Reviewer) running private propose-review-execute loop; summary-only return to main transcript. |
| **Observability** | Flat, scannable cross-agent trace log in `sessions/traces/<session-id>.jsonl` rendered via `/trace`. |
| **Project Memory** | Plain Markdown storage: `INDEX.md` (read on session start), `preferences.md`, append-only `daily/<date>.md`, curated `topics/*.md`. |
| **Self-Learning Loop** | End-of-session auto-extraction of objections/corrections to daily logs; human-gated promotion digest via `/learn`. |
| **Commit Journey** | Linear chronological commit timeline rendered in ASCII TUI and exportable standalone HTML via `/journey` (`--export`). |
| **Skill Injection** | Markdown+frontmatter domain skills in `skills/`. Two-stage scan funnel, Main (5-8k) vs Mini (2-4k) tiers, 3-section cap, `/skill` command suite. |

## 5. Conversation Flow

```text
You:      "Add a Razorpay webhook handler."

Orchestrator: [Orchestrator]: "This requires creating new files and handling security HMAC signatures — routing to Triad."
Clarify:      "Should we log failed webhook signature verification attempts to triad.log? (1 question)"
You:          "Yes."

Coder:    [Stage 1 Scan]: Sections selected -> backend
          [Stage 2 Load]: Injected backend Main Skill (1st touch)
          Proposes plan: "Create handlers/razorpay_webhook.go with HMAC verification..."
Reviewer: Checks plan: "Ensure verification uses raw request body. Approved."

Coder:    Proposes action #1 — diff for razorpay_webhook.go.
Reviewer: Reviews diff → Approve → file written, result appended, git commit triggered.

Coder:    Proposes action #2 — wire route into router.
Reviewer: Reviews diff #2 → Approve → route wired, git commit triggered.

Coder:    "Task complete."
Reviewer: Confirms.
Session:  Idles, awaiting next task.
```

## 6. System Architecture

### 6.1 Transcript Entry Model
```go
type Entry struct {
    ID        int
    Speaker   string // "You" | "Coder" | "Reviewer" | "System" | "Orchestrator" | "Twin:<name>"
    Type      string // "message" | "proposed_action" | "action_result" | "routing_decision"
    Content   string // message text, diff, command, or execution output
    Timestamp time.Time
}
```

### 6.2 Action-Approval Core Loop & Modes
```text
loop:
    if new human message: append to transcript (overrides state)
    if current_mode == "orchestrator":
        Orchestrator.Evaluate(task) -> states reasoning in transcript & logs to trace
        if trivial -> route to General Chat (single-agent execution)
        if critical -> route to Triad mode (full propose-review-execute loop)
        if medium -> ask human confirmation ("Proceed with Twin Subagent or Triad?")
    
    run Clarify phase -> batch questions upfront if task is ambiguous
    
    if Coder action:
        run Stage 1 section scan & Stage 2 skill injection (Main vs Mini, max 3 sections)
        Coder.Respond(transcript) -> appends plan/message or proposed_action
        if proposed_action:
            Reviewer.Respond(transcript)
                -> approve: execute action, append action_result, trigger git commit
                -> object:  append objection; Coder must revise and re-propose
    if Coder signals done AND Reviewer confirms:
        trigger self-learning extraction -> append raw entries to memory/daily/<date>.md
        session -> idle
```

### 6.3 Tool Execution & Commands
- `write_file`, `read_file`: Working directory scoped filesystem ops.
- `run_command`: Pinned shell execution via `os/exec`.
- `browser_*`: Playwright-Go DOM automation (`navigate`, `click`, `type`, `get_text`, `screenshot`).
- `spawn_subagent`: Isolated single-subagent execution with summary return.
- `spawn_twin_subagent`: Isolated mini-Triad twin pair execution with summary return.
- **Git Integration & Built-in Slash Commands:**
  - Automated per-action git commits (`[triad] entry #N` / `[triad:twin #ID]`).
  - `/plan`, `/diff`: View proposed plan and diffs.
  - `/undo`: Revert recent action via `git revert`.
  - `/status`: Session state, current mode, active skills, git summary.
  - `/summary`: Local git commit session summary report.
  - `/strict`: Toggle strict approval rules.
  - `/mode`: Inspect or switch sticky execution mode (`orchestrator`, `general`, `triad`).
  - `/trace`: View unified cross-agent activity trace log.
  - `/learn`: Review extracted session objections/corrections and promote to topic files.
  - `/journey`: Render visual ASCII commit timeline or export standalone HTML (`--export`).
  - `/skill`: Suite for managing domain skills (`list`, `view`, `edit`, `add`, `delete`, `force`).

### 6.4 Task-Driven Skill Injection Architecture
- **Skill Storage:** Markdown+frontmatter files stored in `skills/` (`frontend.md`, `backend.md`, `db.md`).
- **Two-Stage Scan Funnel:**
  1. **Stage 1 (Section Scan):** Shows Coder a bare list of unique section labels (~2-3 tokens per entry).
  2. **Stage 2 (Skill Load):** Injects Main Skill (5-8k tokens) on 1st touch per session, Mini Skill (2-4k tokens) on subsequent touches.
- **3-Section Hard Cap:** Coder may select at most 3 sections per task.

### 6.5 Memory & Self-Learning Architecture
- **Filesystem Structure:**
  - `memory/INDEX.md`: Pointer file read at session start (kept <150 lines).
  - `memory/preferences.md`: Human developer preferences.
  - `memory/daily/<date>.md`: Verbatim raw append-only extraction log.
  - `memory/topics/*.md`: Curated topic files (e.g. `architecture.md`, `conventions.md`).
- **Self-Learning Gate:** Extraction mines resolved objections & human corrections into daily log; promotion to topic files requires explicit human review via `/learn`.

## 7. Known Risks (Accepted)

- **No destructive-action gate:** Reviewer approval is sole gate prior to execution (mitigated post-hoc by per-action git auto-commits).
- **Round-robin execution:** Per-action sequential turns, not parallel live agent interrupts.
- **Free-tier rate limits:** Handled via single-threaded turn execution and retry backoff.
- **Multi-agent Token Overhead:** Handled via twin subagent hard turn caps, summary-only returns, and two-stage skill injection tiering.

## 8. Shipped & Non-Goals

### Shipped Deliverables
- **v1 Core:** 3-participant shared transcript, propose-review-execute loop, JSONL persistence, Bubbletea v2 TUI, OpenCode Zen client.
- **Workflow 2:** Slash command loader (`commands/*.md`), automated git commits & `/undo`, isolated subagents (`spawn_subagent`), Playwright-Go browser tools (`browser_*`).
- **Workflow 3:** Orchestrator mode & `/mode`, upfront clarify phase (`internal/clarify`), twin subagent engine (`internal/twinsubagent`), unified trace logging & `/trace` (`internal/tracelog`), plain Markdown memory (`internal/memory`), self-learning loop & `/learn` (`internal/learn`), commit journey TUI & HTML export (`internal/journey`).
- **Workflow 5:** Task-driven skill injection system (`internal/skills`), Stage 1/2 scan funnel, Main/Mini tiering, 3-section cap, `/skill` command suite, starter skills (`frontend.md`, `backend.md`, `db.md`).

### Non-Goals
- No web UI (CLI/TUI only).
- No hardcoded destructive confirmation step.
- No model hardcoding.
- No SQLite or vector database for memory (plain Markdown files only).
- No un-gated auto-promotion of memory without human approval.
- No OpenTelemetry/external tracing server infrastructure.

## 9. Known Rough Edges

1. **API Backoff Refinement:** Basic rate-limit handling active; exponential backoff can be tuned further.
2. **CI/CD Workflow:** Local test suite passes cleanly (`go test ./...`); automated GitHub Actions workflow not configured.
3. **Browser Idle Teardown:** Playwright browser operates on-demand without auto idle shutdown.
4. **Browser Binary Auto-Download:** Missing binaries produce startup warnings rather than background auto-download.
5. **Git Merge Conflicts on `/undo`:** Complex reverts against dirty worktrees require manual resolution.
6. **Log Management:** Logging to `triad.log` without automatic log rotation or truncation.
7. **Draft Input Recovery:** JSONL persists transcript; unsubmitted prompt drafts in input bar are not persisted across crashes.
8. **Per-Invocation Command Timeouts:** Global timeout configurable via `config.yaml`; per-tool timeout overrides omitted.
9. **Subagent Nesting Depth Cap:** Capped at depth 1 (subagents/twins cannot spawn nested subagents).
10. **Single Session Instance:** Single active session per CLI process.