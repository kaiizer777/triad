package tui

// plan_gate.go
//
// Phase 6.4 plan-first gate — the TUI side. The headless loop
// has its own plan_gate.go with the same shape (the package
// helpers there are also used by the TUI: PlanRequiredForTask,
// LatestApprovedPlan, extractPlanFromToolCall,
// extractPlanItemID, heuristicBindPlanItem — see
// internal/loop/plan_gate.go). This file holds the TUI-only
// helpers: the per-Model state (currentPlan, planRequired,
// planBypassed, planPreTextCount, planBoundItemID) and the
// four Model methods that mutate that state.
//
// Why a separate file in the TUI: keeps the gate's TUI
// plumbing out of update.go (already 1000+ lines) and out of
// model.go (the constructor/state-restoration file). Easy to
// find, easy to review as a unit, easy to delete wholesale
// if a future refactor changes the architecture.
//
// Design notes:
//   - The TUI gate is always on. Unlike the headless loop
//     (which defaults planGateDisabled: true so 15+ test files
//     keep passing), the TUI is the production path and the
//     plan gate is the safety feature for real coding work. The
//     loop's `PlanRequiredForTask` already returns false for
//     trivial tasks, so most slash commands and "hello"-style
//     prompts pass through untouched.
//   - The TUI does NOT use a `*Loop` instance — it has its
//     own Model. So the gate's bookkeeping methods (resetPlanGateForCycle,
//     bindActionToPlanItem, markPlanItemStatusInPlace, etc.)
//     are Model methods that work on m.currentPlan and write
//     the plan-snapshot entry through m.transcript directly.
//   - Mutations are in place on m.currentPlan. The snapshot
//     is what survives a crash; the in-memory pointer is
//     what the gate's "is this item done?" check reads.

import (
	"fmt"
	"strings"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// resetPlanGateForCycle prepares the per-cycle plan-gate state on
// the TUI Model. It mirrors Loop.resetPlanGateForCycle so the
// headless and TUI paths agree on what "ready for a new cycle"
// means:
//
//  1. Recover the most-recently-approved plan from the transcript
//     if m.currentPlan is nil (e.g. on a fresh resume where the
//     restore path in RestoreSessionState ran before any plan
//     landed). The TUI restore already populates currentPlan; this
//     step is a defensive double-check that handles the case
//     where the cycle runs but no restore has happened yet.
//  2. Recompute planRequired for the current cycle's task using
//     loop.PlanRequiredForTask and the model's currentMode.
//  3. Set planBypassed when the gate is inactive for any reason
//     (general mode, trivial task). Same convention as the
//     headless loop: planBypassed is the single source of truth
//     for "this cycle's gate is off".
//  4. Reset planPreTextCount and planBoundItemID so the new
//     cycle starts from a known baseline.
//
// Called once at the top of the humanInputMsg active-cycle path,
// before firing the Coder turn.
func (m *Model) resetPlanGateForCycle() {
	// 1. Recover the latest approved plan if we don't have one
	// in memory. RestoreSessionState should have done this on
	// session start, but a cycle triggered after a `clearCycleState`
	// (e.g. after a task_complete that reset state) needs to
	// re-read from disk.
	if m.currentPlan == nil {
		m.currentPlan = loop.LatestApprovedPlan(m.transcript.Entries())
	}

	// 2. Decide plan-required for this cycle's task. We classify
	// the most recent You message — the same "fresh task" input
	// that triggered this cycle. This matches Loop's behaviour
	// (Loop uses activeCycleTask which it sets in the same path).
	task := m.mostRecentHumanTask()
	m.planRequired = loop.PlanRequiredForTask(m.currentMode, task)

	// 3. Bypass when the gate is off for this cycle.
	// currentMode == ModeGeneral is the headless loop's behaviour
	// and matches it here. Trivial tasks are also bypassed
	// (PlanRequiredForTask returns false for them).
	m.planBypassed = !m.planRequired

	// 4. Reset per-cycle counters.
	m.planPreTextCount = 0
	m.planBoundItemID = 0
}

// planGateRejectsNonPlanCall returns true when the TUI gate
// should reject a non-submit_plan tool call. Same four-condition
// shape as Loop.planGateRejectsNonPlanCall:
//
//  1. Gate is not bypassed (planRequired && !planBypassed).
//  2. No plan is pending yet this cycle (m.currentPlan == nil
//     OR the latest snapshot was on a different cycle).
//  3. The tool call is NOT submit_plan (the gate's release valve).
//  4. The tool call is NOT task_complete (the cycle's terminal
//     signal — no plan required to say "done").
//
// When all four hold, the gate rejects. The caller is expected
// to emit a System note and give Coder another turn (no retry
// counter is burned; the gate is a precondition, not a counter).
func (m *Model) planGateRejectsNonPlanCall(tc agent.ToolCall) bool {
	if m.planBypassed {
		return false
	}
	if m.currentPlan != nil {
		// A plan is already approved for this cycle. The
		// per-action binding check (below) handles subsequent
		// tool calls; the "have you submitted a plan yet?"
		// check only applies when no plan exists.
		return false
	}
	if tc.Function.Name == "submit_plan" {
		return false
	}
	if tc.Function.Name == "task_complete" {
		return false
	}
	return true
}

// recordPrePlanTextMessage bumps the pre-plan-text counter and
// returns true when the gate should trip a stall guard. Mirrors
// Loop.recordPrePlanTextMessage. The stall-trip System note is
// the caller's responsibility; this helper only reports the
// boolean.
//
// loop.MaxPlanPreTextMessages is the headless cap; the TUI uses
// the same value for consistency between the two paths. If they
// ever need to differ, the TUI can introduce its own constant —
// for now, sharing keeps "what does Coder see" uniform across
// headless and TUI modes.
func (m *Model) recordPrePlanTextMessage() bool {
	m.planPreTextCount++
	return m.planPreTextCount > loop.MaxPlanPreTextMessages
}

// bindActionToPlanItem is the TUI's per-action bookkeeping hook.
// It looks for an explicit plan_item_id on the tool call, falls
// back to the first pending item (loop.heuristicBindPlanItem),
// marks the chosen item in_progress in m.currentPlan, and writes
// a TypeProposedPlan snapshot so the transcript reflects the
// state change. Returns the bound item ID (0 when no binding
// happened — plan is nil, no pending items, etc.).
//
// Same contract as Loop.bindActionToPlanItem: in-place mutation
// on the plan the TUI is currently holding, plus a snapshot
// write. The caller stores the bound ID in m.planBoundItemID
// so the toolResultMsg handler can mark the item done on
// successful execution.
func (m *Model) bindActionToPlanItem(tc agent.ToolCall) (int, error) {
	if m.currentPlan == nil {
		return 0, nil
	}

	id, ok := loop.ExtractPlanItemIDPublic(tc, m.currentPlan)
	if !ok {
		id, ok = loop.HeuristicBindPlanItemPublic(m.currentPlan)
		if !ok {
			return 0, nil
		}
	}

	if err := m.markPlanItemStatusInPlace(id, transcript.PlanItemInProgress); err != nil {
		return id, err
	}
	return id, nil
}

// markPlanItemStatusInPlace flips the status of the plan item
// with the given ID in m.currentPlan and writes a TypeProposedPlan
// snapshot. The change is in-place on the same pointer
// m.currentPlan holds — every other gate check reads the same
// pointer, so an in-place mutation is immediately visible.
//
// Mirrors the loop's markPlanItemInProgress / markPlanItemDone
// pair but unified into a single TUI helper that takes the
// target status as an argument. The TUI is the only caller
// (the headless loop uses the two-arg form because it has
// a Loop instance and the two calls have meaningfully different
// "reason" text). For the TUI, the reason text is the same
// "item N <status>" shape and the bookkeeping is identical.
func (m *Model) markPlanItemStatusInPlace(id int, status string) error {
	if m.currentPlan == nil {
		return fmt.Errorf("markPlanItemStatusInPlace: no current plan")
	}
	updated := false
	for i := range m.currentPlan.Items {
		if m.currentPlan.Items[i].ID == id {
			m.currentPlan.Items[i].Status = status
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("markPlanItemStatusInPlace: no plan item with ID %d", id)
	}
	reason := fmt.Sprintf("item %d %s", id, status)
	return transcript.AppendPlanSnapshot(m.transcript, m.currentPlan, reason)
}

// lastActionResultSucceeded returns true when the most recent
// TypeActionResult entry in the transcript is not an error.
// Mirrors the headless loop's check. Used by the toolResultMsg
// handler to decide whether to mark the bound plan item done.
func (m *Model) lastActionResultSucceeded() bool {
	entries := m.transcript.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeActionResult {
			content := strings.TrimSpace(entries[i].Content)
			return !strings.HasPrefix(content, "ERROR:")
		}
	}
	// No action_result found — treat as "no success signal" so
	// the gate does NOT mark the plan item done.
	return false
}

// formatPlanRejectionMessage renders the System note the TUI
// emits when the gate rejects a non-submit_plan tool call.
// Same wording as the headless loop's helper so the two paths
// produce identical transcripts (handy when comparing a headless
// test fixture against a TUI session recording).
func formatPlanRejectionMessage(tc agent.ToolCall) string {
	return fmt.Sprintf(
		"[Plan Gate]: A plan is required before non-trivial actions. "+
			"Call submit_plan first to propose a plan, then re-propose this action. "+
			"(rejected tool: %s)",
		strings.TrimSpace(tc.Function.Name),
	)
}

// formatPlanStallMessage renders the System note the TUI emits
// when the pre-text stall guard trips — Coder has emitted too
// many plain-text "thinking" responses without committing to a
// plan. Same wording as the headless loop's helper.
func formatPlanStallMessage() string {
	return "[Plan Gate]: Multiple plain-text responses received without a plan. " +
		"Please call submit_plan to propose a plan before continuing."
}
