package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Path safety tests
// ---------------------------------------------------------------------------

func TestSafeRelPath_RejectsAbsolutePaths(t *testing.T) {
	workDir := t.TempDir()

	absolutePaths := []string{
		`C:\Windows\System32\evil.txt`,
		`/etc/passwd`,
		`/tmp/evil`,
	}

	for _, p := range absolutePaths {
		t.Run(p, func(t *testing.T) {
			_, err := safeRelPath(workDir, p)
			if err == nil {
				t.Fatalf("expected error for absolute path %q, got nil", p)
			}
			if !strings.Contains(err.Error(), "absolute") {
				t.Errorf("expected 'absolute' in error, got: %v", err)
			}
		})
	}
}

func TestSafeRelPath_RejectsDotDotTraversal(t *testing.T) {
	workDir := t.TempDir()

	traversalPaths := []string{
		`../evil.txt`,
		`foo/../../evil.txt`,
		`../../../etc/passwd`,
	}

	for _, p := range traversalPaths {
		t.Run(p, func(t *testing.T) {
			_, err := safeRelPath(workDir, p)
			if err == nil {
				t.Errorf("expected error for traversal path %q, got nil", p)
			}
		})
	}
}

func TestSafeRelPath_AcceptsValidRelativePaths(t *testing.T) {
	workDir := t.TempDir()

	validPaths := []string{
		"hello.txt",
		"internal/handler.go",
		"a/b/c/deep.txt",
	}

	for _, p := range validPaths {
		t.Run(p, func(t *testing.T) {
			result, err := safeRelPath(workDir, p)
			if err != nil {
				t.Fatalf("unexpected error for valid path %q: %v", p, err)
			}
			expected := filepath.Join(workDir, p)
			if result != expected {
				t.Errorf("expected %q, got %q", expected, result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ExecuteWriteFile / ExecuteReadFile round-trip
// ---------------------------------------------------------------------------

func TestExecuteWriteFile_CreatesFile(t *testing.T) {
	workDir := t.TempDir()

	msg, err := ExecuteWriteFile(workDir, "hello.txt", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "hello.txt") {
		t.Errorf("expected confirmation message to mention 'hello.txt', got %q", msg)
	}

	data, err := os.ReadFile(filepath.Join(workDir, "hello.txt"))
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected file content 'hello world', got %q", string(data))
	}
}

func TestExecuteWriteFile_CreatesParentDirs(t *testing.T) {
	workDir := t.TempDir()

	_, err := ExecuteWriteFile(workDir, "a/b/c/deep.txt", "nested content")
	if err != nil {
		t.Fatalf("unexpected error creating nested path: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, "a/b/c/deep.txt"))
	if err != nil {
		t.Fatalf("nested file was not created: %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("expected 'nested content', got %q", string(data))
	}
}

func TestExecuteReadFile_ReadsExistingFile(t *testing.T) {
	workDir := t.TempDir()

	// Write a file directly so we know the content.
	target := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(target, []byte("read me"), 0o644); err != nil {
		t.Fatalf("setup: could not write file: %v", err)
	}

	content, err := ExecuteReadFile(workDir, "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "read me" {
		t.Errorf("expected 'read me', got %q", content)
	}
}

func TestExecuteReadFile_MissingFileReturnsError(t *testing.T) {
	workDir := t.TempDir()

	_, err := ExecuteReadFile(workDir, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestExecuteWriteFile_ThenReadFile_RoundTrip(t *testing.T) {
	workDir := t.TempDir()
	content := "package main\n\nfunc main() {}\n"

	_, err := ExecuteWriteFile(workDir, "main.go", content)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	got, err := ExecuteReadFile(workDir, "main.go")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	if got != content {
		t.Errorf("round-trip mismatch:\nwant: %q\n got: %q", content, got)
	}
}

// ---------------------------------------------------------------------------
// ExecuteTool dispatcher
// ---------------------------------------------------------------------------

func TestExecuteTool_WriteFile(t *testing.T) {
	workDir := t.TempDir()

	call := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"path":"output.txt","content":"dispatched"}`,
		},
	}

	result, err := ExecuteTool(workDir, call, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "output.txt") {
		t.Errorf("expected result to mention 'output.txt', got %q", result)
	}

	data, _ := os.ReadFile(filepath.Join(workDir, "output.txt"))
	if string(data) != "dispatched" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

func TestExecuteTool_ReadFile(t *testing.T) {
	workDir := t.TempDir()
	os.WriteFile(filepath.Join(workDir, "src.txt"), []byte("source content"), 0o644) //nolint:errcheck

	call := ToolCall{
		ID:   "call_2",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "read_file",
			Arguments: `{"path":"src.txt"}`,
		},
	}

	result, err := ExecuteTool(workDir, call, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "source content" {
		t.Errorf("expected 'source content', got %q", result)
	}
}

func TestExecuteTool_UnknownToolReturnsError(t *testing.T) {
	_, err := ExecuteTool(t.TempDir(), ToolCall{
		Function: ToolCallFunction{Name: "delete_everything", Arguments: "{}"},
	}, 0)
	if err == nil {
		t.Fatal("expected error for unknown tool name, got nil")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' in error, got: %v", err)
	}
}

// TestExecuteTool_SpawnSubagentMustBeIntercepted verifies that ExecuteTool
// refuses to dispatch spawn_subagent directly. The headless loop and the TUI
// are responsible for intercepting spawn_subagent before it gets here, because
// the subagent runner needs caller context that ExecuteTool doesn't have. If
// a call ever reaches this function, the caller has a bug and we want a
// debuggable error rather than silent fallback to "unknown tool".
func TestExecuteTool_SpawnSubagentMustBeIntercepted(t *testing.T) {
	_, err := ExecuteTool(t.TempDir(), ToolCall{
		Function: ToolCallFunction{
			Name:      "spawn_subagent",
			Arguments: `{"task":"x","context":"y"}`,
		},
	}, 0)
	if err == nil {
		t.Fatal("expected error when spawn_subagent reaches ExecuteTool directly, got nil")
	}
	if !strings.Contains(err.Error(), "intercepted by the caller") {
		t.Errorf("expected 'intercepted by the caller' in error, got: %v", err)
	}
}

// TestExecuteTool_MalformedArguments verifies that a completely unparseable JSON
// argument string does NOT crash the session (no Go error returned). Instead,
// ExecuteTool returns a "System: malformed..." result string that the TUI can
// write as an action_result entry, keeping the session alive.
func TestExecuteTool_MalformedArguments(t *testing.T) {
	workDir := t.TempDir()
	result, err := ExecuteTool(workDir, ToolCall{
		Function: ToolCallFunction{
			Name:      "write_file",
			Arguments: `{not valid json`,
		},
	}, 0)
	// Malformed JSON must NOT return a Go error — it must return a System: message.
	if err != nil {
		t.Fatalf("expected nil error for malformed JSON (graceful recovery), got: %v", err)
	}
	if !strings.HasPrefix(result, "System: ") {
		t.Errorf("expected result to start with 'System: ', got %q", result)
	}
	if !strings.Contains(result, "write_file") {
		t.Errorf("expected result to mention tool name 'write_file', got %q", result)
	}
}

// ---------------------------------------------------------------------------
// ToolSchema JSON serialization (3.1 — schema shape verification)
// ---------------------------------------------------------------------------

func TestCoderTools_JSONShape(t *testing.T) {
	tools := CoderTools()
	// 5 base tools (write_file, read_file, run_command, task_complete,
	// spawn_subagent) + 5 browser_* tools + 1 web_search tool.
	if len(tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Function.Name] = true

		if tool.Type != "function" {
			t.Errorf("tool %q: expected type 'function', got %q", tool.Function.Name, tool.Type)
		}
		if tool.Function.Description == "" {
			t.Errorf("tool %q: description must not be empty", tool.Function.Name)
		}
		if tool.Function.Parameters.Type != "object" {
			t.Errorf("tool %q: parameters.type must be 'object', got %q", tool.Function.Name, tool.Function.Parameters.Type)
		}
		// task_complete, browser_get_text, and browser_screenshot
		// intentionally have zero required parameters (the latter
		// two default to reading the page body / taking a viewport
		// screenshot).
		if len(tool.Function.Parameters.Required) == 0 &&
			tool.Function.Name != "task_complete" &&
			tool.Function.Name != "browser_get_text" &&
			tool.Function.Name != "browser_screenshot" {
			t.Errorf("tool %q: must have at least one required parameter", tool.Function.Name)
		}
	}

	required := []string{
		"write_file", "read_file", "run_command", "task_complete", "spawn_subagent",
		"browser_navigate", "browser_click", "browser_type", "browser_get_text", "browser_screenshot",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected tool %q to be present", name)
		}
	}
}

func TestCoderTools_JSONRoundTrip(t *testing.T) {
	tools := CoderTools()

	data, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("failed to marshal tool schemas: %v", err)
	}

	var decoded []ToolSchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal tool schemas: %v", err)
	}

	if len(decoded) != len(tools) {
		t.Errorf("expected %d tools after round-trip, got %d", len(tools), len(decoded))
	}
}

// ---------------------------------------------------------------------------
// Phase 9 hardening tests
// ---------------------------------------------------------------------------

// TestExecuteTool_MissingRequiredPath verifies that write_file with a missing
// 'path' argument returns a System: message and nil error (graceful recovery).
func TestExecuteTool_MissingRequiredPath(t *testing.T) {
	workDir := t.TempDir()
	result, err := ExecuteTool(workDir, ToolCall{
		Function: ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"content":"hello world"}`, // 'path' is missing
		},
	}, 0)
	if err != nil {
		t.Fatalf("expected nil error for missing 'path' (graceful recovery), got: %v", err)
	}
	if !strings.HasPrefix(result, "System: ") {
		t.Errorf("expected result to start with 'System: ', got %q", result)
	}
	if !strings.Contains(result, "path") {
		t.Errorf("expected result to mention 'path', got %q", result)
	}
}

// TestExecuteTool_MissingRequiredCommand verifies that run_command with a missing
// 'command' argument returns a System: message and nil error.
func TestExecuteTool_MissingRequiredCommand(t *testing.T) {
	workDir := t.TempDir()
	result, err := ExecuteTool(workDir, ToolCall{
		Function: ToolCallFunction{
			Name:      "run_command",
			Arguments: `{}`, // 'command' is missing
		},
	}, 0)
	if err != nil {
		t.Fatalf("expected nil error for missing 'command' (graceful recovery), got: %v", err)
	}
	if !strings.HasPrefix(result, "System: ") {
		t.Errorf("expected result to start with 'System: ', got %q", result)
	}
	if !strings.Contains(result, "command") {
		t.Errorf("expected result to mention 'command', got %q", result)
	}
}

// TestErrMalformedToolCall_Unwrap verifies the ErrMalformedToolCall type correctly
// wraps its cause so errors.Is/As can reach the underlying parse error.
func TestErrMalformedToolCall_Unwrap(t *testing.T) {
	cause := errors.New("synthetic parse error")
	wrapped := &ErrMalformedToolCall{
		ToolName: "write_file",
		Raw:      `{bad`,
		Cause:    cause,
	}

	if !errors.Is(wrapped, cause) {
		t.Errorf("errors.Is should find cause through ErrMalformedToolCall.Unwrap()")
	}
	if !strings.Contains(wrapped.Error(), "write_file") {
		t.Errorf("expected error to mention tool name, got: %v", wrapped)
	}
}
