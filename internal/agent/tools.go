package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/logger"
)

// ---------------------------------------------------------------------------
// Tool schema types (sent to the API in the "tools" field)
// ---------------------------------------------------------------------------

// ToolSchema represents a single tool definition in the OpenAI function-calling format.
type ToolSchema struct {
	Type     string           `json:"type"` // always "function"
	Function ToolFunctionSpec `json:"function"`
}

// ToolFunctionSpec describes the function's name, description, and parameters.
type ToolFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  ToolParamSchema `json:"parameters"`
}

// ToolParamSchema is the JSON Schema object describing the function's parameters.
type ToolParamSchema struct {
	Type       string                       `json:"type"` // always "object"
	Properties map[string]ToolParamProperty `json:"properties"`
	Required   []string                     `json:"required"`
}

// ToolParamProperty describes a single parameter within a tool's parameter schema.
type ToolParamProperty struct {
	Type        string           `json:"type"`
	Description string           `json:"description,omitempty"`
	Items       *ToolParamSchema `json:"items,omitempty"`
}

// coderToolSchemas holds the three tools available to the Coder agent.
var coderToolSchemas = []ToolSchema{
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "write_file",
			Description: "Write content to a file at the given path (relative to the project working directory). Creates parent directories if needed.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"path": {
						Type:        "string",
						Description: "Relative path to the file (e.g. 'internal/handler.go'). Must not be absolute or contain '..'.",
					},
					"content": {
						Type:        "string",
						Description: "The full text content to write to the file.",
					},
				},
				Required: []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "read_file",
			Description: "Read and return the content of a file at the given path (relative to the project working directory).",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"path": {
						Type:        "string",
						Description: "Relative path to the file (e.g. 'internal/handler.go'). Must not be absolute or contain '..'.",
					},
				},
				Required: []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "run_command",
			Description: "Execute a shell command in the project working directory and return its combined stdout and stderr output along with the exit code.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"command": {
						Type:        "string",
						Description: "The shell command to run (e.g. 'go build ./...' or 'go test ./...').",
					},
				},
				Required: []string{"command"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "task_complete",
			Description: "Signal that the entire requested task has been completed. Call this only when all work is done and verified. The Reviewer will confirm before the session returns to idle.",
			Parameters: ToolParamSchema{
				Type:       "object",
				Properties: map[string]ToolParamProperty{},
				Required:   []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "submit_plan",
			Description: "Submit a structured implementation plan before taking actions on a non-trivial task. Each item needs a stable numeric id and a concise text description. Optionally include status as pending, in_progress, or done; omitted statuses default to pending. After submitting the plan, include plan_item_id in each action when possible.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"plan": {
						Type:        "object",
						Description: "Plan object with an optional revision number and an items array. Each item contains id (integer), text (string), and optional status (pending, in_progress, or done).",
					},
				},
				Required: []string{"plan"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			// spawn_subagent is the opt-in support-research tool (docs/work2.md §3).
			// It runs a short-lived, isolated-context agent with its own transcript
			// and a narrowed tool set (read_file + run_command only). The parent
			// receives only a summary of what the subagent found; the subagent's
			// intermediate file reads and command runs stay out of the main
			// transcript. Use this only for bounded research/verification work
			// (e.g. "scan the existing auth code for how HMAC keys are loaded
			// before I add a new route"). Do NOT use it to do the actual risky
			// work of the task — that still has to go through the normal
			// propose→Reviewer→execute loop.
			Name:        "spawn_subagent",
			Description: "Spawn a short-lived, isolated-context subagent to do bounded research or verification work (e.g. read several files and summarise an existing pattern, run the test suite and summarise failures). The subagent gets its own transcript and a narrower tool set (read_file, run_command only). The parent receives only a summary back. Do NOT use this to do the actual risky work of your task — that still has to go through the normal propose/review/execute loop.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"task": {
						Type:        "string",
						Description: "A short, focused description of what the subagent should investigate or verify (e.g. \"Check how the existing codebase handles HMAC signature verification in handlers/.\"). Be specific — the subagent has no access to the parent transcript.",
					},
					"context": {
						Type:        "string",
						Description: "Optional bounded context to hand to the subagent (relevant code excerpts, file paths, prior decisions). Keep this tight — the subagent's context window is small.",
					},
				},
				Required: []string{"task"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			// spawn_twin_subagent is the medium-complexity routing tool (work.md §Phase 6).
			// It spawns an isolated mini-Triad (mini-Coder + mini-Reviewer pair) that
			// runs its own private propose→review→execute loop and returns only a
			// summary to the parent. Use this for tasks that are too complex for
			// a single-agent reply (General Chat) but not critical enough to require
			// full main-session Triad oversight. The twin pair has a hard turn cap
			// and cannot itself spawn further subagents or twin pairs (depth stops
			// at one level, §6.8). The Orchestrator routes medium-complexity tasks
			// here after human confirmation.
			Name:        "spawn_twin_subagent",
			Description: "Spawn an isolated twin-subagent pair (mini-Coder + mini-Reviewer) to handle a medium-complexity task. The pair runs its own private propose→review→execute loop with a hard turn cap and returns only a single summary. Use for tasks that are clearly scoped to code changes but too complex for General Chat. The pair cannot spawn nested subagents — depth stops at one level.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"task": {
						Type:        "string",
						Description: "A focused description of what the twin pair should implement or fix. Be specific — the twin pair has no access to the parent session transcript.",
					},
					"context": {
						Type:        "string",
						Description: "Optional bounded context for the twin pair (relevant code excerpts, file paths, prior decisions). Keep this tight — the twin pair's context window is small.",
					},
				},
				Required: []string{"task"},
			},
		},
	},
	// Browser tools (docs/work2.md §4.2). These are structured,
	// DOM-level browser control — not raw screenshot-based computer
	// use. They share one long-lived Chromium process owned by the
	// loop / TUI, so multiple tool calls within a session reuse the
	// same page (a click after a navigate operates on the navigated
	// page, just like a human would). Every browser_* call still
	// goes through the same propose→Reviewer→execute approval loop
	// as file/shell tools — Reviewer sees "Coder wants to navigate
	// to https://api.example.com/docs" exactly the same way it sees
	// a write_file diff, and approves or objects the same way.
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_navigate",
			Description: "Navigate the shared browser page to a URL (http or https only). Subsequent browser_click / browser_type / browser_get_text / browser_screenshot calls operate on the page this call loaded. Returns the final URL after any redirects, the HTTP status, and the page title. Use browser_get_text to read the page body — this tool does not return page content.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"url": {
						Type:        "string",
						Description: "The URL to load. Must be an http:// or https:// URL with a non-empty host. file://, javascript:, and data: URLs are rejected.",
					},
				},
				Required: []string{"url"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_click",
			Description: "Click the element matching the given selector on the current page. Waits for the element to be visible before clicking. Use browser_navigate first if the page isn't loaded yet. Prefer the explicit `strategy` hint (role / text / label / placeholder / testid / title / alt) over a raw CSS selector — see the Coder system prompt for the canonical fallback chain.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"selector": {
						Type:        "string",
						Description: "Selector for the element to click. For strategy=role, the form is 'role:name' (e.g. 'button:Sign in'). For strategy=text, the selector is the visible text (prefix 'exact:' for exact match). For strategy=label/placeholder/testid/title/alt, the selector is the associated attribute / text. For strategy=css (default), the selector is a raw CSS / Playwright locator string.",
					},
					"strategy": {
						Type:        "string",
						Description: "Optional. Selector strategy hint. One of: 'role' (selector=\"role:name\", e.g. 'button:Sign in'), 'text' (selector=visible text), 'label' (selector=label / aria-label text), 'placeholder' (selector=placeholder text), 'testid' (selector=data-testid), 'title' (selector=title attribute), 'alt' (selector=alt text), 'css' (default — selector is raw CSS / Playwright locator). Default is 'css' for backward compatibility. Prefer 'role' / 'text' / 'label' for stable, semantic targeting.",
					},
				},
				Required: []string{"selector"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_type",
			Description: "Clear the input element matching selector and type text into it. Equivalent to clicking the field and typing — replaces any existing value. To append or type into a contenteditable element, prefer using a more specific Playwright selector or a separate action. Strategy mirrors browser_click.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"selector": {
						Type:        "string",
						Description: "Selector for the input element. Same strategy-aware form as browser_click: for strategy=role, 'textbox:Email'; for strategy=label, 'Email'; for strategy=placeholder, 'Search'; for strategy=css (default), raw CSS like 'input[name=email]' or '#search-box'.",
					},
					"text": {
						Type:        "string",
						Description: "The text to type. Pass a single space to leave the field visually empty but technically non-empty; an empty string is rejected to avoid accidental clears.",
					},
					"strategy": {
						Type:        "string",
						Description: "Optional. Same enum as browser_click.strategy: 'role', 'text', 'label', 'placeholder', 'testid', 'title', 'alt', 'css' (default). Prefer 'role' or 'label' for form fields.",
					},
				},
				Required: []string{"selector", "text"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_get_text",
			Description: "Read the visible text content of the first element matching the given selector (or of the whole page body if selector is empty / 'body'). Useful for scraping a specific element after navigation — e.g. confirming a form submitted, reading a confirmation message, or pulling a single value out of a page. Long results are truncated. Strategy mirrors browser_click.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"selector": {
						Type:        "string",
						Description: "Selector for the element to read. Defaults to 'body' (the whole visible page text) if omitted. Same strategy-aware form as browser_click. For strategy=role, 'heading:Hello world'; for strategy=text, 'Success'; for strategy=css (default), raw CSS like 'h1' or '.error-message'.",
					},
					"strategy": {
						Type:        "string",
						Description: "Optional. Same enum as browser_click.strategy. Default is 'css'.",
					},
				},
				Required: []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_screenshot",
			Description: "Capture a PNG screenshot of the current page. Use sparingly — text-based tools (browser_get_text, browser_navigate's status/title) are usually enough to verify a page loaded, and screenshots bloat the transcript. Pass a `path` to write the PNG to a file (recommended for full-page captures); omit `path` to receive the PNG base64-encoded in the result, capped to 512KB.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"path": {
						Type:        "string",
						Description: "Optional. Project-relative file path to write the PNG to (e.g. 'screenshots/after-login.png'). If omitted, the PNG is returned base64-encoded inline (capped to 512KB; larger screenshots fail with a clear error).",
					},
					"full_page": {
						Type:        "boolean",
						Description: "Optional. Set to true to capture the entire scrollable page rather than just the current viewport. Default is false (viewport-only, faster, smaller).",
					},
				},
				Required: []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_wait_for",
			Description: "Wait for a specific page signal before continuing (Work 4 Phase 2 — replaces silent fixed delays with an explicit, reviewable, condition-based wait). The wait is bounded by an explicit timeout (default 30s, override via timeout_ms up to 2 minutes). Three wait kinds are supported: 'text' (wait until visible text appears), 'visible' (wait until a selector target becomes visible — reuses the same strategy hint as browser_click), and 'url' (wait until the page URL contains a substring). If the condition never becomes true, the call fails with a clear timeout error rather than hanging the loop.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"kind": {
						Type:        "string",
						Description: "What to wait for. One of: 'text' (wait for visible text — uses the 'text' field), 'visible' (wait for an element to become visible — uses 'selector' and 'strategy'), 'url' (wait for a URL substring — uses the 'url' field).",
					},
					"selector": {
						Type:        "string",
						Description: "Used when kind='visible'. Same strategy-aware form as browser_click: for strategy='role', 'heading:Success'; for strategy='text', 'Success'; for strategy='css' (default), raw CSS like '#result-text'.",
					},
					"strategy": {
						Type:        "string",
						Description: "Optional. Same enum as browser_click.strategy. Used when kind='visible'.",
					},
					"text": {
						Type:        "string",
						Description: "Used when kind='text'. The visible page text to wait for. Substring match by default; pass exact=true for an exact match.",
					},
					"exact": {
						Type:        "boolean",
						Description: "Optional, kind='text' only. If true, wait for an exact text match rather than a substring.",
					},
					"url": {
						Type:        "string",
						Description: "Used when kind='url'. A substring to match against the current page URL (page.WaitForURL's documented default). Useful for waiting for SPA navigations like '/dashboard' or '?error=1'.",
					},
					"timeout_ms": {
						Type:        "integer",
						Description: "Optional. Per-call timeout in milliseconds. 0 means 'use the default (30s)'. Capped at 120000 (2 minutes) so a runaway timeout can't hang the loop.",
					},
				},
				Required: []string{"kind"},
			},
		},
	},
	// Session isolation tools (Work 4 Phase 4). These manage the browser
	// context lifecycle — resetting state between tasks, and optionally
	// preserving login sessions across resets.
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_reset_context",
			Description: "Create a fresh browser context, closing the old one. This resets cookies, localStorage, sessionStorage, and navigation history — all state from previous tasks is wiped. Call this at task boundaries to prevent cross-task contamination. If a storage state was previously saved via browser_save_storage_state, the new context will be seeded with that state (preserving login sessions). No arguments required.",
			Parameters: ToolParamSchema{
				Type:       "object",
				Properties: map[string]ToolParamProperty{},
				Required:   []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_save_storage_state",
			Description: "Capture the current browser context's cookies and localStorage as a saved state. The next browser_reset_context call will restore this state into the new context, preserving login sessions across task boundaries. Use this after logging in to a site if you need the login to persist. Returns a summary of what was captured. No arguments required.",
			Parameters: ToolParamSchema{
				Type:       "object",
				Properties: map[string]ToolParamProperty{},
				Required:   []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "browser_clear_saved_storage",
			Description: "Clear any previously saved storage state. After this call, browser_reset_context will create a truly empty context with no login state. Use when you no longer need the saved login to persist. No arguments required.",
			Parameters: ToolParamSchema{
				Type:       "object",
				Properties: map[string]ToolParamProperty{},
				Required:   []string{},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "web_search",
			Description: "Search the web using Firecrawl and return up to 5 clean Markdown results (title, URL, and Markdown content/snippet). Read-only.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"query": {
						Type:        "string",
						Description: "The search query string.",
					},
				},
				Required: []string{"query"},
			},
		},
	},
	{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "ask_question",
			Description: "Ask the human one or more clarifying questions to resolve genuine ambiguity before proceeding. Use this instead of asking questions in plain text. Execution blocks synchronously until the human answers all questions in the batch.",
			Parameters: ToolParamSchema{
				Type: "object",
				Properties: map[string]ToolParamProperty{
					"questions": {
						Type:        "array",
						Description: "A batch of questions to ask the human.",
						Items: &ToolParamSchema{
							Type: "object",
							Properties: map[string]ToolParamProperty{
								"question": {
									Type:        "string",
									Description: "The clarifying question.",
								},
								"allow_multi_select": {
									Type:        "boolean",
									Description: "If true, allows selecting multiple options.",
								},
								"options": {
									Type:        "array",
									Description: "Available options for the question (2-4 recommended).",
									Items: &ToolParamSchema{
										Type: "object",
										Properties: map[string]ToolParamProperty{
											"label": {
												Type:        "string",
												Description: "The option text.",
											},
											"description": {
												Type:        "string",
												Description: "The tradeoff or reasoning for this option.",
											},
										},
										Required: []string{"label"},
									},
								},
							},
							Required: []string{"question", "options"},
						},
					},
				},
				Required: []string{"questions"},
			},
		},
	},
}

// CoderTools returns the full list of tool schemas for the Coder agent.
func CoderTools() []ToolSchema {
	return coderToolSchemas
}

// ---------------------------------------------------------------------------
// Tool call response types (received from the API in choices[0].message)
// ---------------------------------------------------------------------------

// ToolCall represents a single tool invocation returned by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name of the function and its raw JSON arguments string.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string, e.g. {"path":"foo.go","content":"..."}
}

// ---------------------------------------------------------------------------
// ask_question structured tool call definitions
// ---------------------------------------------------------------------------

type AskQuestionBatch struct {
	Questions []AskQuestion `json:"questions"`
}

type AskQuestion struct {
	Question         string              `json:"question"`
	Options          []AskQuestionOption `json:"options"`
	AllowMultiSelect bool                `json:"allow_multi_select"`
}

type AskQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ---------------------------------------------------------------------------
// Path safety helper
// ---------------------------------------------------------------------------

// safeRelPath validates that the given path is a clean, relative path inside
// workDir. It rejects absolute paths and any path containing ".." segments to
// prevent accidental filesystem escape outside the project.
func safeRelPath(workDir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path safety: absolute paths are not allowed (got %q)", path)
	}

	// Also reject Unix-style absolute paths (e.g. /etc/passwd) on Windows,
	// since filepath.IsAbs returns false for them on that platform.
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path safety: absolute paths are not allowed (got %q)", path)
	}

	// Check for ".." components before any filepath cleaning.
	// Split on both separators to be thorough.
	normalized := filepath.ToSlash(path)
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path safety: '..' traversal is not allowed in path %q", path)
		}
	}

	full := filepath.Join(workDir, path)

	// Double-check after cleaning: the result must still be inside workDir.
	if !strings.HasPrefix(full, filepath.Clean(workDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("path safety: resolved path %q is outside working directory", full)
	}

	return full, nil
}

// ---------------------------------------------------------------------------
// Tool executors
// ---------------------------------------------------------------------------

// ExecuteWriteFile writes content to path (relative to workDir).
// Returns a short confirmation string on success.
func ExecuteWriteFile(workDir, path, content string) (string, error) {
	full, err := safeRelPath(workDir, path)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("write_file: could not create parent directories for %q: %w", path, err)
	}

	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: failed to write %q: %w", path, err)
	}

	return fmt.Sprintf("write_file: wrote %d bytes to %q", len(content), path), nil
}

// ExecuteReadFile reads and returns the content of path (relative to workDir).
func ExecuteReadFile(workDir, path string) (string, error) {
	full, err := safeRelPath(workDir, path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read_file: failed to read %q: %w", path, err)
	}

	return string(data), nil
}

// DefaultCommandTimeout is the default maximum duration for run_command executions.
// A hung shell command will be killed after this duration rather than freezing
// the entire Triad session. Configurable via config.yaml (command_timeout_seconds).
const DefaultCommandTimeout = 30 * time.Second

// ExecuteRunCommand runs command in workDir using the OS shell with a timeout.
// On Windows this uses "cmd /C"; on Unix it would use "sh -c".
// stdout and stderr are combined in the returned string along with the exit code.
// If the command exceeds timeout, it is killed and a timeout error is returned
// along with any partial output captured before the kill.
//
// Note: this project runs on Windows, so "cmd /C" is used.
// If ported to Linux/macOS, swap to exec.Command("sh", "-c", command).
func ExecuteRunCommand(workDir, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cmd", "/C", command) //nolint:gosec // intentional shell execution
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	output := string(out)

	// context.DeadlineExceeded means the command timed out.
	if ctx.Err() == context.DeadlineExceeded {
		logger.L().Warn("run_command timed out",
			"command", command,
			"timeout", timeout.String(),
			"partial_output_bytes", len(output),
		)
		return output, fmt.Errorf("run_command: timed out after %s (command: %q). Partial output: %s", timeout, command, output)
	}

	if err != nil {
		// Include the output even on failure — it usually contains the error message.
		return output, fmt.Errorf("run_command: command %q exited with error: %w\noutput: %s", command, err, output)
	}

	return output, nil
}

// ---------------------------------------------------------------------------
// Tool dispatcher
// ---------------------------------------------------------------------------

// ExecuteToolArgs holds the decoded arguments for any tool call.
type ExecuteToolArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Command string `json:"command"`
	Query   string `json:"query"`
}

// SpawnSubagentArgs holds the decoded arguments for a spawn_subagent tool call.
// Only the headless loop and the TUI should construct / decode this — see
// docs/work2.md §3.2.2. `Task` is required; `Context` is optional.
type SpawnSubagentArgs struct {
	Task    string `json:"task"`
	Context string `json:"context"`
}

// ErrMalformedToolCall is returned when tool call arguments cannot be parsed
// at all (not just missing optional fields). The session continues — the error
// is surfaced as a transcript entry rather than crashing.
type ErrMalformedToolCall struct {
	ToolName string
	Raw      string
	Cause    error
}

func (e *ErrMalformedToolCall) Error() string {
	return fmt.Sprintf("malformed tool call arguments for %q (raw: %.120s): %v", e.ToolName, e.Raw, e.Cause)
}

func (e *ErrMalformedToolCall) Unwrap() error { return e.Cause }

// unmarshalToolArgs decodes the raw JSON arguments string from a tool call.
// It uses json.Decoder with DisallowUnknownFields disabled so that extra/unknown
// fields from the model are silently ignored rather than causing a parse failure.
// Missing required fields are validated separately per tool by ExecuteTool.
func unmarshalToolArgs(toolName, rawJSON string, dst *ExecuteToolArgs) error {
	if rawJSON == "" || rawJSON == "{}" {
		return nil
	}
	if err := json.Unmarshal([]byte(rawJSON), dst); err != nil {
		logger.L().Warn("malformed tool call arguments",
			"tool", toolName,
			"raw_args", rawJSON,
			"error", err.Error(),
		)
		return &ErrMalformedToolCall{ToolName: toolName, Raw: rawJSON, Cause: err}
	}
	return nil
}

// validateToolArgs checks that required fields are non-empty for the given tool.
// Returns a descriptive error for any missing required field.
func validateToolArgs(toolName string, args ExecuteToolArgs) error {
	switch toolName {
	case "write_file":
		if args.Path == "" {
			return fmt.Errorf("write_file: required argument 'path' is missing or empty")
		}
		// content may legitimately be an empty string (writing an empty file is valid)
	case "read_file":
		if args.Path == "" {
			return fmt.Errorf("read_file: required argument 'path' is missing or empty")
		}
	case "run_command":
		if args.Command == "" {
			return fmt.Errorf("run_command: required argument 'command' is missing or empty")
		}
	case "web_search":
		if args.Query == "" {
			return fmt.Errorf("web_search: required argument 'query' is missing or empty")
		}
	}
	return nil
}

// ExecuteTool dispatches a ToolCall to the appropriate executor.
// workDir is the project working directory used to resolve relative paths.
// commandTimeout is the maximum duration for run_command executions (0 → DefaultCommandTimeout).
// Returns the output string (to be stored as an action_result entry).
//
// On malformed tool call arguments, ExecuteTool returns a descriptive error string
// prefixed with "System: " rather than a Go error, so the TUI can surface it in
// the transcript without crashing the session.
func ExecuteTool(workDir string, call ToolCall, commandTimeout time.Duration) (string, error) {
	logger.L().Debug("executing tool",
		"tool", call.Function.Name,
		"id", call.ID,
		"args", call.Function.Arguments,
	)

	var args ExecuteToolArgs
	if err := unmarshalToolArgs(call.Function.Name, call.Function.Arguments, &args); err != nil {
		// Malformed JSON — surface as a readable result, not a Go error.
		// The TUI writes this as an action_result entry so the session continues.
		msg := fmt.Sprintf("System: %v", err)
		logger.L().Warn("tool call skipped due to malformed arguments",
			"tool", call.Function.Name,
			"message", msg,
		)
		return msg, nil
	}

	// Validate required fields after JSON parse.
	if err := validateToolArgs(call.Function.Name, args); err != nil {
		msg := fmt.Sprintf("System: %v", err)
		logger.L().Warn("tool call skipped due to missing required argument",
			"tool", call.Function.Name,
			"message", msg,
		)
		return msg, nil
	}

	var result string
	var execErr error

	switch call.Function.Name {
	case "write_file":
		result, execErr = ExecuteWriteFile(workDir, args.Path, args.Content)
	case "read_file":
		result, execErr = ExecuteReadFile(workDir, args.Path)
	case "run_command":
		result, execErr = ExecuteRunCommand(workDir, args.Command, commandTimeout)
	case "task_complete":
		// No execution needed — the loop handles this signal itself.
		// Returning the sentinel string "task_complete" lets the loop detect it.
		result = "task_complete"
	case "spawn_subagent":
		// Intentionally not executed here. The headless loop and the TUI
		// intercept spawn_subagent BEFORE ExecuteTool runs, because the
		// subagent runner needs caller context (the loop's client, the
		// working directory, the session path, the subagent session dir)
		// that ExecuteTool doesn't have. If a spawn_subagent call lands
		// here, it means the caller forgot to intercept it; surface a
		// clear, debuggable error rather than silently falling through
		// to the "unknown tool" default.
		execErr = fmt.Errorf("ExecuteTool: spawn_subagent must be intercepted by the caller (headless loop or TUI) before ExecuteTool is invoked; got here directly")
	case "spawn_twin_subagent":
		// Same interception contract as spawn_subagent — the twin runner
		// needs the parent loop's client, session dir, and Coder config.
		// If this reaches ExecuteTool directly the caller forgot to intercept.
		execErr = fmt.Errorf("ExecuteTool: spawn_twin_subagent must be intercepted by the caller (headless loop or TUI) before ExecuteTool is invoked; got here directly")
	case "ask_question":
		execErr = fmt.Errorf("ExecuteTool: ask_question must be intercepted by the caller (headless loop or TUI) before ExecuteTool is invoked; got here directly")
	case "web_search":
		apiKey := os.Getenv("FIRECRAWL_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("SEARCH_API_KEY")
		}
		result, execErr = ExecuteWebSearch(args.Query, apiKey)
	default:
		execErr = fmt.Errorf("ExecuteTool: unknown tool name %q", call.Function.Name)
	}

	if execErr != nil {
		logger.L().Warn("tool execution error",
			"tool", call.Function.Name,
			"error", execErr.Error(),
		)
	} else {
		truncated := result
		if len(truncated) > 200 {
			truncated = truncated[:200] + "... [truncated]"
		}
		logger.L().Debug("tool execution success",
			"tool", call.Function.Name,
			"result_preview", truncated,
		)
	}

	return result, execErr
}
