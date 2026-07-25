package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/gitcommit"
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
		"summary.md": `---
target: system
description: Render a local git-based report of changes made in the current session
---

(show session summary)
`,
		"undo.md": `---
target: system
description: Revert last [triad] auto-commit
---

Revert the most recent [triad] auto-commit (docs/work2.md §2.3).
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

func TestTUI_SlashCommand_SummaryZeroCommits(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// /summary on zero commits session
	updated, cmd := model.Update(humanInputMsg{content: "/summary"})
	m := updated.(Model)

	if cmd != nil {
		t.Errorf("/summary should not trigger a Coder turn, got cmd=%v", cmd)
	}

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Speaker != transcript.SpeakerSystem {
		t.Errorf("/summary should produce a System entry, got %q", entries[0].Speaker)
	}
	if !strings.Contains(entries[0].Content, "Nothing committed yet this session") {
		t.Errorf("expected 'Nothing committed yet this session', got: %q", entries[0].Content)
	}
}

func TestTUI_SlashCommand_SummaryWithCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = model.workDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=NUL", "GIT_CONFIG_SYSTEM=NUL")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "--local", "user.email", "test@example.com")
	run("config", "--local", "user.name", "Triad Test")
	run("commit", "--allow-empty", "-q", "-m", "initial")

	// 1. Task message
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Build authentication module",
	})

	// 2. Add an action result and auto-commit
	actionRes := transcript.Entry{
		Speaker: transcript.SpeakerSystem,
		Type:    transcript.TypeActionResult,
		Content: "File write_file written",
	}
	_ = model.transcript.Append(actionRes)
	trEntries := model.transcript.Entries()
	actionResID := trEntries[len(trEntries)-1].ID

	filePath := filepath.Join(model.workDir, "auth.go")
	if err := os.WriteFile(filePath, []byte("package auth\nfunc Login() {}\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	msg := gitcommit.CommitMessage{
		EntryID:     actionResID,
		Intent:      "add auth.go",
		ToolName:    "write_file",
		SessionPath: model.transcript.FilePath(),
	}
	res, err := gitcommit.CommitAction(model.workDir, []string{"auth.go"}, msg)
	if err != nil || res.Hash == "" {
		t.Fatalf("CommitAction failed: %v", err)
	}

	// 3. Issue /summary
	updated, cmd := model.Update(humanInputMsg{content: "/summary"})
	m := updated.(Model)

	if cmd != nil {
		t.Errorf("/summary should not trigger a Coder turn, got cmd=%v", cmd)
	}

	entries := m.transcript.Entries()
	last := entries[len(entries)-1]
	if last.Speaker != transcript.SpeakerSystem {
		t.Errorf("expected System entry, got %q", last.Speaker)
	}
	if !strings.Contains(last.Content, "Session Summary") {
		t.Errorf("expected 'Session Summary' header, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "Build authentication module") {
		t.Errorf("expected task description, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "Commits made: 1") {
		t.Errorf("expected 'Commits made: 1', got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "auth.go") {
		t.Errorf("expected 'auth.go' in files touched, got: %q", last.Content)
	}
}

func TestTUI_SlashCommand_SummaryMixedRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = model.workDir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=NUL", "GIT_CONFIG_SYSTEM=NUL")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "--local", "user.email", "test@example.com")
	run("config", "--local", "user.name", "Triad Test")
	run("commit", "--allow-empty", "-q", "-m", "initial")

	// Manual user commit
	manualFile := filepath.Join(model.workDir, "user_manual.txt")
	_ = os.WriteFile(manualFile, []byte("manual content\n"), 0644)
	run("add", "user_manual.txt")
	run("commit", "-m", "user manual commit")

	// Triad commit
	actionRes := transcript.Entry{
		Speaker: transcript.SpeakerSystem,
		Type:    transcript.TypeActionResult,
		Content: "File write_file written",
	}
	_ = model.transcript.Append(actionRes)
	trEntries := model.transcript.Entries()
	actionResID := trEntries[len(trEntries)-1].ID

	triadFile := filepath.Join(model.workDir, "triad_file.go")
	_ = os.WriteFile(triadFile, []byte("package main\n"), 0644)
	msg := gitcommit.CommitMessage{
		EntryID:  actionResID,
		Intent:   "add triad_file.go",
		ToolName: "write_file",
	}
	_, err := gitcommit.CommitAction(model.workDir, []string{"triad_file.go"}, msg)
	if err != nil {
		t.Fatalf("CommitAction failed: %v", err)
	}

	// Issue /summary
	updated, _ := model.Update(humanInputMsg{content: "/summary"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	last := entries[len(entries)-1]
	if !strings.Contains(last.Content, "Commits made: 1") {
		t.Errorf("expected 1 Triad commit counted in mixed repo, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "triad_file.go") {
		t.Errorf("expected triad_file.go in report, got: %q", last.Content)
	}
	if strings.Contains(last.Content, "user_manual.txt") {
		t.Errorf("user_manual.txt should NOT be in Triad summary report, got: %q", last.Content)
	}
}

func TestTUI_SlashCommand_Help(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	updated, cmd := model.Update(humanInputMsg{content: "/help"})
	m := updated.(Model)

	if cmd != nil {
		t.Errorf("/help should not trigger a Coder turn, got cmd=%v", cmd)
	}

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry for /help, got %d", len(entries))
	}
	last := entries[0]
	if last.Speaker != transcript.SpeakerSystem {
		t.Errorf("expected System entry, got %q", last.Speaker)
	}
	if !strings.Contains(last.Content, "Available Slash Commands:") {
		t.Errorf("expected 'Available Slash Commands:' header, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "/diff") || !strings.Contains(last.Content, "/plan") || !strings.Contains(last.Content, "/status") {
		t.Errorf("expected command listing in help output, got: %q", last.Content)
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

// ---------------------------------------------------------------------------
// /undo tests (docs/work2.md §2.3)
// ---------------------------------------------------------------------------

// makeRepoModelForUndo sets up a Model whose workDir is a real git repo
// with one prior [triad] commit on top of an initial empty commit, so
// /undo has something concrete to revert.
func makeRepoModelForUndo(t *testing.T) (Model, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}

	dir := t.TempDir()
	devNull := "NUL"
	if _, err := os.Stat(devNull); err != nil {
		devNull = "/dev/null"
	}
	t.Setenv("GIT_CONFIG_GLOBAL", devNull)
	t.Setenv("GIT_CONFIG_SYSTEM", devNull)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "--local", "user.email", "test@example.com")
	run("config", "--local", "user.name", "Triad Test")
	run("commit", "--allow-empty", "-q", "-m", "initial")

	// Create a file and commit it (this is the [triad] commit that /undo will revert).
	if err := os.WriteFile(filepath.Join(dir, "doomed.txt"), []byte("bad content"), 0o644); err != nil {
		t.Fatalf("write doomed.txt: %v", err)
	}
	run("add", "--", "doomed.txt")
	run("commit", "-q", "-m", "[triad] entry #1: create doomed.txt")

	// Build a model in this dir.
	client := &mockClient{}
	sessionPath := filepath.Join(dir, "test_session.jsonl")
	tr := transcript.NewTranscript(sessionPath)
	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	reg := loadTestRegistry(t)
	model := NewModel(tr, coder, reviewer, client, dir, 0, reg)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	return model, dir
}

func TestTUI_Undo_NothingToUndo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}
	dir := t.TempDir()
	client := &mockClient{}
	tr := transcript.NewTranscript(filepath.Join(dir, "test_session.jsonl"))
	model := NewModel(tr, agent.AgentConfig{Name: "Coder", HasTools: true},
		agent.AgentConfig{Name: "Reviewer", HasTools: false},
		client, dir, 0, loadTestRegistry(t))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)

	// /undo with no [triad] commit in history should produce a friendly
	// "nothing to undo" System entry and not panic.
	updated, _ = model.Update(humanInputMsg{content: "/undo"})
	m := updated.(Model)
	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 System entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Content, "Nothing to undo") {
		t.Errorf("expected 'Nothing to undo' in entry, got: %q", entries[0].Content)
	}
}

func TestTUI_Undo_RevertsLastCommit(t *testing.T) {
	model, dir := makeRepoModelForUndo(t)

	// Confirm doomed.txt exists before /undo.
	if _, err := os.Stat(filepath.Join(dir, "doomed.txt")); err != nil {
		t.Fatalf("pre-condition: doomed.txt should exist: %v", err)
	}

	// Invoke /undo.
	updated, cmd := model.Update(humanInputMsg{content: "/undo"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("/undo should not trigger a Coder turn, got cmd=%v", cmd)
	}

	// doomed.txt should be gone after the revert.
	if _, err := os.Stat(filepath.Join(dir, "doomed.txt")); !os.IsNotExist(err) {
		t.Errorf("expected doomed.txt to be removed by revert, stat err: %v", err)
	}

	// Transcript should contain a System entry recording the revert.
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		t.Fatal("expected at least one System entry from /undo")
	}
	last := entries[len(entries)-1]
	if last.Speaker != transcript.SpeakerSystem {
		t.Errorf("expected last entry to be System, got %q", last.Speaker)
	}
	if !strings.Contains(last.Content, "/undo") || !strings.Contains(strings.ToLower(last.Content), "revert") {
		t.Errorf("expected /undo revert summary, got: %q", last.Content)
	}
	// And the original [triad] commit should still be in history —
	// we used `git revert` (preserves history), not `git reset` (destroys it).
	logCmd := exec.Command("git", "log", "--pretty=%s", "-n", "10") //nolint:gosec
	logCmd.Dir = dir
	logOut, _ := logCmd.Output()
	if !strings.Contains(string(logOut), "[triad]") {
		t.Errorf("original [triad] commit should still be in history:\n%s", logOut)
	}
}

func setupAutocompleteTestModel(t *testing.T) (Model, func()) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "commands")
	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		t.Fatalf("setup cmdDir: %v", err)
	}

	cmdFiles := map[string]string{
		"diff.md":    "---\ntarget: reviewer\ndescription: Re-examine proposed action diff\n---\nRe-examine diff\n",
		"help.md":    "---\ntarget: system\ndescription: Show available slash commands\n---\nShow help\n",
		"plan.md":    "---\ntarget: coder\ndescription: Ask Coder to produce a plan only\n---\nPropose a step-by-step plan for: {{args}}\n",
		"status.md":  "---\ntarget: system\ndescription: Check current session status\n---\nShow status\n",
		"strict.md":  "---\ntarget: reviewer\ndescription: Enforce strict safety gates\n---\nStrict instructions: {{args}}\n",
		"summary.md": "---\ntarget: system\ndescription: Render session summary\n---\nShow summary\n",
		"undo.md":    "---\ntarget: system\ndescription: Undo last action\n---\nUndo action\n",
	}
	for name, content := range cmdFiles {
		if err := os.WriteFile(filepath.Join(cmdDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write cmd %s: %v", name, err)
		}
	}

	reg, err := commands.Load(cmdDir)
	if err != nil {
		t.Fatalf("Load commands: %v", err)
	}

	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, reg)
	return model, cleanup
}

func TestTUI_Autocomplete_Filtering(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// 1. Typing '/' opens popup with all 7 commands sorted
	m := typeString(model, "/")
	if !m.autocompleteActive {
		t.Fatalf("expected autocompleteActive to be true on '/'")
	}
	if len(m.autocompleteCmds) != 7 {
		t.Fatalf("expected 7 commands on '/', got %d", len(m.autocompleteCmds))
	}
	if m.autocompleteCmds[0].Name != "diff" || m.autocompleteCmds[6].Name != "undo" {
		t.Errorf("unexpected command order: %+v", m.autocompleteCmds)
	}

	// 2. Typing 'p' narrows to [plan]
	m = typeString(m, "p")
	if !m.autocompleteActive || len(m.autocompleteCmds) != 1 || m.autocompleteCmds[0].Name != "plan" {
		t.Fatalf("expected 1 match ('plan') on '/p', got %v", m.autocompleteCmds)
	}


	// 3. Clear and type '/s' narrows to [status, strict, summary]
	m.input.SetValue("")
	m = typeString(m, "/s")
	if !m.autocompleteActive || len(m.autocompleteCmds) != 3 {
		t.Fatalf("expected 3 matches ('status', 'strict', 'summary') on '/s', got %d", len(m.autocompleteCmds))
	}
	if m.autocompleteCmds[0].Name != "status" || m.autocompleteCmds[1].Name != "strict" || m.autocompleteCmds[2].Name != "summary" {
		t.Errorf("unexpected commands on '/s': %+v", m.autocompleteCmds)
	}

	// 4. Type 'tr' -> '/str' narrows to [strict]
	m = typeString(m, "tr")
	if !m.autocompleteActive || len(m.autocompleteCmds) != 1 || m.autocompleteCmds[0].Name != "strict" {
		t.Fatalf("expected 1 match ('strict') on '/str', got %v", m.autocompleteCmds)
	}
}

func TestTUI_Autocomplete_Navigation(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// Type '/s' -> matches [status, strict, summary], initial index 0
	m := typeString(model, "/s")
	if m.autocompleteIndex != 0 {
		t.Fatalf("expected initial autocompleteIndex to be 0, got %d", m.autocompleteIndex)
	}

	// Press Down arrow -> index becomes 1 (strict)
	up, _ := m.Update(makeKeyMsg("down"))
	m = up.(Model)
	if m.autocompleteIndex != 1 {
		t.Fatalf("expected autocompleteIndex 1 after Down, got %d", m.autocompleteIndex)
	}

	// Press Down arrow -> index becomes 2 (summary)
	up, _ = m.Update(makeKeyMsg("down"))
	m = up.(Model)
	if m.autocompleteIndex != 2 {
		t.Fatalf("expected autocompleteIndex 2 after Down, got %d", m.autocompleteIndex)
	}

	// Press Down arrow again -> index wraps to 0 (status)
	up, _ = m.Update(makeKeyMsg("down"))
	m = up.(Model)
	if m.autocompleteIndex != 0 {
		t.Fatalf("expected autocompleteIndex 0 after wrapping Down, got %d", m.autocompleteIndex)
	}

	// Press Up arrow -> index wraps to 2 (summary)
	up, _ = m.Update(makeKeyMsg("up"))
	m = up.(Model)
	if m.autocompleteIndex != 2 {
		t.Fatalf("expected autocompleteIndex 2 after Up, got %d", m.autocompleteIndex)
	}
}

func TestTUI_Autocomplete_AcceptanceAndExpansion(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// 1. Test Tab acceptance
	m := typeString(model, "/pl")
	if !m.autocompleteActive {
		t.Fatalf("expected autocompleteActive true on '/pl'")
	}
	up, _ := m.Update(makeKeyMsg("tab"))
	m = up.(Model)
	if m.autocompleteActive {
		t.Errorf("expected autocompleteActive false after Tab")
	}
	if m.input.Value() != "/plan " {
		t.Errorf("expected input '/plan ', got %q", m.input.Value())
	}

	// Submit input via Enter
	up, cmd := m.Update(makeKeyMsg("enter"))
	m = up.(Model)
	if cmd == nil {
		t.Fatalf("expected non-nil Cmd after submitting '/plan '")
	}
	// Process humanInputMsg generated by Enter
	msg := cmd()
	up, _ = m.Update(msg)
	m = up.(Model)

	entries := m.transcript.Entries()
	if len(entries) == 0 {
		t.Fatalf("expected transcript entry after submitting expanded slash command")
	}
	last := entries[len(entries)-1]
	if last.Speaker != transcript.SpeakerYou || !strings.Contains(last.Content, "Propose a step-by-step plan for:") {
		t.Errorf("expected expanded plan template in transcript, got: %+v", last)
	}

	// 2. Test Enter acceptance on dropdown selection
	m.input.SetValue("")
	m = typeString(m, "/st") // matches status, strict
	// Down arrow to select strict (index 1)
	up, _ = m.Update(makeKeyMsg("down"))
	m = up.(Model)
	// Press Enter to accept suggestion
	up, _ = m.Update(makeKeyMsg("enter"))
	m = up.(Model)
	if m.autocompleteActive {
		t.Errorf("expected autocompleteActive false after Enter suggestion acceptance")
	}
	if m.input.Value() != "/strict " {
		t.Errorf("expected input '/strict ', got %q", m.input.Value())
	}
}

func TestTUI_Autocomplete_DismissalOnEscAndNonMatching(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// 1. Dismissal on Escape
	m := typeString(model, "/p")
	if !m.autocompleteActive {
		t.Fatalf("expected autocompleteActive true on '/p'")
	}
	up, cmd := m.Update(makeKeyMsg("esc"))
	m = up.(Model)
	if cmd != nil {
		t.Errorf("Esc when autocomplete is active should NOT produce Quit Cmd, got: %v", cmd)
	}
	if m.autocompleteActive {
		t.Errorf("expected autocompleteActive false after Esc")
	}

	// 2. Dismissal on non-matching input
	m.input.SetValue("")
	m = typeString(m, "/xyz")
	if m.autocompleteActive {
		t.Errorf("expected autocompleteActive false on non-matching input '/xyz'")
	}

	// 3. Dismissal when input no longer starts with '/'
	m.input.SetValue("")
	m = typeString(m, "hello")
	if m.autocompleteActive {
		t.Errorf("expected autocompleteActive false on non-slash input 'hello'")
	}
}

func makeKeyMsg(str string) tea.KeyPressMsg {
	switch str {
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	default:
		if len(str) == 1 {
			r := rune(str[0])
			return tea.KeyPressMsg(tea.Key{Code: r, Text: str})
		}
		return tea.KeyPressMsg(tea.Key{Text: str})
	}
}

// helper to simulate typing characters one by one into the model
func typeString(m Model, s string) Model {
	for _, r := range s {
		str := string(r)
		updated, _ := m.Update(makeKeyMsg(str))
		m = updated.(Model)
	}
	return m
}

func TestTUI_ViewHeightOverflow(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	heights := []int{20, 24, 30, 40, 50}
	widths := []int{70, 80, 100, 120, 150}

	for _, h := range heights {
		for _, w := range widths {
			m, _ := model.Update(tea.WindowSizeMsg{Width: w, Height: h})
			mod := m.(Model)
			v := mod.View()
			lines := strings.Split(v.Content, "\n")
			if len(lines) != h {
				t.Errorf("w=%d h=%d: expected %d lines, got %d", w, h, h, len(lines))
				continue
			}

			// Last line must contain bottom border character '╰' or '─' or '╯'
			lastLine := lines[len(lines)-1]
			if !strings.Contains(lastLine, "╰") && !strings.Contains(lastLine, "─") && !strings.Contains(lastLine, "╯") {
				t.Errorf("w=%d h=%d: bottom line missing bottom border, got: %q", w, h, lastLine)
			}

			// Check that the prompt input 'YOU' is present and not cut off
			foundPrompt := false
			for _, line := range lines {
				if strings.Contains(line, "YOU") {
					foundPrompt = true
					break
				}
			}
			if !foundPrompt {
				t.Errorf("w=%d h=%d: prompt bar 'YOU' missing from view", w, h)
			}
		}
	}
}





