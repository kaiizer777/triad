# Documentation Index

Welcome to the Triad documentation directory. This page serves as a navigation index for all documentation files in the `docs` folder.

---

## Available Documentation Files

### 1. [Triad — Build Workflow](./work-1.md) (`work-1.md`)

* **Title:** Triad — Build Workflow
* **Companion Document:** `PROJECT_SPEC.md`
* **Purpose:** Provides an ordered, practical build sequence (Phases 1–9)—outlining what to build, in what order, and with what libraries/APIs.
* **Tech Stack:** Go, `bubbletea` v2 (TUI), flat JSON files (persistence), OpenCode Zen API.
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

## Navigation Quick Links

* [index.md](./index.md) — Documentation index and navigation map (this file)
* [work-1.md](./work-1.md) — Comprehensive build workflow and sequence guide
