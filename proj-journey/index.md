# Documentation Index

Welcome to the Triad documentation directory. This page serves as a navigation index for all documentation files in the `proj-journey` folder.

---

## Available Documentation Files

### 1. [Triad — Core Build Workflow (v1)](./work-1.md) (`work-1.md`)

* **Title:** Triad — Build Workflow (v1 Core)
* **Companion Document:** `PROJECT_SPEC.md`
* **Purpose:** Outlines the initial foundational build sequence (Phases 1–9) for Triad's core multi-agent engine, approval loop, TUI, and session persistence.
* **Tech Stack:** Go, `bubbletea` v2 (TUI), `lipgloss` v2, JSON Lines (persistence), OpenCode Zen API.
* **Key Summary & Table of Contents:**
  * **Build Order Strategy:** Headless core loop implementation before TUI integration.
  * **Phase 1:** Transcript + Config (Data structures & JSON Lines persistence)
  * **Phase 2:** Basic LLM Client (OpenAI-compatible client & message models)
  * **Phase 3:** Core Engine Loop (Coder + Reviewer + Human Approval loop)
  * **Phase 4:** Execution Tools & System Prompt (Tool execution engine)
  * **Phase 5:** Auto-Approval Rules Engine (Rule evaluation & security controls)
  * **Phase 6:** Bubbletea v2 TUI Integration (Terminal UI integration)
  * **Phase 7:** Session Persistence & Recovery (State loading & snapshot management)
  * **Phase 8:** Error Recovery & Edge Cases (Robustness & failure handling)
  * **Phase 9:** Final Polish, Performance & Testing (Optimization & unit/integration tests)

---

### 2. [Triad — Workflow 2: Commands, Subagents & Extended Tools](./work-2.md) (`work-2.md`)

* **Title:** Triad — Workflow 2: Commands, Subagents & Extended Tools
* **Companion Document:** `PROJECT_SPEC.md`
* **Purpose:** Details the second phase of development covering custom markdown slash commands, automatic per-edit git commits, isolated subagent spawning, and Playwright DOM browser tools.
* **Tech Stack:** Go, Playwright-Go, Git CLI integration, YAML frontmatter parser, Bubbletea v2 integration.
* **Key Summary & Table of Contents:**
  * **Section 0:** Overview & Motivations (Extending v1 core with daily-driver capabilities)
  * **Section 1:** Slash Commands (Markdown + YAML frontmatter templates in `commands/`, `{{args}}` expansion)
  * **Section 2:** Git Auto-Commit & `/undo` (Automatic per-action commits, `/undo` via `git revert`)
  * **Section 3:** Subagents (Isolated subagent context, `spawn_subagent` tool, summary-only return to transcript)
  * **Section 4:** Extended Tool Calls (Playwright-Go structured DOM browser automation: `browser_navigate`, `click`, `type`, `get_text`, `screenshot`)
  * **Section 5:** Build Order & Architectural Integration
  * **Section 6:** Directory Structure & Package Additions
  * **Section 7:** Carried Forward & Open Items

---

### 3. [Triad — Workflow 3: Orchestrator, Commit Journey & Memory](./work-3.md) (`work-3.md`)

* **Title:** Triad — Workflow 3: Orchestrator, Commit Journey & Memory
* **Companion Document:** `PROJECT_SPEC.md`
* **Purpose:** Outlines the next architectural layer covering task routing by complexity/severity, twin subagent pairs (mini Coder+Reviewer), batched clarify phase, cross-agent trace logging, markdown memory system with human-gated self-learning loop, and commit-journey visualization.
* **Tech Stack:** Go, `lipgloss` styling, Git CLI, Markdown file storage, JSON Lines trace logs.
* **Key Summary & Table of Contents:**
  * **Phase 0:** Research Grounding (Multi-agent orchestration, trace log necessity, memory curation principles, token overhead mitigations)
  * **Phase 1:** Modes Foundation & `/mode` Command (Orchestrator, General Chat, Triad sticky mode switching & persistence)
  * **Phase 2:** Mode Mismatch Notice (Passive non-blocking FYI warning when forced mode mismatches task complexity)
  * **Phase 3:** The Clarify Phase (Upfront batched clarifying questions across all execution modes)
  * **Phase 4:** Orchestrator Routing Logic (Logged routing decisions, auto-proceed extremes & human confirmation in middle)
  * **Phase 5:** Rubric for Orchestrator's Judgment (Concrete criteria for consistent task categorization)
  * **Phase 6:** Twin Subagent (Isolated mini-Triad propose-review-execute loops, summary-only return, depth guard & loop turn cap)
  * **Phase 7:** Observability (Unified cross-agent trace log in `sessions/traces/<session-id>.jsonl` and `/trace` command)
  * **Phase 8:** Memory Structure (Markdown index, preferences, daily logs, topic files, and session-start loading)
  * **Phase 9:** Self-Learning Loop (Automatic extraction of Reviewer objections/corrections to daily logs & human-gated promotion via `/learn`)
  * **Phase 10:** Commit Journey Visualization (Visual linear commit timeline in ASCII TUI and HTML export via `/journey`)

---

## Navigation Quick Links

* [index.md](./index.md) — Documentation index and navigation map (this file)
* [work-1.md](./work-1.md) — Core build workflow (v1)
* [work-2.md](./work-2.md) — Workflow 2 (Commands, Subagents, Git Auto-Commit & Browser Tools)
* [work-3.md](./work-3.md) — Workflow 3 (Orchestrator, Commit Journey, Memory & Twin Subagents)


