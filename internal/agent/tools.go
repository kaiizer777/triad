package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	Type       string                        `json:"type"` // always "object"
	Properties map[string]ToolParamProperty  `json:"properties"`
	Required   []string                      `json:"required"`
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

// ExecuteRunCommand runs command in workDir using the OS shell.
// On Windows this uses "cmd /C"; on Unix it would use "sh -c".
// stdout and stderr are combined in the returned string along with the exit code.
//
// Note: this project runs on Windows, so "cmd /C" is used.
// If ported to Linux/macOS, swap to exec.Command("sh", "-c", command).
func ExecuteRunCommand(workDir, command string) (string, error) {
	cmd := exec.Command("cmd", "/C", command) //nolint:gosec // intentional shell execution
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	output := string(out)

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

// unmarshalToolArgs decodes the raw JSON arguments string from a tool call.
func unmarshalToolArgs(rawJSON string, dst *ExecuteToolArgs) error {
	if rawJSON == "" {
		return nil
	}
	return json.Unmarshal([]byte(rawJSON), dst)
}

// ExecuteTool dispatches a ToolCall to the appropriate executor.
// workDir is the project working directory used to resolve relative paths.
// Returns the output string (to be stored as an action_result entry).
func ExecuteTool(workDir string, call ToolCall) (string, error) {
	var args ExecuteToolArgs
	if err := unmarshalToolArgs(call.Function.Arguments, &args); err != nil {
		return "", fmt.Errorf("ExecuteTool: failed to parse arguments for %q: %w", call.Function.Name, err)
	}

	switch call.Function.Name {
	case "write_file":
		return ExecuteWriteFile(workDir, args.Path, args.Content)
	case "read_file":
		return ExecuteReadFile(workDir, args.Path)
	case "run_command":
		return ExecuteRunCommand(workDir, args.Command)
	case "task_complete":
		// No execution needed — the loop handles this signal itself.
		// Returning the sentinel string "task_complete" lets the loop detect it.
		return "task_complete", nil
	default:
		return "", fmt.Errorf("ExecuteTool: unknown tool name %q", call.Function.Name)
	}
}
