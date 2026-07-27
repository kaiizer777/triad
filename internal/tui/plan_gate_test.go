package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// TestTUI_PlanGate_CurrentPlanRestoredFromTranscript verifies the
// resume path: when the transcript already contains a
// TypeProposedPlan entry, RestoreSessionState (called from
// NewModel) must populate m.currentPlan so a fresh session can
// pick up where the previous one left off.
func TestTUI_PlanGate_CurrentPlanRestoredFromTranscript(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript(dir + "/session.jsonl")
	// Pre-populate a TypeProposedPlan entry — this is what a
	// previous session would have written before the process
	// died or the user quit and resumed.
	if err := tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeProposedPlan,
		Content:   `{"reason":"plan approved (rev #1)","plan":{"revision":1,"items":[{"id":1,"text":"set up foo","status":"pending"},{"id":2,"text":"wire bar","status":"pending"}]}}`,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	m := NewModel(tr, agent.AgentConfig{Name: "Coder", HasTools: true},
		agent.AgentConfig{Name: "Reviewer", HasTools: false},
		&mockClient{}, dir, 0, nil, "", nil)

	if m.currentPlan == nil {
		t.Fatal("expected m.currentPlan to be restored from transcript, got nil")
	}
	if len(m.currentPlan.Items) != 2 {
		t.Errorf("expected 2 plan items, got %d", len(m.currentPlan.Items))
	}
	if m.currentPlan.Items[0].ID != 1 || m.currentPlan.Items[0].Status != transcript.PlanItemPending {
		t.Errorf("expected item 1 to be pending, got ID=%d status=%q", m.currentPlan.Items[0].ID, m.currentPlan.Items[0].Status)
	}
}

// TestTUI_PlanGate_RejectsNonPlanCallInTriadMode verifies the
// gate fires for non-trivial tasks in Triad mode: Coder
// proposes write_file without a prior submit_plan, and the
// gate must reject (System note + Coder gets another turn) —
// NOT forward to the Reviewer and NOT execute.
func TestTUI_PlanGate_RejectsNonPlanCallInTriadMode(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	// Force Triad mode so PlanRequiredForTask returns true
	// for non-trivial tasks.
	model.currentMode = loop.ModeTriad

	tc := agent.ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"path":"hello.txt","content":"hi"}`,
		},
	}

	// 1. Human submits a non-trivial task.
	updated, cmd := model.Update(humanInputMsg{content: "Add a hello.txt file with a greeting"})
	m := updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a Coder turn cmd after human input")
	}
	// Drain cmd — we just need its side-effect on the model.
	_ = cmd

	// 2. Coder proposes write_file directly, skipping submit_plan.
	updated, cmd = m.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{tc}},
	})
	m = updated.(Model)

	// The gate must reject: NO Reviewer turn (cmd should be
	// another Coder turn, not a Reviewer turn), AND a
	// "[Plan Gate]" System note must appear in the transcript.
	entries := m.transcript.Entries()
	var foundRejection bool
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem &&
			strings.Contains(e.Content, "[Plan Gate]") &&
			strings.Contains(e.Content, "write_file") {
			foundRejection = true
			break
		}
	}
	if !foundRejection {
		t.Fatalf("expected [Plan Gate] rejection System note, got entries: %+v", entries)
	}
	// Verify no Reviewer entry landed — the gate stopped the
	// cycle before Reviewer.
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerReviewer {
			t.Errorf("Reviewer must not see rejected-by-gate actions, but found: %+v", e)
		}
	}
}

// TestTUI_PlanGate_AcceptsSubmitPlanThenAllowsAction verifies
// the happy path: Coder submits a plan, the gate records
// currentPlan, the next tool call passes the gate, and the
// per-action binding flips the bound item to in_progress.
func TestTUI_PlanGate_AcceptsSubmitPlanThenAllowsAction(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	model.currentMode = loop.ModeTriad

	// 1. Human submits a non-trivial task.
	updated, cmd := model.Update(humanInputMsg{content: "Create formatter.go with a reusable string formatter"})
	m := updated.(Model)
	_ = cmd

	// 2. Coder proposes submit_plan with a 2-item plan.
	planTC := agent.ToolCall{
		ID:   "call_plan",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "submit_plan",
			Arguments: `{"plan":{"items":[{"id":1,"text":"set up jwt"},{"id":2,"text":"wire routes"}]}}`,
		},
	}
	updated, cmd = m.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{planTC}},
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a Coder turn cmd after submit_plan")
	}
	_ = cmd

	// Plan must be recorded on the model.
	if m.currentPlan == nil {
		t.Fatalf("expected m.currentPlan to be set after submit_plan, got nil")
	}
	if len(m.currentPlan.Items) != 2 {
		t.Errorf("expected 2 plan items, got %d", len(m.currentPlan.Items))
	}

	// 3. Coder proposes a write_file action. The gate should
	// pass it through (currentPlan != nil), bind it to the
	// first pending item, and forward to Reviewer.
	writeTC := agent.ToolCall{
		ID:   "call_write",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"path":"jwt.go","content":"package jwt"}`,
		},
	}
	updated, _ = m.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{writeTC}},
	})
	m = updated.(Model)

	// The bound item must be flipped to in_progress.
	if m.planBoundItemID != 1 {
		t.Errorf("expected planBoundItemID=1, got %d", m.planBoundItemID)
	}
	if m.currentPlan.Items[0].Status != transcript.PlanItemInProgress {
		t.Errorf("expected item 1 to be in_progress, got %q", m.currentPlan.Items[0].Status)
	}
}

// TestTUI_PlanGate_TrivialTaskBypassesGate verifies that
// trivial tasks (classified as TierTrivial by ClassifyTask)
// do NOT trip the gate — even in Triad mode, "hello" is fine
// without a plan.
func TestTUI_PlanGate_TrivialTaskBypassesGate(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	model.currentMode = loop.ModeTriad

	// A trivial "hello" message — ClassifyTask should return
	// TierTrivial, so PlanRequiredForTask returns false.
	updated, cmd := model.Update(humanInputMsg{content: "hi"})
	m := updated.(Model)
	_ = cmd
	if m.planRequired {
		t.Errorf("expected planRequired=false for trivial task, got true")
	}
	if !m.planBypassed {
		t.Errorf("expected planBypassed=true for trivial task, got false")
	}

	// A write_file Coder response should pass through to
	// Reviewer (no rejection).
	tc := agent.ToolCall{
		ID:   "call_x",
		Type: "function",
		Function: agent.ToolCallFunction{
			Name:      "write_file",
			Arguments: `{"path":"x.txt","content":"x"}`,
		},
	}
	updated, _ = m.Update(agentResponseMsg{
		speaker: transcript.SpeakerCoder,
		resp:    agent.AgentResponse{ToolCalls: []agent.ToolCall{tc}},
	})
	m = updated.(Model)

	// No [Plan Gate] rejection should appear.
	for _, e := range m.transcript.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "[Plan Gate]") {
			t.Errorf("expected no [Plan Gate] rejection for trivial task, got: %q", e.Content)
		}
	}
}

// TestTUI_PlanGate_GeneralModeBypassesGate verifies that
// general mode never requires a plan (no Reviewer, no gate).
func TestTUI_PlanGate_GeneralModeBypassesGate(t *testing.T) {
	client := &mockClient{}
	model, cleanup := setupTestModel(t, client)
	defer cleanup()
	model.currentMode = loop.ModeGeneral

	updated, cmd := model.Update(humanInputMsg{content: "Build handler.go with a request handler"})
	m := updated.(Model)
	_ = cmd
	if m.planRequired {
		t.Errorf("expected planRequired=false in ModeGeneral, got true")
	}
	if !m.planBypassed {
		t.Errorf("expected planBypassed=true in ModeGeneral, got false")
	}
	// Sanity: the cmd we got back should have been a Coder
	// turn, and the gate should never have been mentioned.
	_ = cmd
}
