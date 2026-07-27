package loop

// plan_gate.go
//
// Phase 6 plan-first gate — the headless side. The TUI has its
// own always-on gate in update.go; this file mirrors the gate's
// headless implementation so the two paths share the same wire
// format (TypeProposedPlan entries in the transcript) and the same
// classification logic. The TUI's gate is the production path;
// this headless gate exists for tests and for the
// "headless loop with the gate on" use case (work.md §6 — a
// pure-loop mode that never touches a TUI).
//
// Why a separate file: the gate is a self-contained subsystem
// (helpers + state-machine logic) and putting it in plan_gate.go
// makes Phase 6.3 / 6.4 / 6.5 easier to review as a single unit
// and easier to delete wholesale if a future refactor changes the
// architecture.
//
// The gate is opt-in for the headless loop: the default value of
// Loop.planGateDisabled is true, and tests that exercise the gate
// call SetPlanGateDisabled(false) to enable it. The TUI does not
// read Loop.planGateDisabled — it runs its own always-on gate.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ---------------------------------------------------------------------------
// Public helpers
// ---------------------------------------------------------------------------

// PlanRequiredForTask returns true when a task of the given string,
// running under the given mode, requires an approved plan before
// Coder can take real actions. Trivial tasks never need a plan — the
// reviewer overhead is pure waste. Critical and middle tasks always
// need a plan, except in ModeGeneral where the gate is a no-op
// regardless of tier (single-agent path, no Reviewer, no plan
// approval). The plan_first gate is the Triad/Ochestrator-only
// safety feature.
//
// The mode parameter is the loop's effective mode for the cycle
// (set by Orchestrator routing or directly from CurrentMode), NOT
// CurrentMode. This mirrors the runActiveCycle split — the gate
// honours whatever the orchestrator routed to, not the
// session-level default.
func PlanRequiredForTask(mode Mode, task string) bool {
	switch mode {
	case ModeGeneral:
		// General Chat is a single-agent path with no Reviewer and
		// no approval loop. Plans are not enforced here. (The TUI
		// also bypasses the gate in General mode for the same
		// reason.)
		return false
	}

	// Triad / Orchestrator (post-routing) — gate is on. Classify
	// the task: trivial → no plan needed; middle/critical → plan
	// required.
	tier, _ := ClassifyTask(task)
	return tier != TierTrivial
}

// extractPlanFromToolCall decodes a submit_plan tool call's JSON
// arguments into a *transcript.Plan. The expected shape is:
//
//	{"plan": {<Plan object>}}
//
// or sometimes Coder just passes the bare Plan. The function
// accepts both shapes and returns the resulting Plan. The "revision"
// argument is the revision number to assign to the new plan (1 for
// the first attempt, bumped on rejection-revise cycles by the
// caller).
func extractPlanFromToolCall(tc agent.ToolCall, revision int) (*transcript.Plan, error) {
	if tc.Function.Arguments == "" || tc.Function.Arguments == "{}" {
		return nil, fmt.Errorf("extractPlanFromToolCall: empty arguments")
	}

	// Preferred shape: {"plan": {<Plan>}}
	var wrapped struct {
		Plan *transcript.Plan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &wrapped); err == nil && wrapped.Plan != nil {
		wrapped.Plan.Revision = revision
		return wrapped.Plan, nil
	}

	// Fallback shape: bare Plan object.
	var bare transcript.Plan
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &bare); err == nil && len(bare.Items) > 0 {
		bare.Revision = revision
		return &bare, nil
	}

	return nil, fmt.Errorf("extractPlanFromToolCall: arguments are neither {plan: {...}} nor a bare Plan object: %s", tc.Function.Arguments)
}

// extractPlanItemID inspects a tool call's arguments for a
// "plan_item_id" / "item_id" field, and returns the integer ID
// along with a "found" boolean. Coder is encouraged to bind its
// actions to specific plan items by setting this field; the gate
// uses it to mark items in_progress / done as the cycle advances.
//
// Returns (id=0, found=false) if no ID was specified. Callers
// (e.g. heuristicBindPlanItem) should fall back to a heuristic
// choice in that case.
func extractPlanItemID(tc agent.ToolCall, plan *transcript.Plan) (int, bool) {
	if tc.Function.Arguments == "" {
		return 0, false
	}

	var args struct {
		PlanItemID *int   `json:"plan_item_id"`
		ItemID     *int   `json:"item_id"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return 0, false
	}

	if args.PlanItemID != nil {
		return *args.PlanItemID, true
	}
	if args.ItemID != nil {
		return *args.ItemID, true
	}
	return 0, false
}

// heuristicBindPlanItem picks a plan item for a tool call that
// didn't explicitly bind itself to one. The heuristic is simple
// but covers the common cases:
//
//  1. If the plan has any item still pending, return the first
//     pending one (in declaration order).
//  2. If all items are done, return (0, false) — the action has
//     nothing to bind to.
//
// The returned bool is true when an ID was selected; false
// otherwise. The plan is NOT mutated by this function — marking
// the item in_progress is the caller's job (via
// markPlanItemInProgress). This split is intentional: the gate
// wants to mark a plan item ONLY when the corresponding action
// is actually approved, and ONLY when execution succeeds. A
// "candidates" helper that doesn't mutate is easier to reason
// about than a "select and mark" helper that could leave the
// plan in an inconsistent state.
func heuristicBindPlanItem(plan *transcript.Plan) (int, bool) {
	if plan == nil {
		return 0, false
	}
	for _, item := range plan.Items {
		if item.Status == transcript.PlanItemPending {
			return item.ID, true
		}
	}
	return 0, false
}

// writePlanSnapshot appends a TypeProposedPlan entry to the
// loop's transcript, encoding the plan alongside a short reason.
// Thin wrapper around transcript.AppendPlanSnapshot that passes
// the loop's transcript in for the caller. Returns the same
// error AppendPlanSnapshot would.
func (l *Loop) writePlanSnapshot(plan *transcript.Plan, reason string) error {
	return transcript.AppendPlanSnapshot(l.transcript, plan, reason)
}

// LatestApprovedPlan walks a transcript backwards looking for the
// most recent TypeProposedPlan entry and returns the Plan it
// encoded. Returns nil when no plan has been approved yet (the
// usual case for fresh sessions, or sessions that never opened
// the gate). Exposed as a package function (not a Loop method)
// because it operates on a plain []transcript.Entry, not on the
// loop's own transcript — this lets the TUI and any other
// caller reconstruct plan state from an arbitrary transcript
// snapshot.
//
// The function accepts both the bare Plan encoding and the
// planSnapshot wrapper written by AppendPlanSnapshot — same
// shape as transcript.DecodePlan.
func LatestApprovedPlan(entries []transcript.Entry) *transcript.Plan {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeProposedPlan {
			p, err := transcript.DecodePlan(entries[i].Content)
			if err == nil {
				return p
			}
		}
	}
	return nil
}

// markPlanItemInProgress flips the status of the plan item with
// the given ID to "in_progress" and writes a plan snapshot so the
// transcript reflects the state change. The change is in-place on
// the *transcript.Plan the loop is currently holding (pendingPlan),
// which is the same pointer the gate's "is this item done?" check
// reads. Returns the new revision (always 1 for now — a future
// change might bump the revision on every status change for a
// stricter audit trail).
func (l *Loop) markPlanItemInProgress(id int) error {
	if l.pendingPlan == nil {
		return fmt.Errorf("markPlanItemInProgress: no pending plan")
	}
	updated := false
	for i := range l.pendingPlan.Items {
		if l.pendingPlan.Items[i].ID == id {
			l.pendingPlan.Items[i].Status = transcript.PlanItemInProgress
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("markPlanItemInProgress: no plan item with ID %d", id)
	}
	return l.writePlanSnapshot(l.pendingPlan, fmt.Sprintf("item %d in progress", id))
}

// markPlanItemDone flips the status of the plan item with the
// given ID to "done" and writes a plan snapshot. Same lifecycle
// as markPlanItemInProgress (in-place mutation + snapshot write).
func (l *Loop) markPlanItemDone(id int) error {
	if l.pendingPlan == nil {
		return fmt.Errorf("markPlanItemDone: no pending plan")
	}
	updated := false
	for i := range l.pendingPlan.Items {
		if l.pendingPlan.Items[i].ID == id {
			l.pendingPlan.Items[i].Status = transcript.PlanItemDone
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("markPlanItemDone: no plan item with ID %d", id)
	}
	return l.writePlanSnapshot(l.pendingPlan, fmt.Sprintf("item %d done", id))
}

// ---------------------------------------------------------------------------
// Per-cycle gate state
// ---------------------------------------------------------------------------

// resetPlanGateForCycle prepares the per-cycle gate state at the
// start of an active cycle. It:
//
//  1. Recovers pendingPlan from the transcript if pendingPlan is nil
//     (e.g. a resumed session that already had a plan in flight).
//  2. Decides planRequired for this cycle's task using
//     PlanRequiredForTask.
//  3. Sets planBypassed when the gate is disabled OR the task
//     doesn't need a plan (trivial in any non-General mode).
//  4. Resets planPreTextCount.
//
// Called once at the top of runActiveCycle.
func (l *Loop) resetPlanGateForCycle() {
	// 1. Recover pending plan from transcript if we don't have one
	// in memory. The TUI relies on this too (RestoreSessionState
	// in model.go) — a resumed session reads the latest plan
	// snapshot back into pendingPlan.
	if l.pendingPlan == nil {
		l.pendingPlan = LatestApprovedPlan(l.transcript.Entries())
	}

	// 2. Decide plan-required for this task.
	task := l.activeCycleTask
	if task == "" {
		task = l.mostRecentHumanTask()
	}
	required := PlanRequiredForTask(l.effectiveMode, task)

	// 3. Bypass the gate if it's disabled, or if the task doesn't
	// need a plan. planBypassed is the single source of truth for
	// "the gate will not block tool calls this cycle" — every
	// downstream check (in runActiveCycle) reads this field.
	l.planBypassed = l.planGateDisabled || !required

	// 4. Reset pre-text counter.
	l.planPreTextCount = 0
}

// planGateRejectsNonPlanCall returns true when the gate should
// reject a non-submit_plan tool call. Encapsulates the four
// preconditions the gate checks so the call site in
// runActiveCycle stays readable:
//
//  1. The gate is enabled (not bypassed for any reason).
//  2. The current Coder tool call is NOT submit_plan itself
//     (submit_plan is the gate's release valve, never rejected).
//  3. There is no plan pending yet for this cycle.
//  4. The tool call is a "real" action (not task_complete, which
//     is the cycle's terminal signal — no plan required to say
//     "I'm done").
//
// When all four are true, the gate rejects the call. The caller is
// expected to emit a System note explaining the rejection and let
// Coder have another turn.
func (l *Loop) planGateRejectsNonPlanCall(tc agent.ToolCall) bool {
	if l.planBypassed {
		return false
	}
	if l.pendingPlan != nil {
		// Plan already approved — the gate has done its job this
		// cycle. Subsequent actions are gated by the
		// bindActionToPlanItem check, not by the "have you
		// submitted a plan yet?" check.
		return false
	}
	if tc.Function.Name == "submit_plan" {
		return false
	}
	if tc.Function.Name == "task_complete" {
		// Ending the cycle doesn't need a plan. Coder can call
		// task_complete even before planning — the gate is about
		// *actions*, not about *completion*.
		return false
	}
	return true
}

// recordPrePlanTextMessage bumps the pre-plan-text counter and
// returns true when the gate should trip a stall guard (the
// human/Coder pair has spent too long "thinking" without
// committing to a plan). Returns false on the first call, true on
// the second — MaxPlanPreTextMessages=1 means "one plain-text
// turn is fine, two in a row is the trip".
//
// Stall-trip System note is the caller's responsibility; this
// helper just returns the boolean.
func (l *Loop) recordPrePlanTextMessage() bool {
	l.planPreTextCount++
	return l.planPreTextCount > MaxPlanPreTextMessages
}

// bindActionToPlanItem is the gate's per-action bookkeeping hook.
// It looks for an explicit plan_item_id on the tool call, falls
// back to the first pending item (heuristicBindPlanItem), and
// marks the chosen item in_progress in the plan. Returns the
// bound item ID, or 0 when the action could not be bound (e.g.
// the plan is fully done, or the plan is nil).
//
// Called after a tool call has been approved by Reviewer, before
// execution. If execution later fails, the caller is responsible
// for not marking the item done — the gate's contract is
// in_progress on approval, done on successful execution.
func (l *Loop) bindActionToPlanItem(tc agent.ToolCall) (int, error) {
	if l.pendingPlan == nil {
		return 0, nil
	}

	id, ok := extractPlanItemID(tc, l.pendingPlan)
	if !ok {
		id, ok = heuristicBindPlanItem(l.pendingPlan)
		if !ok {
			return 0, nil
		}
	}

	if err := l.markPlanItemInProgress(id); err != nil {
		return id, err
	}
	return id, nil
}

// formatPlanRejectionMessage renders the System note the loop
// emits when the gate rejects a tool call. Kept as a small
// helper so the wording is consistent across the headless loop
// and the TUI mirror.
func formatPlanRejectionMessage(tc agent.ToolCall) string {
	return fmt.Sprintf(
		"[Plan Gate]: A plan is required before non-trivial actions. "+
			"Call submit_plan first to propose a plan, then re-propose this action. "+
			"(rejected tool: %s)",
		strings.TrimSpace(tc.Function.Name),
	)
}

// formatPlanStallMessage renders the System note emitted when
// the pre-text stall guard trips — Coder has emitted too many
// plain-text "thinking" responses without committing to a plan.
func formatPlanStallMessage() string {
	return "[Plan Gate]: Multiple plain-text responses received without a plan. " +
		"Please call submit_plan to propose a plan before continuing."
}

// lastActionResultSucceeded returns true when the most recent
// action_result in the transcript is not an error. Used by the
// gate to decide whether to mark a plan item done (only on
// successful execution, not on tool failure). The heuristic is
// a content-prefix check: a "ERROR:" prefix on the
// action_result's content means the tool failed. The check is
// intentionally simple — anything more elaborate (e.g. parsing
// the result JSON) would be brittle given the wide range of
// tools that emit action_results.
func lastActionResultSucceeded(entries []transcript.Entry) bool {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeActionResult {
			content := strings.TrimSpace(entries[i].Content)
			return !strings.HasPrefix(content, "ERROR:")
		}
	}
	// No action_result found — treat as "no success signal", so
	// the gate does NOT mark the plan item done. This is the
	// conservative choice (a missing result is not the same as
	// a successful result).
	return false
}
