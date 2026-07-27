package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/skills"
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

// ListModels implements loop.AgentClient for the test mock. Returns
// a small canned list so /models tests have something to render.
func (m *mockClient) ListModels(ctx context.Context, cfg agent.AgentConfig) ([]agent.ModelInfo, error) {
	return []agent.ModelInfo{
		{ID: "mimo-v2.5-free", OwnedBy: "opencode_zen"},
		{ID: "mimo-v2.5-pro", OwnedBy: "opencode_zen"},
	}, nil
}

// ListAllModels implements loop.AgentClient. Returns the same
// canned list wrapped as AnnotatedModels so /models tests work
// without a real HTTP server.
func (m *mockClient) ListAllModels(ctx context.Context, cfg *agent.Config) ([]agent.AnnotatedModel, []agent.ModelError) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil, nil
	}
	var out []agent.AnnotatedModel
	for name := range cfg.Providers {
		out = append(out,
			agent.AnnotatedModel{Provider: name, Info: agent.ModelInfo{ID: "mimo-v2.5-free", OwnedBy: name}},
			agent.AnnotatedModel{Provider: name, Info: agent.ModelInfo{ID: "mimo-v2.5-pro", OwnedBy: name}},
		)
	}
	return out, nil
}

func setupTestModel(t *testing.T, client loop.AgentClient) (Model, func()) {
	// Default test setup uses loadTestRegistry so system
	// commands like /help, /status, /skill are registered
	// (the TUI's slash-command parser only routes
	// target=system commands that have a .md file in the
	// registry). Tests that need a different command set
	// call setupTestModelWithRegistry directly.
	return setupTestModelWithRegistry(t, client, loadTestRegistry(t))
}

func setupTestModelWithRegistry(t *testing.T, client loop.AgentClient, reg *commands.Registry) (Model, func()) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test_session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, reg, "", nil)
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
	updated, cmd := model.Update(humanInputMsg{content: "Create a test file at tests/example_test.go"})
	m := updated.(Model)

	if cmd == nil {
		t.Fatalf("expected non-nil Cmd for coder turn after human input")
	}

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d", len(entries))
	}

	if entries[0].Speaker != transcript.SpeakerYou || entries[0].Content != "Create a test file at tests/example_test.go" {
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

func TestTUI_RenderProposedPlan(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	content, err := transcript.EncodePlan(&transcript.Plan{
		Revision: 2,
		Items: []transcript.PlanItem{
			{ID: 1, Text: "Inspect the current implementation", Status: transcript.PlanItemDone},
			{ID: 2, Text: "Render the plan card", Status: transcript.PlanItemInProgress},
			{ID: 3, Text: "Run the test suite", Status: transcript.PlanItemPending},
		},
	})
	if err != nil {
		t.Fatalf("EncodePlan: %v", err)
	}

	rendered := model.renderProposedPlan(content, 60)
	for _, want := range []string{"PLAN (revised from initial · #2)", "1/3 done", "✓", "▷", "▢", "Render the plan card"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan missing %q:\n%s", want, rendered)
		}
	}

	if err := transcript.AppendPlanSnapshot(model.transcript, &transcript.Plan{
		Revision: 1,
		Items:    []transcript.PlanItem{{ID: 1, Text: "Visible in the transcript", Status: transcript.PlanItemPending}},
	}, "plan approved"); err != nil {
		t.Fatalf("AppendPlanSnapshot: %v", err)
	}
	model.viewport.SetWidth(60)
	if transcriptView := model.renderTranscript(); !strings.Contains(transcriptView, "Visible in the transcript") {
		t.Errorf("expected proposed-plan snapshot in main transcript:\n%s", transcriptView)
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
		"skill.md": `---
target: system
description: Manage skills (list, view, add, delete, force, edit)
---

Skill management subcommands — handled by the TUI.
`,
		"help.md": `---
target: system
description: List available slash commands
---

Show help.
`,
		"strict.md": `---
target: reviewer
description: Toggle strict mode
---

Be strict.
`,
		"clear.md": `---
target: system
description: Wipe current session transcript and start fresh
---

Wipe session.
`,
		"new.md": `---
target: system
description: Start a brand new session
---

New session.
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

// loadTestSkillsRegistry writes a single skill .md file
// (main + mini) into a temp dir, plus a second skill that's
// intentionally left out so the tests can exercise "added
// after load" flow without restarts. Returns the registry
// plus the temp dir (so tests can write to the same dir
// after /skill add and verify reload behavior).
//
// Tests that need the on-disk files to align with the
// model's workDir (e.g. for /skill delete) should use
// `loadTestSkillsRegistryIn(dir)` instead.
func loadTestSkillsRegistry(t *testing.T) (*skills.Registry, string) {
	return loadTestSkillsRegistryIn(t, t.TempDir())
}

// loadTestSkillsRegistryIn writes the seed skills to the
// given dir (which should match the model's workDir for
// tests that read/write files via the slash command path).
func loadTestSkillsRegistryIn(t *testing.T, dir string) (*skills.Registry, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	main := "---\nname: frontend\nsection: frontend\ndescription: UI work.\ntier: main\nmini_ref: frontend-mini.md\ntoken_budget_main: 1000\ntoken_budget_mini: 500\n---\n\nFrontend main body.\n"
	mini := "---\nname: frontend\nsection: frontend\ndescription: UI work.\ntier: mini\n---\n\nFrontend mini body.\n"
	mainPath := filepath.Join(dir, "frontend.md")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatalf("write frontend main to %q: %v", mainPath, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "frontend-mini.md"), []byte(mini), 0o644); err != nil {
		t.Fatalf("write frontend mini: %v", err)
	}
	reg, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("skills.Load: %v", err)
	}
	return reg, dir
}

// TestTUI_SlashCommand_SkillList covers /skill list: the
// command must be routed to the system-handler path, write a
// System entry to the transcript that mentions the seeded
// skill, and not trigger a Coder turn.
func TestTUI_SlashCommand_SkillList(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	skillsReg, _ := loadTestSkillsRegistry(t)
	// Attach the skills registry BEFORE the first /skill
	// invocation so the listing is real (rather than
	// "no skills" because we tried before wiring).
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()
	updated, cmd := model.Update(humanInputMsg{content: "/skill list"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("expected nil cmd (no Coder turn) for /skill list, got %v", cmd)
	}
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		t.Fatal("expected at least 1 transcript entry from /skill list")
	}
	last := entries[len(entries)-1]
	if last.Speaker != transcript.SpeakerSystem {
		t.Errorf("expected system entry, got %q", last.Speaker)
	}
	if !strings.Contains(last.Content, "frontend") {
		t.Errorf("expected list body to mention frontend, got: %q", last.Content)
	}
}

// TestTUI_SlashCommand_SkillView covers /skill view <name>:
// the System entry must include the Main body delimiter and
// the actual body content.
func TestTUI_SlashCommand_SkillView(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	skillsReg, _ := loadTestSkillsRegistry(t)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()
	updated, cmd := model.Update(humanInputMsg{content: "/skill view frontend"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("expected nil cmd, got %v", cmd)
	}
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		t.Fatal("expected at least 1 transcript entry")
	}
	last := entries[len(entries)-1]
	if !strings.Contains(last.Content, "MAIN BODY") {
		t.Errorf("expected MAIN BODY delimiter, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "Frontend main body") {
		t.Errorf("expected main body content, got: %q", last.Content)
	}
}

// TestTUI_SlashCommand_SkillAddThenList covers the Phase 3.7
// flow: /skill add creates a new file on disk, the registry
// is reloaded in the same session, and /skill list now shows
// the new skill without any code change or process restart.
func TestTUI_SlashCommand_SkillAddThenList(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	// Match the model's workDir so the registry's Dir
	// matches where /skill add will write the new files.
	skillsReg, _ := loadTestSkillsRegistryIn(t, model.workDir)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	// /skill add backend → scaffold two files on disk.
	updated, _ := model.Update(humanInputMsg{content: "/skill add backend"})
	m := updated.(Model)
	if _, err := os.Stat(filepath.Join(model.workDir, "backend.md")); err != nil {
		t.Fatalf("expected backend.md to exist after add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(model.workDir, "backend-mini.md")); err != nil {
		t.Fatalf("expected backend-mini.md to exist after add: %v", err)
	}
	// Registry must reflect the new skill after the same
	// session — that's the Phase 3.7 invariant.
	if sk, ok := m.skillsRegistry.Get("backend"); !ok || sk.Name != "backend" {
		t.Errorf("expected backend to be in the live registry, got ok=%v sk=%#v", ok, sk)
	}

	// /skill list → both frontend and backend show up.
	updated, _ = m.Update(humanInputMsg{content: "/skill list"})
	m = updated.(Model)
	entries := m.transcript.Entries()
	last := entries[len(entries)-1]
	if !strings.Contains(last.Content, "frontend") {
		t.Errorf("list should still include frontend, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "backend") {
		t.Errorf("list should include the newly-added backend, got: %q", last.Content)
	}
}

// TestTUI_SlashCommand_SkillAddOpensEditor covers the
// "drop into edit mode" follow-up: /skill add sets the
// inline editor state on the model so the next KeyMsg
// routes to the textarea, not the input box.
func TestTUI_SlashCommand_SkillAddOpensEditor(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	// Seed a real registry in the model's workDir so the
	// follow-up path can re-look up the just-added skill
	// and open the editor on its source path.
	skillsReg, _ := loadTestSkillsRegistryIn(t, model.workDir)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	updated, _ := model.Update(humanInputMsg{content: "/skill add newone"})
	m := updated.(Model)
	if m.skillEditor == nil {
		t.Fatal("expected m.skillEditor to be set after /skill add")
	}
	if m.skillEditor.name != "newone" {
		t.Errorf("expected editor name=newone, got %q", m.skillEditor.name)
	}
	if m.skillEditor.path == "" {
		t.Error("expected editor path to be set")
	}
	// The pending action should also be recorded so
	// post-handler follow-up paths can read it.
	if m.pendingSkillAction == nil {
		t.Error("expected pendingSkillAction to be queued")
	}
}

// TestTUI_SlashCommand_SkillDeleteRequiresConfirmation
// covers the destructive-command gate: /skill delete queues
// a pending confirmation, and the next user message is
// consumed as the confirmation reply rather than a new task.
func TestTUI_SlashCommand_SkillDeleteRequiresConfirmation(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	// Match the model's workDir so the registry loads the
	// seed files from the same dir the model will write
	// to. The TUI's /skill add/delete/edit paths use
	// m.workDir (or m.skillsRegistry.Dir) as their base.
	skillsReg, _ := loadTestSkillsRegistryIn(t, model.workDir)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	// Step 1: user types /skill delete frontend → the
	// command is queued but the file is NOT removed yet.
	updated, _ := model.Update(humanInputMsg{content: "/skill delete frontend"})
	m := updated.(Model)
	if m.pendingSkillAction == nil || m.pendingSkillAction.Kind != skillActionDelete {
		t.Fatalf("expected pendingSkillAction=delete, got %#v", m.pendingSkillAction)
	}
	mainPath := filepath.Join(m.workDir, "frontend.md")
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("expected frontend.md to still exist before confirm, got: %v", err)
	}

	// Step 2: user types "yes" — file is removed.
	updated, _ = m.Update(humanInputMsg{content: "yes"})
	m = updated.(Model)
	if m.pendingSkillAction != nil {
		t.Error("expected pendingSkillAction to be cleared after confirmation")
	}
	if _, err := os.Stat(mainPath); !os.IsNotExist(err) {
		t.Errorf("expected frontend.md to be removed after confirm, got err=%v", err)
	}
}

// TestTUI_SlashCommand_SkillDeleteCancel covers the
// "anything other than yes" path: the file must remain
// intact when the user cancels the confirmation.
func TestTUI_SlashCommand_SkillDeleteCancel(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	skillsReg, _ := loadTestSkillsRegistryIn(t, model.workDir)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	updated, _ := model.Update(humanInputMsg{content: "/skill delete frontend"})
	m := updated.(Model)
	if m.pendingSkillAction == nil {
		t.Fatal("expected pending action")
	}
	// Cancel with "no".
	updated, _ = m.Update(humanInputMsg{content: "no"})
	m = updated.(Model)
	if m.pendingSkillAction != nil {
		t.Error("expected pending action cleared")
	}
	mainPath := filepath.Join(model.workDir, "frontend.md")
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("expected frontend.md to still exist after cancel, got: %v", err)
	}
}

// TestTUI_SlashCommand_SkillForce covers the /skill force
// manual override: after force, the loaded set reports
// IsForced for that section.
func TestTUI_SlashCommand_SkillForce(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	skillsReg, _ := loadTestSkillsRegistry(t)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	updated, _ := model.Update(humanInputMsg{content: "/skill force frontend"})
	m := updated.(Model)
	if !m.loadedSkills.IsForced("frontend") {
		t.Error("expected frontend to be forced after /skill force")
	}
	entries := m.transcript.Entries()
	last := entries[len(entries)-1]
	if !strings.Contains(last.Content, "Forced") {
		t.Errorf("expected System entry to mention Forced, got: %q", last.Content)
	}
}

// TestTUI_SkillEditor_EscCancels covers the editor cancel
// path: when the editor is open, an Esc key dismisses it
// without writing to disk.
func TestTUI_SkillEditor_EscCancels(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	skillsReg, _ := loadTestSkillsRegistryIn(t, model.workDir)
	model.skillsRegistry = skillsReg
	model.loadedSkills = skills.NewLoadedSet()

	updated, _ := model.Update(humanInputMsg{content: "/skill add tobecancelled"})
	m := updated.(Model)
	if m.skillEditor == nil {
		t.Fatal("expected editor to open on /skill add")
	}
	// Esc closes the editor. tea.KeyPressMsg is the v2
	// concrete type for a key event; we synthesize one with
	// the Escape key code.
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	if m.skillEditor != nil {
		t.Error("expected Esc to close the editor")
	}
	if m.pendingSkillAction != nil {
		t.Error("expected pendingSkillAction to clear on cancel")
	}
}

// TestTUI_SlashCommand_SkillHelpEntry checks that /help
// mentions the /skill subcommand, so a new user can find
// the management surface without reading docs.
func TestTUI_SlashCommand_SkillHelpEntry(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	updated, _ := model.Update(humanInputMsg{content: "/help"})
	m := updated.(Model)
	entries := m.transcript.Entries()
	last := entries[len(entries)-1]
	if !strings.Contains(last.Content, "/skill") {
		t.Errorf("/help should mention /skill, got: %q", last.Content)
	}
	if !strings.Contains(last.Content, "list") || !strings.Contains(last.Content, "view") {
		t.Errorf("/help should reference /skill subcommands, got: %q", last.Content)
	}
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
	updated, cmd := model.Update(humanInputMsg{content: "Add a webhook handler in internal/webhooks/stripe.go"})
	m := updated.(Model)

	if cmd == nil {
		t.Errorf("plain message should trigger a Coder turn")
	}
	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Content != "Add a webhook handler in internal/webhooks/stripe.go" {
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
	// Note: Phase 3 (clarify) deliberately skips clarification for slash-command
	// expansions — the human has already committed to a deliberate workflow
	// by typing the command, so a "are you sure?" interruption would be
	// obnoxious. The expanded text just happens to contain trigger keywords.
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
	model := NewModel(tr, coder, reviewer, client, dir, 0, reg, "", nil)
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
		client, dir, 0, loadTestRegistry(t), "", nil)
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
		"mode.md":    "---\ntarget: system\ndescription: View or set current orchestration mode (orchestrator|general|triad)\n---\nView or set mode\n",
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

	// 1. Typing '/' opens popup with all 11 commands sorted (8 base + 3 mode subcommands)
	m := typeString(model, "/")
	if !m.autocompleteActive {
		t.Fatalf("expected autocompleteActive to be true on '/'")
	}
	if len(m.autocompleteCmds) != 11 {
		t.Fatalf("expected 11 commands on '/', got %d", len(m.autocompleteCmds))
	}
	if m.autocompleteCmds[0].Name != "diff" || m.autocompleteCmds[10].Name != "undo" {
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

			// Check that the prompt input bar is present with placeholder
			foundPrompt := false
			for _, line := range lines {
				if strings.Contains(line, "sk Triad") {
					foundPrompt = true
					break
				}
			}
			if !foundPrompt {
				t.Errorf("w=%d h=%d: prompt bar placeholder missing from view", w, h)
			}
		}
	}
}

func TestTUI_TranscriptGapAndBottomPadding(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	_ = model.transcript.Append(transcript.Entry{
		Speaker:   transcript.SpeakerYou,
		Type:      transcript.TypeMessage,
		Content:   "Hello from user",
		Timestamp: time.Now(),
	})
	_ = model.transcript.Append(transcript.Entry{
		Speaker:   transcript.SpeakerCoder,
		Type:      transcript.TypeMessage,
		Content:   "Response from coder",
		Timestamp: time.Now(),
	})

	rendered := model.renderTranscript()
	if !strings.HasSuffix(rendered, "\n\n\n") {
		t.Errorf("expected rendered transcript to end with trailing blank line gap (\\n\\n\\n), got: %q", rendered)
	}

	if !strings.Contains(rendered, "····") {
		t.Errorf("expected rendered transcript to contain entry divider, got: %q", rendered)
	}
}

func TestTUI_SystemLogsInSidebar(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	_ = model.transcript.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   "System event occurred",
		Timestamp: time.Now(),
	})
	_ = model.transcript.Append(transcript.Entry{
		Speaker:   transcript.SpeakerYou,
		Type:      transcript.TypeMessage,
		Content:   "Hello from user",
		Timestamp: time.Now(),
	})

	model.refreshViewport()
	sidebar := model.renderSidebar(60, 30)
	if !strings.Contains(sidebar, "SYSTEM LOGS") {
		t.Errorf("expected sidebar to contain SYSTEM LOGS section header, got: %q", sidebar)
	}
	if !strings.Contains(sidebar, "System event occurred") {
		t.Errorf("expected sidebar to contain system event message, got: %q", sidebar)
	}

	mainChat := model.renderTranscript()
	if strings.Contains(mainChat, "System event occurred") {
		t.Errorf("expected main chat to filter out system messages, got: %q", mainChat)
	}
	if !strings.Contains(mainChat, "Hello from user") {
		t.Errorf("expected main chat to contain user message, got: %q", mainChat)
	}
}

func TestTUI_RenderMessageCalloutBoxes(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	now := time.Now()
	model.transcript.Append(transcript.Entry{
		ID:        1,
		Speaker:   transcript.SpeakerYou,
		Type:      transcript.TypeMessage,
		Content:   "Please update the project",
		Timestamp: now,
	})
	model.transcript.Append(transcript.Entry{
		ID:        2,
		Speaker:   transcript.SpeakerCoder,
		Type:      transcript.TypeMessage,
		Content:   "I will update the project files now.",
		Timestamp: now,
	})
	model.transcript.Append(transcript.Entry{
		ID:        3,
		Speaker:   transcript.SpeakerReviewer,
		Type:      transcript.TypeMessage,
		Content:   "APPROVED: Looks safe and sound.",
		Timestamp: now,
	})

	chat := model.renderTranscript()
	if !strings.Contains(chat, "Please update the project") {
		t.Errorf("expected chat to contain user message, got: %q", chat)
	}
	if !strings.Contains(chat, "I will update the project files now.") {
		t.Errorf("expected chat to contain coder message, got: %q", chat)
	}
	if !strings.Contains(chat, "APPROVED BY REVIEWER") || !strings.Contains(chat, "Looks safe and sound.") {
		t.Errorf("expected chat to contain reviewer approved message, got: %q", chat)
	}
	if !strings.Contains(chat, "╭") || !strings.Contains(chat, "╰") {
		t.Errorf("expected chat messages to be wrapped in rounded box UIs, got: %q", chat)
	}
}

func TestTUI_ModeCommand(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// 1. /mode with no args returns current mode report
	updated, _ := model.Update(humanInputMsg{content: "/mode"})
	m := updated.(Model)
	entries := m.transcript.Entries()
	if len(entries) != 1 || !strings.Contains(entries[0].Content, "Current mode: Orchestrator") {
		t.Fatalf("expected current mode report for /mode, got: %v", entries)
	}

	// 2. /mode general sets mode to General
	updated, _ = m.Update(humanInputMsg{content: "/mode general"})
	m = updated.(Model)
	entries = m.transcript.Entries()
	if len(entries) != 2 || !strings.Contains(entries[1].Content, "Mode set to: General") {
		t.Fatalf("expected confirmation for /mode general, got: %v", entries[1].Content)
	}

	// 3. /mode triad sets mode to Triad
	updated, _ = m.Update(humanInputMsg{content: "/mode triad"})
	m = updated.(Model)
	entries = m.transcript.Entries()
	if len(entries) != 3 || !strings.Contains(entries[2].Content, "Mode set to: Triad") {
		t.Fatalf("expected confirmation for /mode triad, got: %v", entries[2].Content)
	}

	// 4. /mode orchestrator sets mode to Orchestrator
	updated, _ = m.Update(humanInputMsg{content: "/mode orchestrator"})
	m = updated.(Model)
	entries = m.transcript.Entries()
	if len(entries) != 4 || !strings.Contains(entries[3].Content, "Mode set to: Orchestrator") {
		t.Fatalf("expected confirmation for /mode orchestrator, got: %v", entries[3].Content)
	}

	// 5. /mode invalid returns error
	updated, _ = m.Update(humanInputMsg{content: "/mode invalid"})
	m = updated.(Model)
	entries = m.transcript.Entries()
	if len(entries) != 5 || !strings.Contains(entries[4].Content, "Unknown mode") {
		t.Fatalf("expected unknown mode error for /mode invalid, got: %v", entries[4].Content)
	}
}

func TestTUI_ModeGeneralExecution(t *testing.T) {
	client := &mockClient{}
	client.coderResponses = append(client.coderResponses, agent.AgentResponse{Text: "Plain text response in general mode"})
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// Switch to general mode
	updated, _ := model.Update(humanInputMsg{content: "/mode general"})
	model = updated.(Model)

	// Send task
	updated, cmd := model.Update(humanInputMsg{content: "hello world"})
	model = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected cmd for coder turn in general mode")
	}

	// Execute coder turn
	msg := cmd()
	updated, nextCmd := model.Update(msg)
	model = updated.(Model)

	if nextCmd != nil {
		t.Errorf("expected no nextCmd (no Reviewer turn) in ModeGeneral, got %v", nextCmd)
	}

	entries := model.transcript.Entries()
	hasReviewer := false
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerReviewer {
			hasReviewer = true
		}
	}
	if hasReviewer {
		t.Errorf("expected no Reviewer entries in General mode, but found Reviewer entry")
	}
}

func TestTUI_ModeAutocompleteSuggestions(t *testing.T) {
	model, cleanup := setupAutocompleteTestModel(t)
	defer cleanup()

	// 1. Typing '/mode' shows /mode plus all 3 mode options
	m := typeString(model, "/mode")
	if !m.autocompleteActive {
		t.Fatalf("expected autocompleteActive to be true on '/mode'")
	}
	if len(m.autocompleteCmds) != 4 {
		t.Fatalf("expected 4 autocompleteCmds on '/mode', got %d: %+v", len(m.autocompleteCmds), m.autocompleteCmds)
	}

	// 2. Typing space (' ') narrows to 3 mode options (/mode general, /mode triad, /mode orchestrator)
	m = typeString(m, " ")
	if !m.autocompleteActive || len(m.autocompleteCmds) != 3 {
		t.Fatalf("expected 3 autocompleteCmds on '/mode ', got %d: %+v", len(m.autocompleteCmds), m.autocompleteCmds)
	}

	// 3. Typing 'g' narrows to '/mode general'
	m = typeString(m, "g")
	if !m.autocompleteActive || len(m.autocompleteCmds) != 1 || m.autocompleteCmds[0].Name != "mode general" {
		t.Fatalf("expected 1 match ('mode general') on '/mode g', got %+v", m.autocompleteCmds)
	}

	// 4. Pressing Tab selects '/mode general'
	updated, _ := m.Update(makeKeyMsg("tab"))
	m = updated.(Model)
	if m.input.Value() != "/mode general" {
		t.Fatalf("expected input value '/mode general', got %q", m.input.Value())
	}
}

func TestTUI_ModeMismatchNotice(t *testing.T) {
	client := &mockClient{
		coderResponses: []agent.AgentResponse{
			{Text: "Acknowledged."},
		},
	}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	// Switch to Triad mode
	model.currentMode = loop.ModeTriad

	// Submit a trivial task via humanInputMsg
	updated, _ := model.Update(humanInputMsg{content: "hello"})
	m := updated.(Model)

	entries := m.transcript.Entries()
	var foundMismatchNote bool
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "you're in Triad mode; this looks trivial") {
			foundMismatchNote = true
			break
		}
	}

	if !foundMismatchNote {
		t.Fatalf("expected TUI transcript to contain passive mode mismatch note for forced triad mode + trivial task. Entries: %+v", entries)
	}
}

func TestTUI_JourneyCommand(t *testing.T) {
	cmdDir := filepath.Join(t.TempDir(), "commands")
	_ = os.MkdirAll(cmdDir, 0755)
	journeyMd := "---\nname: journey\ntarget: system\ndescription: Visualize commit journey\n---\nVisualize commit history timeline\n"
	_ = os.WriteFile(filepath.Join(cmdDir, "journey.md"), []byte(journeyMd), 0644)
	reg, err := commands.Load(cmdDir)
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}

	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, reg)
	defer cleanup()

	// Test /journey on zero commits
	updated, _ := model.Update(humanInputMsg{content: "/journey"})
	m := updated.(Model)
	if !m.showJourney {
		t.Fatalf("expected m.showJourney to be true after /journey command")
	}
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		t.Fatalf("expected transcript entry for /journey command")
	}
	lastEntry := entries[len(entries)-1]
	if !strings.Contains(lastEntry.Content, "No Triad commit history") {
		t.Errorf("expected zero commit notice in transcript, got: %s", lastEntry.Content)
	}

	// Verify sidebar view in journey mode contains COMMIT JOURNEY
	sbView := m.renderSidebar(32, 30)
	if !strings.Contains(sbView, "COMMIT JOURNEY") {
		t.Errorf("expected sidebar view to contain COMMIT JOURNEY header, got: %s", sbView)
	}

	// Test /journey toggle off
	updatedOff, _ := m.Update(humanInputMsg{content: "/journey"})
	mOff := updatedOff.(Model)
	if mOff.showJourney {
		t.Fatalf("expected m.showJourney to be false after second /journey command")
	}

	// Test /journey --export
	updated2, _ := m.Update(humanInputMsg{content: "/journey --export"})
	m2 := updated2.(Model)
	entries2 := m2.transcript.Entries()
	lastEntry2 := entries2[len(entries2)-1]
	if !strings.Contains(lastEntry2.Content, "[Commit Journey] Exported visual HTML report") {
		t.Errorf("expected export confirmation in transcript, got: %s", lastEntry2.Content)
	}
	if _, err := os.Stat(filepath.Join(m2.workDir, "journey_report.html")); os.IsNotExist(err) {
		t.Errorf("expected journey_report.html to be created")
	}
}

func TestTUI_LiveSessionTokenStats(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()

	model.coder.InputCostPerToken = 0.000003
	model.coder.OutputCostPerToken = 0.000015
	model.coder.ContextWindow = 1000000

	model.reviewer.InputCostPerToken = 0.000003
	model.reviewer.OutputCostPerToken = 0.000015
	model.reviewer.ContextWindow = 1000000

	// 1. Send Coder agentResponseMsg with token usage and cache info
	coderResp := agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp: agent.AgentResponse{
			Text: "I will refactor the code.",
			Usage: agent.Usage{
				PromptTokens:     26000,
				CompletionTokens: 1200,
				TotalTokens:      27200,
				PromptTokensDetails: &agent.PromptTokensDetails{
					CachedTokens: 20000,
				},
			},
		},
	}
	up1, _ := model.Update(coderResp)
	m1 := up1.(Model)

	// 2. Send Reviewer agentResponseMsg with token usage
	reviewerResp := agentResponseMsg{
		speaker: transcript.SpeakerReviewer,
		resp: agent.AgentResponse{
			Text: "APPROVED",
			Usage: agent.Usage{
				PromptTokens:     5000,
				CompletionTokens: 500,
				TotalTokens:      5500,
			},
		},
	}
	up2, _ := m1.Update(reviewerResp)
	m2 := up2.(Model)

	summary := m2.renderStatsSummary()

	// Verify 3 fields: 1) Session context token usage vs max context window (5k/1M), 2) % context used (0.5%), 3) Running cost ($...)
	if !strings.Contains(summary, "5k/1M") {
		t.Errorf("Expected summary to contain '5k/1M', got: %s", summary)
	}
	if !strings.Contains(summary, "(0.5%)") {
		t.Errorf("Expected summary to contain '(0.5%%)', got: %s", summary)
	}
	if !strings.Contains(summary, "$") {
		t.Errorf("Expected summary to contain '$', got: %s", summary)
	}

	footer := m2.renderInputFooter(140)
	if !strings.Contains(footer, "5k/1M") {
		t.Errorf("Expected renderInputFooter output to include live stats, got: %s", footer)
	}
}

func TestRenderInputFooterWorkDir(t *testing.T) {
	m, cleanup := setupTestModel(t, nil)
	defer cleanup()

	m.workDir = "c:/Users/bari2/Desktop/triad"
	m.coder.Model = "mimo-v2.5-pro"

	// Test wide width (should contain full path or base directory and ORCHESTRATOR mode)
	footerWide := m.renderInputFooter(120)
	if !strings.Contains(footerWide, "triad") {
		t.Errorf("Expected wide footer to contain 'triad', got: %s", footerWide)
	}
	if !strings.Contains(footerWide, "ORCHESTRATOR") {
		t.Errorf("Expected wide footer to contain 'ORCHESTRATOR', got: %s", footerWide)
	}

	// Test medium width (should fall back to folder name 'triad' when space is tighter)
	footerMedium := m.renderInputFooter(80)
	if !strings.Contains(footerMedium, "triad") {
		t.Errorf("Expected medium footer to contain 'triad', got: %s", footerMedium)
	}
}

func TestTUI_InputBarTokenCount(t *testing.T) {
	m, cleanup := setupTestModel(t, nil)
	defer cleanup()

	// 1. Verify renderInputFooter displays 0 tokens when input is empty
	footerEmpty := m.renderInputFooter(120)
	if !strings.Contains(footerEmpty, "0") || !strings.Contains(footerEmpty, "tokens") {
		t.Errorf("expected footer to display '0 tokens' for empty input, got:\n%s", footerEmpty)
	}

	// 2. Set input value and verify updated token count in footer
	m.input.SetValue("This is a sample prompt to verify token count rendering in the TUI input bar.")
	footerWithText := m.renderInputFooter(120)
	if !strings.Contains(footerWithText, "20") || !strings.Contains(footerWithText, "tokens") {
		t.Errorf("expected footer to display '20 tokens' for sample input, got:\n%s", footerWithText)
	}

	// 3. Verify renderInputBar is clean when idle
	barEmpty := m.renderInputBar(100)
	if strings.Contains(barEmpty, "Enter") {
		t.Errorf("expected input bar to NOT contain 'Enter' keycap when idle, got:\n%s", barEmpty)
	}
}

func TestTUI_InputBarMultiLineHeight(t *testing.T) {
	m, cleanup := setupTestModel(t, nil)
	defer cleanup()

	// 1. Empty input should have height 1 (3 lines total for input bar including borders)
	bar1 := m.renderInputBar(100)
	if h := lipgloss.Height(bar1); h != 3 {
		t.Errorf("expected 3 total lines for empty input bar, got %d:\n%s", h, bar1)
	}
	if m.input.Height() != 1 {
		t.Errorf("expected input component height 1 for empty input, got %d", m.input.Height())
	}

	// 2. Multi-line input (3 lines) should scale input bar to height 3 (5 lines total)
	m.input.SetValue("line 1\nline 2\nline 3")
	bar3 := m.renderInputBar(100)
	if h := lipgloss.Height(bar3); h != 5 {
		t.Errorf("expected 5 total lines for 3-line input bar, got %d:\n%s", h, bar3)
	}
	if m.input.Height() != 3 {
		t.Errorf("expected input component height 3 for 3-line input, got %d", m.input.Height())
	}

	// 3. Multi-line input exceeding 4 lines (e.g. 6 lines) must be capped at 4 lines (6 lines total with borders)
	m.input.SetValue("line 1\nline 2\nline 3\nline 4\nline 5\nline 6")
	barCapped := m.renderInputBar(100)
	if h := lipgloss.Height(barCapped); h != 6 {
		t.Errorf("expected 6 total lines max (4 content + 2 borders) for 6-line input, got %d:\n%s", h, barCapped)
	}
	if m.input.Height() != 4 {
		t.Errorf("expected input component height capped at 4, got %d", m.input.Height())
	}

	// 4. Resetting input should restore height to 1
	m.input.Reset()
	barReset := m.renderInputBar(100)
	if h := lipgloss.Height(barReset); h != 3 {
		t.Errorf("expected 3 total lines for reset input bar, got %d:\n%s", h, barReset)
	}
	if m.input.Height() != 1 {
		t.Errorf("expected input component height restored to 1, got %d", m.input.Height())
	}
}

func TestTUI_InputBarNoYouPillAndFullWidth(t *testing.T) {
	client := &mockClient{}
	m, cleanup := setupTestModel(t, client)
	defer cleanup()

	rendered := m.renderInputBar(100)
	if strings.Contains(rendered, "YOU") {
		t.Errorf("expected input bar to NOT contain 'YOU' text icon, got:\n%s", rendered)
	}

	// renderInputBar(100) sets width on m.input inside renderInputBar
	containerW := 100 - m.styles.InputContainer.GetHorizontalFrameSize()
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) < 3 {
		t.Fatalf("expected at least 3 lines for input bar, got %d", len(renderedLines))
	}
	// Middle line length (without ANSI or trailing spaces) matches container width + frame size
	middleLineW := lipgloss.Width(renderedLines[1])
	if middleLineW != 100 {
		t.Errorf("expected rendered input bar width %d, got %d (container content width %d)", 100, middleLineW, containerW)
	}
}

func TestTUI_AltBackspaceSmoothWordDelete(t *testing.T) {
	client := &mockClient{}
	m, cleanup := setupTestModel(t, client)
	defer cleanup()

	m.input.SetValue("hello world test")
	m.input.CursorEnd()

	// Test alt+backspace key msg
	up, _ := m.Update(makeKeyMsg("alt+backspace"))
	m = up.(Model)
	if val := m.input.Value(); val != "hello world " {
		t.Errorf("expected 'hello world ' after alt+backspace, got %q", val)
	}

	// Test alt+bspace key msg
	up, _ = m.Update(makeKeyMsg("alt+bspace"))
	m = up.(Model)
	if val := m.input.Value(); val != "hello " {
		t.Errorf("expected 'hello ' after alt+bspace, got %q", val)
	}

	// Test ctrl+w key msg
	up, _ = m.Update(makeKeyMsg("ctrl+w"))
	m = up.(Model)
	if val := m.input.Value(); val != "" {
		t.Errorf("expected empty string after ctrl+w, got %q", val)
	}
}

func TestTUI_SlashCommand_Clear_RequiresConfirmation(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	// 1. Set mode to General and append entries
	model.currentMode = loop.ModeGeneral
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Initial prompt before clear",
	})
	if len(model.transcript.Entries()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(model.transcript.Entries()))
	}

	// 2. Issue /clear for the first time without --force
	updated, cmd := model.Update(humanInputMsg{content: "/clear"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("/clear should not trigger a Coder turn, got cmd=%v", cmd)
	}

	// Transcript should NOT be wiped yet
	entries := m.transcript.Entries()
	if len(entries) < 1 {
		t.Fatalf("expected transcript to remain intact before confirmation, got %d entries", len(entries))
	}
	lastEntry := entries[len(entries)-1]
	if !strings.Contains(lastEntry.Content, "erase the current session's transcript") {
		t.Errorf("expected confirmation prompt message, got: %q", lastEntry.Content)
	}
	if m.currentMode != loop.ModeGeneral {
		t.Errorf("expected current_mode to stay General, got: %v", m.currentMode)
	}

	// 3. Issue /clear second time to confirm
	updated2, _ := m.Update(humanInputMsg{content: "/clear"})
	m2 := updated2.(Model)

	// Transcript should now be wiped and contain only the new System notification entry
	entries2 := m2.transcript.Entries()
	if len(entries2) != 1 {
		t.Fatalf("expected exactly 1 entry in cleared transcript, got %d", len(entries2))
	}
	if entries2[0].Speaker != transcript.SpeakerSystem {
		t.Errorf("expected System speaker for clear entry, got: %q", entries2[0].Speaker)
	}
	if !strings.Contains(entries2[0].Content, "Session transcript cleared") {
		t.Errorf("expected cleared message, got: %q", entries2[0].Content)
	}
	if m2.currentMode != loop.ModeGeneral {
		t.Errorf("expected current_mode to stay General after clear, got: %v", m2.currentMode)
	}
}

func TestTUI_SlashCommand_Clear_Force(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModelWithRegistry(t, client, loadTestRegistry(t))
	defer cleanup()

	model.currentMode = loop.ModeTriad
	_ = model.transcript.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Initial prompt before forced clear",
	})

	// Issue /clear --force
	updated, cmd := model.Update(humanInputMsg{content: "/clear --force"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("/clear --force should not trigger a Coder turn, got cmd=%v", cmd)
	}

	entries := m.transcript.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry after forced clear, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Content, "Session transcript cleared") {
		t.Errorf("expected cleared message, got: %q", entries[0].Content)
	}
	if m.currentMode != loop.ModeTriad {
		t.Errorf("expected current_mode to stay Triad after forced clear, got: %v", m.currentMode)
	}
}

func TestTUI_SlashCommand_New(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "sessions")
	_ = os.MkdirAll(sessionDir, 0755)

	oldPath := filepath.Join(sessionDir, "session_20260728_100000.jsonl")
	tr := transcript.NewTranscript(oldPath)
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Task in old session",
	})

	client := &mockClient{}
	model := NewModel(tr, agent.AgentConfig{}, agent.AgentConfig{}, client, tempDir, 5*time.Second, loadTestRegistry(t), "", nil)
	model.currentMode = loop.ModeTriad

	// Issue /new
	updated, cmd := model.Update(humanInputMsg{content: "/new"})
	m := updated.(Model)
	if cmd != nil {
		t.Errorf("/new should not trigger a Coder turn, got cmd=%v", cmd)
	}

	newPath := m.transcript.FilePath()
	if newPath == oldPath {
		t.Errorf("expected new session file path, got same as old path: %s", newPath)
	}
	if filepath.Dir(newPath) != sessionDir {
		t.Errorf("expected new session to be created in %s, got %s", sessionDir, filepath.Dir(newPath))
	}

	// Verify mode was reset to orchestrator (default)
	if m.currentMode != loop.ModeOrchestrator {
		t.Errorf("expected current_mode to be reset to Orchestrator, got: %v", m.currentMode)
	}

	// Verify old session file is untouched and readable
	loadedOld, err := transcript.LoadFromFile(oldPath)
	if err != nil {
		t.Fatalf("failed to load old session file %s: %v", oldPath, err)
	}
	oldEntries := loadedOld.Entries()
	if len(oldEntries) != 1 || oldEntries[0].Content != "Task in old session" {
		t.Fatalf("old session file was corrupted or altered, entries: %v", oldEntries)
	}

	// Append a message to the new session
	updated2, _ := m.Update(humanInputMsg{content: "/status"})
	m2 := updated2.(Model)

	// Re-verify old session remains completely untouched
	loadedOld2, err := transcript.LoadFromFile(oldPath)
	if err != nil {
		t.Fatalf("failed to reload old session file: %v", err)
	}
	if len(loadedOld2.Entries()) != 1 {
		t.Errorf("old session entries count changed after activity in new session: got %d", len(loadedOld2.Entries()))
	}
	if len(m2.transcript.Entries()) == 0 {
		t.Errorf("expected new session transcript to contain entries")
	}
}



