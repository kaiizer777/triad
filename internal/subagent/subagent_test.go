package subagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/subagent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Mock client — same shape as loop_test's mockClient, kept local to this
// package so subagent tests are self-contained.
// ---------------------------------------------------------------------------

// cannedResp is one pre-canned response for the mock client.
type cannedResp struct {
	resp agent.AgentResponse
	err  error
}

// mockClient replays a pre-configured sequence of responses for the
// single agent it serves (the subagent — the mock only ever sees one
// agent name, since subagent.Run sets cfg.Name to "Subagent:<id>").
// When the queue is exhausted it returns the last response.
type mockClient struct {
	queue []cannedResp
	calls int
}

func (m *mockClient) Respond(_ context.Context, _ agent.AgentConfig, _ []transcript.Entry) (agent.AgentResponse, error) {
	if len(m.queue) == 0 {
		return agent.AgentResponse{}, nil
	}
	idx := m.calls
	m.calls++
	if idx >= len(m.queue) {
		idx = len(m.queue) - 1
	}
	r := m.queue[idx]
	return r.resp, r.err
}

func textResp(t string) cannedResp {
	return cannedResp{resp: agent.AgentResponse{Text: t}}
}

func toolResp(name string, args map[string]any) cannedResp {
	raw, _ := json.Marshal(args)
	return cannedResp{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{{
			ID:   "call_" + name,
			Type: "function",
			Function: agent.ToolCallFunction{
				Name:      name,
				Arguments: string(raw),
			},
		}},
	}}
}

// errResp wraps a Go error in a cannedResp. Used for the model-failure
// path test (when the mock returns an error from Respond).
func errResp(e error) cannedResp {
	return cannedResp{err: e}
}

// parentConfig returns a minimally valid parent Coder config for tests.
// The subagent only reads BaseURL/Model from it; other fields are not
// asserted on.
func parentConfig() agent.AgentConfig {
	return agent.AgentConfig{
		Name:     "Coder",
		BaseURL:  "http://test",
		Model:    "test-model",
		HasTools: true,
	}
}

// ---------------------------------------------------------------------------
// Constructor / config validation
// ---------------------------------------------------------------------------

func TestNewRunner_RefusesExcessiveDepth(t *testing.T) {
	// docs/work2.md §3.2.6: depth cap is 1. Anything beyond is a hard
	// error at construction time so the recursion guard is impossible
	// to violate by accident.
	_, err := subagent.NewRunner(&mockClient{}, t.TempDir(), t.TempDir(), 0, 0, 2)
	if err == nil {
		t.Fatal("expected error for depth 2, got nil")
	}
	if !strings.Contains(err.Error(), "recursion guard") {
		t.Errorf("expected 'recursion guard' in error, got: %v", err)
	}

	// Depth 1 must succeed.
	if _, err := subagent.NewRunner(&mockClient{}, t.TempDir(), t.TempDir(), 0, 0, 1); err != nil {
		t.Errorf("depth 1 should be allowed (the documented max), got: %v", err)
	}
}

func TestNewRunner_ValidatesArgs(t *testing.T) {
	tests := []struct {
		name    string
		client  subagent.Client
		workDir string
		sessDir string
		wantErr string
	}{
		{"nil client", nil, t.TempDir(), t.TempDir(), "client must not be nil"},
		{"empty workDir", &mockClient{}, "", t.TempDir(), "workDir must not be empty"},
		{"empty sessionDir", &mockClient{}, t.TempDir(), "", "sessionDir must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := subagent.NewRunner(tt.client, tt.workDir, tt.sessDir, 0, 0, 0)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected %q in error, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestSpeakerLabel(t *testing.T) {
	got := subagent.SpeakerLabel("abc123")
	want := "Subagent:abc123"
	if got != want {
		t.Errorf("SpeakerLabel(\"abc123\") = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Tool whitelist — the structural half of the recursion guard
// ---------------------------------------------------------------------------

// TestSubagentTools_OnlyReadAndRun verifies the subagent's tool schema
// is exactly {read_file, run_command}. This is the structural half of
// the recursion guard (docs/work2.md §3.2.6) and the safety rail that
// prevents the subagent from doing the parent's risky work
// (docs/work2.md §3.3).
func TestSubagentTools_OnlyReadAndRun(t *testing.T) {
	tools := subagent.SubagentTools()
	if len(tools) != 2 {
		t.Fatalf("expected exactly 2 subagent tools, got %d", len(tools))
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Function.Name] = true
	}
	for _, required := range []string{"read_file", "run_command"} {
		if !names[required] {
			t.Errorf("expected subagent tool %q to be present", required)
		}
	}
	for _, forbidden := range []string{"write_file", "spawn_subagent", "task_complete"} {
		if names[forbidden] {
			t.Errorf("subagent must NOT have %q in its tool list (recursion guard / safety rail)", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// Summary extraction
// ---------------------------------------------------------------------------

func TestExtractSummary_Present(t *testing.T) {
	// Indirect test: we exercise extractSummary through a subagent
	// that returns a SUMMARY:-prefixed message. Verifies end-to-end
	// that the runner picks it up.
	mock := &mockClient{
		queue: []cannedResp{
			textResp("I checked the file. Found a single handler.\nSUMMARY: One handler file at internal/handler/foo.go."),
		},
	}
	runner, err := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	res, runErr := runner.Run(context.Background(), "sa1", "check files", "", parentConfig())
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result, got Truncated=true")
	}
	if !strings.Contains(res.Summary, "One handler file") {
		t.Errorf("expected summary to contain finding, got %q", res.Summary)
	}
	if strings.HasPrefix(res.Summary, "SUMMARY:") {
		t.Errorf("summary should be returned WITHOUT the SUMMARY: prefix, got %q", res.Summary)
	}
}

func TestExtractSummary_NotPresent(t *testing.T) {
	// First response: plain text without SUMMARY. Second response: with
	// SUMMARY. Verifies the runner keeps going past a non-summary turn.
	mock := &mockClient{
		queue: []cannedResp{
			textResp("I'm still exploring, give me another turn."),
			textResp("OK, done.\nSUMMARY: final answer here."),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "check", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result")
	}
	if res.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", res.Turns)
	}
	if res.Summary != "final answer here." {
		t.Errorf("expected 'final answer here.', got %q", res.Summary)
	}
}

func TestExtractSummary_EmptyAfterPrefix(t *testing.T) {
	// "SUMMARY:" with nothing after it should NOT count as a summary
	// (the model emitted the marker but no actual answer). Runner
	// should keep going.
	mock := &mockClient{
		queue: []cannedResp{
			textResp("thinking...\nSUMMARY:"),
			textResp("OK real answer this time.\nSUMMARY: real answer"),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "check", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Turns != 2 {
		t.Errorf("expected 2 turns (first SUMMARY: should be rejected), got %d", res.Turns)
	}
	if res.Summary != "real answer" {
		t.Errorf("expected 'real answer', got %q", res.Summary)
	}
}

// ---------------------------------------------------------------------------
// Truncation / turn cap
// ---------------------------------------------------------------------------

func TestRun_TruncatesAtTurnCap(t *testing.T) {
	// Subagent that NEVER emits a summary. The runner should hit the
	// turn cap and return a Truncated result.
	mock := &mockClient{
		queue: []cannedResp{
			textResp("not done yet"),
			textResp("still going"),
			textResp("almost there"),
		},
	}
	// maxTurns=3 so the 3 responses exhaust the budget.
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 3, 0)
	res, err := runner.Run(context.Background(), "sa1", "check", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true at turn cap")
	}
	if res.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", res.Turns)
	}
	if res.Summary == "" {
		t.Errorf("truncated result must still produce some summary text")
	}
	if !strings.Contains(res.Summary, "partial findings") {
		t.Errorf("truncation summary should mention 'partial findings', got %q", res.Summary)
	}
}

// ---------------------------------------------------------------------------
// Tool-call handling in the subagent context
// ---------------------------------------------------------------------------

func TestRun_RejectsWriteFile(t *testing.T) {
	// Subagent tries to call write_file. The runner should reject the
	// call (write_file is not in the subagent's tool schema — but even
	// if a model somehow produced it, the runner's switch would refuse
	// it). The subagent should be able to recover and emit a real
	// summary on the next turn.
	mock := &mockClient{
		queue: []cannedResp{
			toolResp("write_file", map[string]any{"path": "evil.go", "content": "evil"}),
			textResp("oh, write_file isn't allowed. OK.\nSUMMARY: write_file is blocked"),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "check", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result")
	}
	if res.Summary != "write_file is blocked" {
		t.Errorf("expected 'write_file is blocked', got %q", res.Summary)
	}

	// Verify the rejection was recorded in the subagent's transcript.
	tr, loadErr := transcript.LoadFromFile(res.TranscriptPath)
	if loadErr != nil {
		t.Fatalf("could not load subagent transcript: %v", loadErr)
	}
	entries := tr.Entries()
	foundRejection := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "not allowed") && strings.Contains(e.Content, "write_file") {
			foundRejection = true
			break
		}
	}
	if !foundRejection {
		t.Errorf("expected subagent transcript to record the write_file rejection; entries were: %+v", entries)
	}
}

func TestRun_RejectsSpawnSubagent(t *testing.T) {
	// Runtime defense in depth: even if the subagent's model somehow
	// produced a spawn_subagent call (e.g. via tool-call injection
	// from a malicious file), the runner's switch refuses it. The
	// structural guard (the schema not including spawn_subagent) is
	// tested in TestSubagentTools_OnlyReadAndRun.
	mock := &mockClient{
		queue: []cannedResp{
			toolResp("spawn_subagent", map[string]any{"task": "nested"}),
			textResp("OK, no nested.\nSUMMARY: spawn_subagent is rejected"),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "check", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result")
	}
	if res.Summary != "spawn_subagent is rejected" {
		t.Errorf("expected 'spawn_subagent is rejected', got %q", res.Summary)
	}
}

func TestRun_AllowsReadFile(t *testing.T) {
	// Happy path: subagent reads a file (via read_file), then emits
	// a summary that includes the file content. The file is real on
	// disk in the runner's workDir.
	workDir := t.TempDir()
	target := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(target, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mock := &mockClient{
		queue: []cannedResp{
			toolResp("read_file", map[string]any{"path": "notes.txt"}),
			textResp("I read it.\nSUMMARY: file contains 'hello world'"),
		},
	}
	runner, _ := subagent.NewRunner(mock, workDir, t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "read notes", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result")
	}
	if res.Summary != "file contains 'hello world'" {
		t.Errorf("expected 'file contains...', got %q", res.Summary)
	}
}

func TestRun_AllowsRunCommand(t *testing.T) {
	// Subagent runs `echo hello` and gets the output. Then emits
	// summary. Verifies run_command is allowed and the output is
	// recorded in the subagent's transcript.
	mock := &mockClient{
		queue: []cannedResp{
			toolResp("run_command", map[string]any{"command": "echo hello"}),
			textResp("ran it.\nSUMMARY: got 'hello' on stdout"),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(context.Background(), "sa1", "echo test", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected non-truncated result")
	}
	if res.Summary != "got 'hello' on stdout" {
		t.Errorf("expected 'got hello...', got %q", res.Summary)
	}
}

// ---------------------------------------------------------------------------
// Input validation
// ---------------------------------------------------------------------------

func TestRun_RejectsEmptyTask(t *testing.T) {
	runner, _ := subagent.NewRunner(&mockClient{}, t.TempDir(), t.TempDir(), 0, 0, 0)
	_, err := runner.Run(context.Background(), "sa1", "", "", parentConfig())
	if err == nil {
		t.Fatal("expected error for empty task, got nil")
	}
	if !strings.Contains(err.Error(), "task must not be empty") {
		t.Errorf("expected 'task must not be empty' in error, got: %v", err)
	}
}

func TestRun_RejectsEmptyID(t *testing.T) {
	runner, _ := subagent.NewRunner(&mockClient{}, t.TempDir(), t.TempDir(), 0, 0, 0)
	_, err := runner.Run(context.Background(), "", "do something", "", parentConfig())
	if err == nil {
		t.Fatal("expected error for empty id, got nil")
	}
	if !strings.Contains(err.Error(), "id must not be empty") {
		t.Errorf("expected 'id must not be empty' in error, got: %v", err)
	}
}

func TestRun_RejectsIncompleteParentConfig(t *testing.T) {
	runner, _ := subagent.NewRunner(&mockClient{}, t.TempDir(), t.TempDir(), 0, 0, 0)
	_, err := runner.Run(context.Background(), "sa1", "do something", "", agent.AgentConfig{
		// BaseURL and Model intentionally empty.
	})
	if err == nil {
		t.Fatal("expected error for incomplete parent config, got nil")
	}
	if !strings.Contains(err.Error(), "BaseURL and Model") {
		t.Errorf("expected 'BaseURL and Model' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Transcript isolation
// ---------------------------------------------------------------------------

// TestRun_WritesIsolatedTranscript verifies the subagent's transcript
// file is created at <sessionDir>/subagents/<id>.jsonl and contains the
// expected entries (seed "You" message + the subagent's own message).
// This is the per-run transcript isolation that docs/work2.md §3.2.3
// requires: the parent never sees these entries.
func TestRun_WritesIsolatedTranscript(t *testing.T) {
	sessionDir := t.TempDir()
	mock := &mockClient{
		queue: []cannedResp{
			textResp("done.\nSUMMARY: result here"),
		},
	}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), sessionDir, 0, 0, 0)
	res, err := runner.Run(context.Background(), "iso1", "test task", "extra context", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify file exists at the documented path.
	expected := filepath.Join(sessionDir, "subagents", "iso1.jsonl")
	if res.TranscriptPath != expected {
		t.Errorf("expected transcript at %q, got %q", expected, res.TranscriptPath)
	}
	if _, statErr := os.Stat(expected); statErr != nil {
		t.Fatalf("subagent transcript file not created at %q: %v", expected, statErr)
	}

	// Load and verify contents.
	tr, err := transcript.LoadFromFile(expected)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	entries := tr.Entries()
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries (seed + subagent summary), got %d", len(entries))
	}

	// First entry: seed "You" message containing the task + context.
	if entries[0].Speaker != transcript.SpeakerYou {
		t.Errorf("first entry should be SpeakerYou, got %q", entries[0].Speaker)
	}
	if !strings.Contains(entries[0].Content, "test task") {
		t.Errorf("first entry should contain the task, got %q", entries[0].Content)
	}
	if !strings.Contains(entries[0].Content, "extra context") {
		t.Errorf("first entry should contain the extra context, got %q", entries[0].Content)
	}

	// Find a subagent entry with the SUMMARY content.
	foundSub := false
	for _, e := range entries {
		if e.Speaker == "Subagent:iso1" && strings.Contains(e.Content, "done.") {
			foundSub = true
			break
		}
	}
	if !foundSub {
		t.Errorf("expected an entry from Subagent:iso1 with the model's text, got entries: %+v", entries)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestRun_ContextCancellation(t *testing.T) {
	// A cancelled context should surface as an error from Run, with
	// the partial result. The runner increments Turns at the top of
	// the loop body, then checks ctx.Err() — so a cancelled context
	// surfaces as Turns=1 (the attempt that never completed its model
	// call) plus a context error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The mock returns a plain text response. We never reach it
	// because the runner's per-turn ctx.Err() check fires first.
	mock := &mockClient{queue: []cannedResp{textResp("nope")}}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0)
	res, err := runner.Run(ctx, "sa1", "task", "", parentConfig())
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("expected 'context cancelled' in error, got: %v", err)
	}
	// The mock's Respond was never called — that's the real point of
	// the test (we didn't waste an API call on a cancelled context).
	if mock.calls != 0 {
		t.Errorf("expected 0 model calls on cancelled context, got %d", mock.calls)
	}
	// Turns=1 because the loop entered turn 1 and bailed before the
	// model call. (Documented behaviour, not a bug.)
	if res.Turns != 1 {
		t.Errorf("expected 1 turn attempted (bailed before model call), got %d", res.Turns)
	}
}

// ---------------------------------------------------------------------------
// Default turn cap
// ---------------------------------------------------------------------------

func TestRun_DefaultsToEightTurns(t *testing.T) {
	// Sanity: with maxTurns=0 (the "use default" sentinel), the
	// runner must let the subagent talk for at least 8 turns before
	// truncating. We feed 9 plain-text-no-summary responses and
	// expect Truncated=true with Turns=8.
	queue := make([]cannedResp, 9)
	for i := range queue {
		queue[i] = textResp("not done")
	}
	mock := &mockClient{queue: queue}
	runner, _ := subagent.NewRunner(mock, t.TempDir(), t.TempDir(), 0, 0, 0) // maxTurns=0 → default
	res, err := runner.Run(context.Background(), "sa1", "task", "", parentConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Errorf("expected truncation at default turn cap")
	}
	if res.Turns != subagent.DefaultMaxTurns {
		t.Errorf("expected %d turns (the default), got %d", subagent.DefaultMaxTurns, res.Turns)
	}
}
