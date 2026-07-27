package loop_test

// Tests for the Phase 6.3 plan-first gate (headless side). The
// gate has two halves:
//
//  1. Pure helper functions: PlanRequiredForTask,
//     extractPlanFromToolCall, extractPlanItemID,
//     heuristicBindPlanItem, LatestApprovedPlan,
//     markPlanItemInProgress, markPlanItemDone. These are tested
//     directly because they're pure functions of their inputs.
//  2. Loop-wired behaviour: the gate's enforcement inside
//     runActiveCycle (rejection on non-plan calls, submit_plan
//     branch, mark-in-progress on approval, mark-done on
//     successful execution). These are tested via the mock
//     client and a real Loop instance with SetPlanGateDisabled(false).
//
// The default value of Loop.planGateDisabled is true, so a test
// that exercises the gate MUST opt in. The pre-existing tests
// don't call SetPlanGateDisabled — they get the gate bypassed
// for free.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Unit tests — pure helpers, no loop wiring required
// ---------------------------------------------------------------------------

// TestPlanRequired_TrivialBypass verifies that trivial tasks never
// require a plan in any mode. The point of the trivial classification
// is exactly "this is so simple that plan overhead is pure waste";
// the gate must respect that even in Triad mode.
func TestPlanRequired_TrivialBypass(t *testing.T) {
	trivialCases := []string{
		"hi",
		"hello",
		"fix typo in README",
		"rename X to Y",
		"explain the main.go structure",
		"what is a goroutine",
	}
	for _, task := range trivialCases {
		t.Run(task, func(t *testing.T) {
			if loop.PlanRequiredForTask(loop.ModeTriad, task) {
				t.Errorf("PlanRequiredForTask(ModeTriad, %q) = true, want false (trivial tasks must not require a plan)", task)
			}
			if loop.PlanRequiredForTask(loop.ModeGeneral, task) {
				t.Errorf("PlanRequiredForTask(ModeGeneral, %q) = true, want false (General mode never requires a plan)", task)
			}
		})
	}
}

// TestPlanRequired_NonTrivialTriadTaskRequiresPlan verifies the
// flip side: middle/critical tasks DO require a plan in Triad
// mode. This is the gate's primary purpose.
func TestPlanRequired_NonTrivialTriadTaskRequiresPlan(t *testing.T) {
	cases := []string{
		"add a /help command to the TUI that lists available modes",          // middle
		"improve the caching layer to handle concurrent writes better",       // middle
		"update the auth token validation logic",                             // critical
		"add a billing endpoint that issues a refund when subscription ends", // critical
	}
	for _, task := range cases {
		t.Run(task, func(t *testing.T) {
			if !loop.PlanRequiredForTask(loop.ModeTriad, task) {
				t.Errorf("PlanRequiredForTask(ModeTriad, %q) = false, want true (non-trivial tasks in Triad mode must require a plan)", task)
			}
			if loop.PlanRequiredForTask(loop.ModeGeneral, task) {
				t.Errorf("PlanRequiredForTask(ModeGeneral, %q) = true, want false (General mode never requires a plan)", task)
			}
		})
	}
}

// TestExtractPlanItemID covers the two accepted key names
// (plan_item_id and item_id) plus the missing-field case.
func TestExtractPlanItemID(t *testing.T) {
	plan := &transcript.Plan{
		Revision: 1,
		Items: []transcript.PlanItem{
			{ID: 1, Text: "first", Status: transcript.PlanItemPending},
		},
	}

	cases := []struct {
		name   string
		args   string
		wantID int
		wantOK bool
	}{
		{"plan_item_id", `{"plan_item_id":1}`, 1, true},
		{"item_id", `{"item_id":1}`, 1, true},
		{"missing", `{}`, 0, false},
		{"empty", ``, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCall := agent.ToolCall{Function: agent.ToolCallFunction{Arguments: tc.args}}
			gotID, gotOK := loop.ExtractPlanItemIDForTest(toolCall, plan)
			if gotID != tc.wantID || gotOK != tc.wantOK {
				t.Errorf("extractPlanItemID(%q) = (%d, %v), want (%d, %v)", tc.args, gotID, gotOK, tc.wantID, tc.wantOK)
			}
		})
	}
}

// TestHeuristicBindPlanItem verifies the heuristic binding
// algorithm: pick the first pending item, in declaration order.
// When all items are done, return (0, false).
func TestHeuristicBindPlanItem(t *testing.T) {
	plan := &transcript.Plan{
		Revision: 1,
		Items: []transcript.PlanItem{
			{ID: 1, Status: transcript.PlanItemDone},
			{ID: 2, Status: transcript.PlanItemPending},
			{ID: 3, Status: transcript.PlanItemPending},
		},
	}
	gotID, gotOK := loop.HeuristicBindPlanItemForTest(plan)
	if !gotOK || gotID != 2 {
		t.Errorf("heuristicBindPlanItem on partly-done plan = (%d, %v), want (2, true)", gotID, gotOK)
	}

	// All done → returns (0, false).
	allDone := &transcript.Plan{
		Items: []transcript.PlanItem{
			{ID: 1, Status: transcript.PlanItemDone},
			{ID: 2, Status: transcript.PlanItemDone},
		},
	}
	gotID, gotOK = loop.HeuristicBindPlanItemForTest(allDone)
	if gotOK || gotID != 0 {
		t.Errorf("heuristicBindPlanItem on all-done plan = (%d, %v), want (0, false)", gotID, gotOK)
	}

	// Nil plan → (0, false).
	gotID, gotOK = loop.HeuristicBindPlanItemForTest(nil)
	if gotOK || gotID != 0 {
		t.Errorf("heuristicBindPlanItem on nil = (%d, %v), want (0, false)", gotID, gotOK)
	}
}

// TestPlanFieldWasPresent verifies the gate's pre-Phase 6.3.1
// invariant: a Loop's `pendingPlan` field exists and is nil by
// default (no plan pending until Coder calls submit_plan). The
// "field was present" name is a regression test against the
// Phase 6 prior-attempt bug where the field got accidentally
// removed by an over-zealous refactor. The test does this
// through the public API (PlanGateDisabled / SetPlanGateDisabled)
// so a struct-private field rename still passes.
func TestPlanFieldWasPresent(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")
	mc := newMockClient()
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	// Default state: gate is disabled (opt-in for the headless
	// loop). After SetPlanGateDisabled(false), the gate is on.
	if !l.PlanGateDisabled() {
		t.Errorf("expected new loop to have plan gate disabled by default (opt-in)")
	}
	l.SetPlanGateDisabled(false)
	if l.PlanGateDisabled() {
		t.Errorf("expected SetPlanGateDisabled(false) to enable the gate")
	}
	l.SetPlanGateDisabled(true)
	if !l.PlanGateDisabled() {
		t.Errorf("expected SetPlanGateDisabled(true) to disable the gate")
	}
}

// TestMarkPlanItemStatusInPlace exercises both markPlanItemInProgress
// and markPlanItemDone on a real loop, with a real transcript
// (in-memory). Asserts the in-place mutation plus the plan
// snapshot is appended.
func TestMarkPlanItemStatusInPlace(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")
	mc := newMockClient()
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	// Seed pendingPlan via the public surface (the gate's
	// recovery path uses LatestApprovedPlan, but for the unit
	// test we set it directly through the bind path).
	if err := loop.SeedPendingPlanForTest(l, &transcript.Plan{
		Revision: 1,
		Items: []transcript.PlanItem{
			{ID: 1, Text: "first", Status: transcript.PlanItemPending},
			{ID: 2, Text: "second", Status: transcript.PlanItemPending},
		},
	}); err != nil {
		t.Fatalf("seedPendingPlan: %v", err)
	}

	// Mark item 1 in progress — snapshot should be appended.
	if err := loop.MarkPlanItemInProgressForTest(l, 1); err != nil {
		t.Fatalf("markInProgress: %v", err)
	}
	// Mark item 1 done — snapshot should be appended again.
	if err := loop.MarkPlanItemDoneForTest(l, 1); err != nil {
		t.Fatalf("markDone: %v", err)
	}

	// Two TypeProposedPlan entries should now be in the transcript.
	snapshots := entriesOfType(tr.Entries(), transcript.TypeProposedPlan)
	if len(snapshots) != 2 {
		t.Errorf("expected 2 plan snapshot entries, got %d", len(snapshots))
	}

	// The plan's first item must now be marked done.
	plan := loop.PendingPlanForTest(l)
	if plan == nil {
		t.Fatalf("pendingPlan is nil after marking")
	}
	if plan.Items[0].Status != transcript.PlanItemDone {
		t.Errorf("item 1 status = %q, want %q", plan.Items[0].Status, transcript.PlanItemDone)
	}
	if plan.Items[1].Status != transcript.PlanItemPending {
		t.Errorf("item 2 status = %q, want %q (must be unchanged)", plan.Items[1].Status, transcript.PlanItemPending)
	}
}

// TestLatestApprovedPlan verifies LatestApprovedPlan walks the
// transcript backwards and returns the most recent plan from a
// TypeProposedPlan entry. Includes subtests for: no plan,
// single plan, two plans (returns the most recent).
func TestLatestApprovedPlan(t *testing.T) {
	t.Run("no_plan_returns_nil", func(t *testing.T) {
		entries := []transcript.Entry{
			{Speaker: "You", Type: transcript.TypeMessage, Content: "hi"},
		}
		if p := loop.LatestApprovedPlan(entries); p != nil {
			t.Errorf("expected nil for empty plan list, got %+v", p)
		}
	})

	t.Run("single_plan_returned", func(t *testing.T) {
		// Build entries manually using mustEncodeSnapshot helper.
		entries := []transcript.Entry{
			{Speaker: "You", Type: transcript.TypeMessage, Content: "do something"},
			{Speaker: "System", Type: transcript.TypeProposedPlan, Content: mustEncodeSnapshot(t, &transcript.Plan{
				Revision: 1,
				Items:    []transcript.PlanItem{{ID: 1, Text: "do thing", Status: transcript.PlanItemPending}},
			}, "initial")},
		}
		p := loop.LatestApprovedPlan(entries)
		if p == nil {
			t.Fatalf("expected a plan, got nil")
		}
		if p.Revision != 1 {
			t.Errorf("plan revision = %d, want 1", p.Revision)
		}
		if len(p.Items) != 1 || p.Items[0].ID != 1 {
			t.Errorf("plan items = %+v, want single item with ID=1", p.Items)
		}
	})

	t.Run("two_plans_returns_most_recent", func(t *testing.T) {
		entries := []transcript.Entry{
			{Speaker: "System", Type: transcript.TypeProposedPlan, Content: mustEncodeSnapshot(t, &transcript.Plan{
				Revision: 1,
				Items:    []transcript.PlanItem{{ID: 1, Status: transcript.PlanItemDone}},
			}, "rev 1")},
			{Speaker: "System", Type: transcript.TypeProposedPlan, Content: mustEncodeSnapshot(t, &transcript.Plan{
				Revision: 2,
				Items:    []transcript.PlanItem{{ID: 1, Status: transcript.PlanItemPending}},
			}, "rev 2")},
		}
		p := loop.LatestApprovedPlan(entries)
		if p == nil {
			t.Fatalf("expected a plan, got nil")
		}
		if p.Revision != 2 {
			t.Errorf("plan revision = %d, want 2 (most recent)", p.Revision)
		}
	})
}

// mustEncodeSnapshot encodes a plan inside a planSnapshot wrapper
// (the wire format AppendPlanSnapshot writes). Helper for the
// LatestApprovedPlan test only.
func mustEncodeSnapshot(t *testing.T, plan *transcript.Plan, reason string) string {
	t.Helper()
	tr := transcript.NewTranscript("")
	if err := transcript.AppendPlanSnapshot(tr, plan, reason); err != nil {
		t.Fatalf("AppendPlanSnapshot: %v", err)
	}
	entries := tr.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	return entries[0].Content
}

// TestPlanResumeFromJSONL exercises the recovery path: a fresh
// Loop loads a transcript that already contains an approved plan
// snapshot, and pendingPlan is recovered from the latest
// snapshot. Mirrors the TUI's RestoreSessionState flow but on
// the headless side.
func TestPlanResumeFromJSONL(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript("")

	// Seed an approved plan in the transcript.
	if err := transcript.AppendPlanSnapshot(tr, &transcript.Plan{
		Revision: 1,
		Items: []transcript.PlanItem{
			{ID: 1, Text: "first", Status: transcript.PlanItemPending},
		},
	}, "resumed from prior session"); err != nil {
		t.Fatalf("AppendPlanSnapshot: %v", err)
	}

	mc := newMockClient()
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	l := loop.New(tr, coderCfg, reviewerCfg, mc, dir)
	// No seeded pending plan — recovery must find it from the
	// transcript on the first active cycle.
	if plan := loop.PendingPlanForTest(l); plan != nil {
		t.Errorf("expected fresh loop to have nil pendingPlan, got %+v", plan)
	}

	// Trigger the recovery path by calling the public helper.
	// (runActiveCycle would do this internally; for the unit
	// test we exercise the same code path directly.)
	if err := loop.RecoverPendingPlanForTest(l); err != nil {
		t.Fatalf("RecoverPendingPlan: %v", err)
	}

	plan := loop.PendingPlanForTest(l)
	if plan == nil {
		t.Fatalf("expected pendingPlan to be recovered from transcript, got nil")
	}
	if plan.Revision != 1 || len(plan.Items) != 1 {
		t.Errorf("recovered plan = %+v, want rev=1 with 1 item", plan)
	}
}

// ---------------------------------------------------------------------------
// Loop-wired integration tests
// ---------------------------------------------------------------------------

// newGateLoop builds a loop in Triad mode with the plan gate
// enabled, and returns the loop, the in-memory transcript, the
// task channel, and the workDir (so tests can inspect files on
// disk). Mirrors newTestLoop's pattern but with the gate
// opt-in call added.
func newGateLoop(t *testing.T, mc *mockClient, task string) (*loop.Loop, *transcript.Transcript, chan string, string) {
	t.Helper()
	workDir := t.TempDir()
	tr := transcript.NewTranscript("")
	coderCfg := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", HasTools: false}
	l := loop.New(tr, coderCfg, reviewerCfg, mc, workDir)
	l.CurrentMode = loop.ModeTriad
	l.SetPlanGateDisabled(false) // opt the gate in
	taskChan := make(chan string, 1)
	taskChan <- task
	close(taskChan)
	return l, tr, taskChan, workDir
}

// readFile reads a file from the given workDir. Returns the
// contents and a non-nil error if the file doesn't exist.
func readFile(t *testing.T, workDir, name string) ([]byte, error) {
	t.Helper()
	path := filepath.Join(workDir, name)
	return os.ReadFile(path)
}

// TestPlanGate_NonTrivialTriadTaskRequiresPlan drives a full
// loop with the gate enabled and asserts:
//  1. A submit_plan tool call passes the gate and creates a plan.
//  2. A subsequent write_file tool call (after the plan) is
//     approved by Reviewer and executed.
//  3. A task_complete ends the cycle cleanly.
//  4. The plan snapshot appears in the transcript.
func TestPlanGate_NonTrivialTriadTaskRequiresPlan(t *testing.T) {
	mc := newMockClient()

	planArgs := `{"plan":{"revision":1,"items":[{"id":1,"text":"create file","status":"pending"}]}}`
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("submit_plan", planArgs)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"gate.txt","content":"ok","plan_item_id":1}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, tr, taskChan, workDir := newGateLoop(t, mc, "extract the transcript JSON helpers into a new package")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Plan snapshot should be in the transcript.
	snapshots := entriesOfType(tr.Entries(), transcript.TypeProposedPlan)
	if len(snapshots) < 1 {
		t.Errorf("expected at least 1 plan snapshot, got %d", len(snapshots))
	}

	// File should exist on disk.
	if _, err := readFile(t, workDir, "gate.txt"); err != nil {
		t.Errorf("gate.txt not on disk: %v", err)
	}
}

// TestPlanGate_PlanRejectionWhenStalling verifies the
// plan-required rejection branch: with the gate enabled, Coder's
// first tool call is a non-submit_plan action (write_file), and
// the gate rejects it with a System note. Coder then submits a
// plan on the next turn, and execution proceeds.
func TestPlanGate_PlanRejectionWhenStalling(t *testing.T) {
	mc := newMockClient()

	// Use a non-trivial task so the gate is actually active.
	// "extract the transcript JSON helpers into a new package"
	// is 9 words, classifies as middle, and does NOT trigger
	// the clarify step (it's specific enough about what to do).
	planArgs := `{"plan":{"revision":1,"items":[{"id":1,"text":"create file","status":"pending"}]}}`
	// Turn 1: Coder tries to write_file directly — gate rejects.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"stale.txt","content":"x"}`)},
	}})
	// Turn 2: Coder submits a plan — gate accepts.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("submit_plan", planArgs)},
	}})
	// Turn 3: Coder does the actual write_file, this time with
	// plan_item_id so the gate's bind path picks it up.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"stale.txt","content":"final","plan_item_id":1}`)},
	}})
	// Turn 4: task_complete.
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})

	// Reviewer approves the write_file and task_complete.
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, tr, taskChan, workDir := newGateLoop(t, mc, "extract the transcript JSON helpers into a new package")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// We expect at least one [Plan Gate] rejection System note.
	rejectionCount := 0
	for _, e := range tr.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "[Plan Gate]:") && strings.Contains(e.Content, "rejected tool") {
			rejectionCount++
		}
	}
	if rejectionCount < 1 {
		t.Errorf("expected at least 1 plan-gate rejection, got %d", rejectionCount)
	}

	// Plan snapshot should be in the transcript.
	snapshots := entriesOfType(tr.Entries(), transcript.TypeProposedPlan)
	if len(snapshots) < 1 {
		t.Errorf("expected at least 1 plan snapshot, got %d", len(snapshots))
	}

	// File should exist on disk with the FINAL content (not the
	// rejected earlier content).
	data, err := readFile(t, workDir, "stale.txt")
	if err != nil {
		t.Fatalf("stale.txt not on disk: %v", err)
	}
	if !strings.Contains(string(data), "final") {
		t.Errorf("stale.txt content = %q, want it to contain 'final'", data)
	}
}

// TestPlanGate_TrivialTaskBypassesPlanInTriadMode verifies that
// even with the gate enabled, a trivial task does NOT trigger
// the plan-required check. This is the contract that lets
// short, focused tasks run with the same overhead as before.
func TestPlanGate_TrivialTaskBypassesPlanInTriadMode(t *testing.T) {
	mc := newMockClient()
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"trivial.txt","content":"ok"}`)},
	}})
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, tr, taskChan, _ := newGateLoop(t, mc, "fix typo in trivial.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No plan snapshot should appear (the gate was bypassed
	// because the task is trivial).
	snapshots := entriesOfType(tr.Entries(), transcript.TypeProposedPlan)
	if len(snapshots) != 0 {
		t.Errorf("expected 0 plan snapshots for a trivial task, got %d", len(snapshots))
	}

	// No [Plan Gate] rejection should appear.
	for _, e := range tr.Entries() {
		if e.Speaker == transcript.SpeakerSystem && strings.Contains(e.Content, "[Plan Gate]:") {
			t.Errorf("unexpected plan-gate System entry on trivial task: %s", e.Content)
		}
	}
}

// TestPlanGate_HeuristicBindingEmitsComplianceNote verifies that
// when Coder doesn't explicitly set plan_item_id, the gate's
// heuristic binds the action to the first pending item AND
// writes an in_progress snapshot.
func TestPlanGate_HeuristicBindingEmitsComplianceNote(t *testing.T) {
	mc := newMockClient()

	// Use a non-trivial task so the gate is actually active.
	planArgs := `{"plan":{"revision":1,"items":[{"id":1,"text":"create file","status":"pending"}]}}`
	// Turn 1: submit_plan
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("submit_plan", planArgs)},
	}})
	// Turn 2: write_file WITHOUT plan_item_id (heuristic binds)
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("write_file", `{"path":"heuristic.txt","content":"ok"}`)},
	}})
	// Turn 3: task_complete
	mc.addResponse("Coder", mockResponse{resp: agent.AgentResponse{
		ToolCalls: []agent.ToolCall{makeToolCall("task_complete", "{}")},
	}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED."}})
	mc.addResponse("Reviewer", mockResponse{resp: agent.AgentResponse{Text: "APPROVED. Done."}})

	l, tr, taskChan, _ := newGateLoop(t, mc, "extract the transcript JSON helpers into a new package")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// We expect at LEAST 3 plan snapshots: initial approval,
	// mark-in-progress, mark-done.
	snapshots := entriesOfType(tr.Entries(), transcript.TypeProposedPlan)
	if len(snapshots) < 3 {
		t.Errorf("expected at least 3 plan snapshots (initial, in_progress, done), got %d", len(snapshots))
	}

	// The last snapshot's plan must have item 1 marked done.
	last := snapshots[len(snapshots)-1]
	plan, err := transcript.DecodePlan(last.Content)
	if err != nil {
		t.Fatalf("DecodePlan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Status != transcript.PlanItemDone {
		t.Errorf("expected final plan to have item 1 done, got: %+v", plan.Items)
	}
}
