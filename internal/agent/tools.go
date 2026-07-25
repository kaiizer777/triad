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
	Type        string `json:"type"`
	Description string `json:"description"`
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
	Type     string           `json:"type"`     // always "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name of the function and its raw JSON arguments string.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string, e.g. {"path":"foo.go","content":"..."}
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
