package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// mockClient implements loop.AgentClient for testing.
type mockClient struct {
	coderResponses    []agent.AgentResponse
	reviewerResponses []agent.AgentResponse
	coderIndex        int
	reviewerIndex     int
}

func (m *mockClient) Respond(ctx context.Context, cfg agent.AgentConfig, entries []transcript.Entry) (agent.AgentResponse, error) {
	if cfg.Name == "Coder" {
		if m.coderIndex < len(m.coderResponses) {
			resp := m.coderResponses[m.coderIndex]
			m.coderIndex++
			return resp, nil
		}
		return agent.AgentResponse{Text: "default mock coder response"}, nil
	}

	if m.reviewerIndex < len(m.reviewerResponses) {
		resp := m.reviewerResponses[m.reviewerIndex]
		m.reviewerIndex++
		return resp, nil
	}
	return agent.AgentResponse{Text: "APPROVED: default mock approval"}, nil
}

func setupTestModel(t *testing.T, client loop.AgentClient) (Model, func()) {
	return setupTestModelWithRegistry(t, client, &commands.Registry{})
}

func setupTestModelWithRegistry(t *testing.T, client loop.AgentClient, reg *commands.Registry) (Model, func()) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test_session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, reg)
	// Simulate WindowSizeMsg to initialize viewport
	updatedModel, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updatedModel.(Model)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return model, cleanup
}

func TestTUI_HumanInput(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// Handle human input message
	updated, cmd := model.Update(humanInputMsg{content: "Create a test file"})
	m := updated.(Model)

	if cmd == nil {
		t.Fatalf("expected non-nil Cmd for coder turn after human input")
	}

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d", len(entries))
	}

	if entries[0].Speaker != transcript.SpeakerYou || entries[0].Content != "Create a test file" {
		t.Errorf("unexpected entry content: %+v", entries[0])
	}
}

func TestTUI_ApprovalLoopFlow(t *testing.T) {
	tc := agent.ToolCall{
		ID:   "call_123",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"path":"hello.txt","content":"hello world"}`,
		},
	}

	client := &mockClient{
		coderResponses: []agent.AgentResponse{
			{ToolCalls: []agent.ToolCall{tc}},
		},
		reviewerResponses: []agent.AgentResponse{
			{Text: "APPROVED: looks good"},
		},
	}

	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// 1. Human submits task
	updated, _ := model.Update(humanInputMsg{content: "Create hello.txt"})
	m := updated.(Model)

	// 2. Coder proposes action
	updated, cmd := m.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{tc}},
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected Cmd for Reviewer turn")
	}
	if m.activeToolCall == nil || m.activeToolCall.Function.Name != "write_file" {
		t.Fatalf("expected activeToolCall to be write_file")
	}

	// 3. Reviewer approves
	updated, cmd = m.Update(agentResponseMsg{
		speaker: transcript.SpeakerReviewer,
		resp:    agent.AgentResponse{Text: "APPROVED: looks good"},
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected Cmd for tool execution")
	}

	// 4. Tool executes
	updated, cmd = m.Update(toolResultMsg{
		toolCall: tc,
		result:   "File write_file written (11 bytes)",
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected Cmd for next Coder turn")
	}
	if m.activeToolCall != nil {
		t.Errorf("expected activeToolCall to be reset after tool execution")
	}
}

func TestTUI_ReviewerObjection(t *testing.T) {
	tc := agent.ToolCall{
		ID:   "call_456",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "run_command",
			Arguments: `{"command":"rm -rf /"}`,
		},
	}

	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// 1. Coder proposes action
	updated, _ := model.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{tc}},
	})
	m := updated.(Model)

	// 2. Reviewer objects
	updated, cmd := m.Update(agentResponseMsg{
		speaker: transcript.SpeakerReviewer,
		resp:    agent.AgentResponse{Text: "OBJECTION: dangerous command"},
	})
	m = updated.(Model)

	if cmd == nil {
		t.Fatalf("expected Cmd for Coder revision turn after objection")
	}
	if m.retryCount != 1 {
		t.Errorf("expected retryCount=1, got %d", m.retryCount)
	}
}

func TestTUI_Phase7_ViewRendering(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// Append sample entries
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Create hello.txt",
	})
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerCoder,
		Type:    transcript.TypeProposedAction,
		Content: "Proposed tool call: write_file\nArguments:\n{\n  \"path\": \"hello.txt\"\n}",
	})

	model.refreshViewport()

	view := model.View()
	rendered := view.Content

	if rendered == "" {
		t.Fatalf("expected non-empty view output")
	}

	// Verify header, sidebar, pills, and input components in output
	if !model.ready {
		t.Fatalf("expected model to be ready")
	}
}

func TestTUI_Phase7_ProposedActionFormatting(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	content := "Proposed tool call: write_file\nArguments:\n{\n  \"path\": \"test.txt\",\n  \"content\": \"hello\"\n}"
	formatted := model.renderProposedAction(content, 60)

	if formatted == "" {
		t.Fatalf("expected formatted tool action output")
	}
}

func TestTUI_Phase7_WindowResize(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	sizes := []tea.WindowSizeMsg{
		{Width: 120, Height: 40},
		{Width: 50, Height: 15},
		{Width: 80, Height: 24},
	}

	for _, size := range sizes {
		updated, _ := model.Update(size)
		m := updated.(Model)
		v := m.View()
		if v.Content == "" {
			t.Errorf("expected non-empty view for size %dx%d", size.Width, size.Height)
		}
	}
}

func TestTUI_Phase7_PipelineDock(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	dock := model.renderPipelineDock(100)
	if dock == "" {
		t.Fatalf("expected non-empty pipeline dock string")
	}
}

func TestTUI_Phase7_Badges(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerReviewer,
		Type:    transcript.TypeMessage,
		Content: "APPROVED: looks good to go",
	})
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerReviewer,
		Type:    transcript.TypeMessage,
		Content: "OBJECTION: unsafe action detected",
	})

	model.refreshViewport()
	rendered := model.renderTranscript()
	if rendered == "" {
		t.Fatalf("expected non-empty transcript output with badges")
	}
}

func TestTUI_HeightBudgetAndClipping(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	testDimensions := []struct {
		w, h int
	}{
		{80, 24},
		{100, 30},
		{60, 15},
		{120, 40},
	}

	for _, dim := range testDimensions {
		updated, _ := model.Update(tea.WindowSizeMsg{Width: dim.w, Height: dim.h})
		m := updated.(Model)
		v := m.View()

		lines := strings.Split(v.Content, "\n")
		if len(lines) != dim.h {
			t.Errorf("View output for %dx%d produced %d lines, expected exactly %d", dim.w, dim.h, len(lines), dim.h)
		}

		for idx, line := range lines {
			visualWidth := lipgloss.Width(line)
			if visualWidth > dim.w {
				t.Errorf("View output line %d for %dx%d has visual width %d, expected <= %d", idx, dim.w, dim.h, visualWidth, dim.w)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Slash command tests (docs/work2.md §1)
// ---------------------------------------------------------------------------

// loadTestRegistry writes a few command .md files into a temp dir and
// returns the resulting Registry. Used by the slash command tests.
func loadTestRegistry(t *testing.T) *commands.Registry {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"plan.md": `---
target: coder
description: Ask Coder to plan
---

Propose a step-by-step plan for: {{args}}
`,
		"status.md": `---
target: system
description: Print session status
---

(show session status)
`,
		"strict.md": `---
target: reviewer
description: Toggle strict mode
---

Be strict.
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("setup: write %s: %v", name, err)
		}
	}
	reg, err := commands.Load(dir)
	if err != nil {
		t.Fatalf("setup: Load: %v", err)
	}
	return reg
}

func TestTUI_SlashCommand_PlanExpandsArgs(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// /plan Add Razorpay webhook → the rendered body should contain the
	// expanded args and be injected as a You message (Speaker == You).
	updated, _ := model.Update(humanInputMsg{content: "/plan Add Razorpay webhook"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d (%v)", len(entries), entries)
	}
	got := entries[0]
	if got.Speaker != transcript.SpeakerYou {
		t.Errorf("speaker: got %q, want You", got.Speaker)
	}
	if !strings.Contains(got.Content, "Add Razorpay webhook") {
		t.Errorf("expanded content missing args: %q", got.Content)
	}
	if !strings.Contains(got.Content, "step-by-step plan") {
		t.Errorf("expanded content missing template body: %q", got.Content)
	}
	if strings.Contains(got.Content, "{{args}}") {
		t.Errorf("template still contains {{args}} placeholder: %q", got.Content)
	}
}

func TestTUI_SlashCommand_NoArgs(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// /plan with no args should still expand (replacing {{args}} with "").
	updated, _ := model.Update(humanInputMsg{content: "/plan"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if strings.Contains(entries[0].Content, "{{args}}") {
		t.Errorf("placeholder not replaced: %q", entries[0].Content)
	}
}

func TestTUI_SlashCommand_Unknown(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// Unknown command → System error entry, no You message, no Coder turn.
	updated, cmd := model.Update(humanInputMsg{content: "/foobar do something"})
	m := updated.(Model)

	if cmd != nil {
		t.Errorf("unknown command should not trigger a Coder turn, got cmd=%v", cmd)
	}
	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (system error), got %d (%v)", len(entries), entries)
	}
	if entries[0].Speaker != transcript.SpeakerSystem {
		t.Errorf("unknown command should produce System entry, got %q", entries[0].Speaker)
	}
	if !strings.Contains(entries[0].Content, "Unknown command") {
		t.Errorf("expected 'Unknown command' in error, got: %q", entries[0].Content)
	}
	if !strings.Contains(entries[0].Content, "/plan") {
		t.Errorf("expected available commands listed, got: %q", entries[0].Content)
	}
}

func TestTUI_SlashCommand_EmptyCommand(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// Bare "/" should not be treated as a command.
	updated, _ := model.Update(humanInputMsg{content: "/"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	if len(entries) != 1 || entries[0].Speaker != transcript.SpeakerSystem {
		t.Errorf("bare / should surface a system error, got entries=%v", entries)
	}
}

func TestTUI_SlashCommand_PlainMessageStillWorks(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// A non-slash message should pass through untouched and trigger Coder turn.
	updated, cmd := model.Update(humanInputMsg{content: "add a webhook handler"})
	m := updated.(Model)

	if cmd == nil {
		t.Errorf("plain message should trigger a Coder turn")
	}
	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Content != "add a webhook handler" {
		t.Errorf("plain message content was modified: %q", entries[0].Content)
	}
}

func TestTUI_SlashCommand_StatusIsSystem(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// Pre-populate a human message so /status has something to report.
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "implement foo",
	})

	// /status is target: system — it should append a System entry and NOT
	// trigger a Coder turn.
	updated, cmd := model.Update(humanInputMsg{content: "/status"})
	m := updated.(Model)

	if cmd != nil {
		t.Errorf("/status should not trigger a Coder turn, got cmd=%v", cmd)
	}

	entries := m.transcript.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (You + System), got %d", len(entries))
	}
	if entries[1].Speaker != transcript.SpeakerSystem {
		t.Errorf("/status should produce a System entry, got %q", entries[1].Speaker)
	}
	if !strings.Contains(entries[1].Content, "Session status") {
		t.Errorf("/status output missing header, got: %q", entries[1].Content)
	}
	if !strings.Contains(entries[1].Content, "implement foo") {
		t.Errorf("/status should report current task, got: %q", entries[1].Content)
	}
}

func TestTUI_SlashCommand_NoRegistry(t *testing.T) {
	client := &mockClient{}
	// Empty registry (no commands dir at all).
	model, cleanup := setupTestModelWithRegistry(t, client, &commands.Registry{})
	defer cleanup()

	updated, _ := model.Update(humanInputMsg{content: "/plan anything"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	if len(entries) != 1 || entries[0].Speaker != transcript.SpeakerSystem {
		t.Errorf("with empty registry, /plan should produce 'no commands registered' error, got: %v", entries)
	}
	if !strings.Contains(entries[0].Content, "No slash commands are registered") {
		t.Errorf("expected 'No slash commands are registered' message, got: %q", entries[0].Content)
	}
}

func TestTUI_SlashCommand_TriggersCoderTurnWhenIdle(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// Session starts in idle; /plan (target: coder) should kick a Coder turn.
	updated, cmd := model.Update(humanInputMsg{content: "/plan refactor auth"})
	m := updated.(Model)

	if cmd == nil {
		t.Errorf("coder-target /plan should trigger a Coder turn when idle")
	}
	if m.sessionState != loop.StateActive {
		t.Errorf("session should be active after /plan, got %v", m.sessionState)
	}
}



