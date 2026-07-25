# 🔺 Triad

> **Shared-Session Multi-Agent Dev Tool for Solo Developers**  
> *Put Yourself, a Coder agent, and a Reviewer agent into a single transparent terminal thread.*

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![Bubbletea v2](https://img.shields.io/badge/TUI-Bubbletea_v2-00F5D4?style=flat-square)](https://charm.sh/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](https://opensource.org/licenses/MIT)
[![NPM Version](https://img.shields.io/badge/npm-v1.0.0-CB3837?style=flat-square&logo=npm)](https://www.npmjs.com/)

---

## ⚡ Overview

**Triad** is a self-hosted, terminal-native development tool that runs a **three-way interactive chat session**:

1. **You** (the human developer) — initiate tasks, steer both agents, and interject at any second.
2. **Coder** (agent) — devises implementation plans, writes files, reads codebase contexts, and runs shell commands.
3. **Reviewer** (agent) — inspects every raw code diff and shell command *before* execution, holding **veto power** to approve or object.

Instead of hiding agent pipelines behind cloud platform black boxes or forcing you to manually relay messages between separate CLIs, **Triad** puts all three participants in **one shared, append-only transcript**.

---

## 💡 Why Triad?

### The Problem with Single-Agent Tools
Solo developers relying on single AI coding agents face a dangerous tradeoff:
* **Fatigue & Blind Spots**: Under time pressure or during long autonomous runs, hallucinated APIs, silent wrong turns, destructive commands, or scope creep slip past human review.
* **Closed Multi-Agent Pipelines**: Commercial tools (Copilot, Cursor, Codex) run multi-agent checks behind proprietary cloud black boxes where you cannot inspect, customize, or control the intermediate exchanges.
* **Manual Workarounds**: Developers often run two separate agent CLIs side-by-side, manually copy-pasting diffs for sanity checks.

### The Triad Solution
* **Transparent Single Thread**: All proposals, objections, tool executions, and human messages are recorded in an append-only JSON Lines transcript.
* **Per-Atomic-Action Veto Loop**: Approval happens per file edit or shell command — not once per massive task. Every action must pass Reviewer check before touching your filesystem.
* **Live Human Interjection**: Type into the prompt at any millisecond. Your message instantly enters the transcript, overriding agent states and immediately redirecting both agents.
* **Model Agnostic & Self-Hosted**: Runs out-of-the-box with free-tier open-weight models (e.g. OpenCode Zen's `mimo-v2.5-free`), or configures easily to any OpenAI-compatible `/chat/completions` endpoint.

---

## 🏗️ Core Architecture & Loop

```text
                  +-------------------------------+
                  |       You (Human Peer)        |
                  | Can interject at any time     |
                  +---------------+---------------+
                                  |
                                  v
                   [ Shared Session Transcript ]
                                  |
            +---------------------+---------------------+
            |                                           |
            v                                           v
  +-------------------+                       +-------------------+
  |   Coder Agent     | -- (Proposed Action) --> |  Reviewer Agent   |
  |  (Has Tool Access)|                       |  (Veto Power)     |
  +-------------------+                       +---------+---------+
            ^                                           |
            |            +------------------------------+
            |            |
            |     [ Approve ]? 
            |      /        \
       (Revises)  No        Yes
            |    /            \
            +---+              v
                     [ Execute Tool Action ]
                     (os.WriteFile / exec.Command)
                               |
                               v
                     [ Append Result to Log ]
```

### The Per-Action Approval Cycle
1. **You** input a high-level task (e.g., `"Add a Razorpay webhook handler with signature validation"`).
2. **Coder** outlines an initial plan and proposes **Action #1** (e.g., `write_file` for signature checking).
3. **Reviewer** checks the full diff content.
   * If **Approved**: The file write / shell command executes immediately and the result is appended to the transcript.
   * If **Objected**: Reviewer's feedback enters the transcript; execution is **blocked**; Coder revises Action #1.
4. The loop repeats until all atomic actions complete, Reviewer confirms completion, and the session enters **Idle** state awaiting your next command.

---

## 🎨 Premium Terminal UI (Charm Bubbletea v2)

Triad features an obsidian-themed terminal interface powered by `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`:

* **Split Dashboard Layout**:
  * **Left Sidebar (Metadata)**: Session ID, Working Directory, System Status, Active Retry Counters, Model Engine details.
  * **Right Viewport**: Smooth-scrolling, high-legibility transcript.
* **Pill Speaker Badging**: Vibrant visual tags distinguishing `[ You ]`, `[ Coder ]`, `[ Reviewer ]`, and `[ System ]`.
* **Tool-Call Code Blocks**: Syntax-styled panels displaying exact file diffs, file reads, and shell command executions.
* **Interactive Status Line & Spinner**: Live thinking indicator so you always know when agents are deliberating.
* **Full Terminal Responsiveness**: Automatic layout recalculation on window resize.

---

## 🚀 Quick Start

### Option 1: Run via `npx` (No installation needed)
```bash
npx triad
```

### Option 2: Global Install via NPM
```bash
npm install -g triad
triad
```

### Option 3: Build from Source (Go 1.25+)
```bash
# Clone the repository
git clone https://github.com/kaiizer777/triad.git
cd triad

# Build the binary
go build -o triad main.go

# Run Triad
./triad
```

---

## ⚙️ Configuration

Triad uses a simple YAML or `.env` configuration file to configure provider credentials and runtime limits.

Create a `config.yaml` in your project root (or copy from `config.yaml.example`):

```yaml
# OpenCode Zen Configuration for Triad
base_url: "https://opencode.ai/zen/v1"
api_key: "YOUR_OPENCODE_API_KEY"
model: "mimo-v2.5-free"

# Optional: execution timeout for shell commands (default: 30s)
command_timeout_seconds: 30
```

### Environment Variables
You can also supply settings via environment variables:
```bash
export OPENCODE_BASE_URL="https://opencode.ai/zen/v1"
export OPENCODE_API_KEY="your-api-key"
export OPENCODE_MODEL="mimo-v2.5-free"
```

---

## 🛠️ Coder Tool Capabilities

Coder has access to three essential filesystem and shell tools:

| Tool Name | Parameters | Description |
|---|---|---|
| `write_file` | `path`, `content` | Writes content to file. Path traversal outside project root is blocked. |
| `read_file` | `path` | Reads file content relative to project root. |
| `run_command` | `command` | Executes shell commands (`cmd.exe /C` on Windows, `sh -c` on Unix) in project directory. |

Coder also has access to a `spawn_subagent` tool (delegates a bounded research/verification sub-task to a short-lived, isolated-context agent) and five structured browser tools (`browser_navigate`, `browser_click`, `browser_type`, `browser_get_text`, `browser_screenshot`) for DOM-level browser control via Playwright.

> **Note on Safety:** Reviewer holds complete veto authority. Reviewer approval unlocks tool execution directly without requiring a manual keypress per step, allowing full agentic velocity while maintaining independent checks.

> **Note on `browser_screenshot`:** Screenshot output (whether written to a file via the `path` argument or returned base64-encoded in the result) is **observational, not a code change**, and is therefore **not auto-committed** to git. `write_file` and `run_command` are the only tools that trigger auto-commit; everything else either reads or produces a transient artifact that you can add to git yourself if you want to keep it. Browser tools are also gated by the Chromium binary being installed — Triad prints a one-line notice at session start if it isn't.

---

## 💾 Persistence & Session Recovery

* **JSON Lines Format**: Transcripts are streamed line-by-line to `sessions/<session-id>.jsonl`.
* **Crash Resilience**: If a session terminates or crashes mid-task, restarting Triad automatically restores the active session, history, and system state seamlessly.

---

## 📂 Project Structure

```text
triad/
├── main.go                      # Application entrypoint & Bubbletea initialization
├── config.yaml.example          # Example configuration file
├── package.json                 # NPM publication manifest & cross-platform build scripts
├── bin/
│   └── triad.js                 # Node.js binary wrapper launcher
├── internal/
│   ├── agent/
│   │   ├── client.go            # OpenAI-compatible HTTP client
│   │   ├── config.go            # Agent configuration loader & specs
│   │   └── tools.go             # Tool schemas (write_file, read_file, run_command)
│   ├── loop/
│   │   └── loop.go              # Headless Coder/Reviewer approval engine & guards
│   ├── transcript/
│   │   ├── entry.go             # Entry struct definitions
│   │   └── transcript.go        # Append-only JSON Lines persistence manager
│   └── tui/
│       ├── model.go             # Bubbletea v2 Model architecture
│       ├── update.go            # Bubbletea v2 Cmd workflows & update handler
│       └── view.go              # Lipgloss v2 UI components & view renderer
└── sessions/                    # Persisted session transcripts (.jsonl)
```

---

## 📄 License

This project is licensed under the **MIT License** — see the [LICENSE](LICENSE) file for details.

---

<p center>
  Built with ❤️ for solo developers who want agentic speed without losing code quality.
</p>
