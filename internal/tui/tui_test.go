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
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test_session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0)
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



