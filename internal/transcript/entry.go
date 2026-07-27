package transcript

import (
	"encoding/json"
	"fmt"
	"time"
)

// Speaker types
const (
	SpeakerYou          = "You"
	SpeakerCoder        = "Coder"
	SpeakerReviewer     = "Reviewer"
	SpeakerSystem       = "System"
	SpeakerOrchestrator = "Orchestrator"
	SpeakerPartner      = "Partner"

	// SpeakerSubagent is the speaker value used for entries that come from
	// a spawned subagent. The subagent's own transcript uses the same
	// SpeakerYou/SpeakerSubagent/SpeakerSystem identifiers; entries
	// appearing in the MAIN transcript with Speaker="Subagent" (or
	// "Subagent:<id>") represent the summary that the parent loop
	// bubbled up after the subagent finished. See docs/work2.md §3.
	SpeakerSubagent = "Subagent"

	// SpeakerTwin is the speaker prefix used for twin subagent entries.
	// The twin pair's own isolated transcript uses "Twin:<id>" as the
	// speaker for mini-Coder messages (mini-Reviewer uses SpeakerReviewer
	// in that transcript). Entries appearing in the MAIN transcript with
	// Speaker="Twin:<id>" represent the single summary that the twin pair
	// returns to the Orchestrator after completing (or hitting the turn
	// cap). See work.md §Phase 6.
	SpeakerTwin = "Twin"
)

// Entry types
const (
	TypeMessage        = "message"
	TypeProposedAction = "proposed_action"
	TypeActionResult   = "action_result"
	// TypeRoutingDecision is emitted by Orchestrator mode for every routing
	// decision — auto-proceeded or human-confirmed. The Content field holds
	// a JSON-encoded RoutingDecision struct so callers can machine-read it.
	TypeRoutingDecision = "routing_decision"
	// TypeProposedPlan is emitted whenever the plan-first gate accepts,
	// rejects, or updates a proposed plan. The Content field holds a
	// JSON-encoded planSnapshot struct (Reason + Plan) so callers can both
	// machine-read the plan and display why the snapshot was written.
	TypeProposedPlan = "proposed_plan"
)

// Plan item status constants. A plan item moves through these in order as
// the corresponding action is proposed, in-flight, and completed.
const (
	PlanItemPending    = "pending"
	PlanItemInProgress = "in_progress"
	PlanItemDone       = "done"
)

// Entry represents a single turn or action in the shared transcript.
type Entry struct {
	ID        int       `json:"id"`
	Speaker   string    `json:"speaker"`   // "You" | "Coder" | "Reviewer" | "System" | "Orchestrator"
	Type      string    `json:"type"`      // "message" | "proposed_action" | "action_result" | "routing_decision"
	Content   string    `json:"content"`   // message text, diff, command, execution output, or JSON-encoded payload
	Timestamp time.Time `json:"timestamp"`
}

// RoutingDecision is the structured payload stored (as JSON) in Content for
// entries of Type TypeRoutingDecision. It records every orchestrator routing
// decision as a first-class, inspectable event — the traceability mitigation
// from Phase 0's research findings.
type RoutingDecision struct {
	Task            string `json:"task"`
	ComplexityJudge string `json:"complexity_judgment"` // "trivial" | "critical" | "middle"
	TargetMode      string `json:"target_mode"`         // "general" | "triad"
	AutoProceeded   bool   `json:"auto_proceeded"`      // true = auto, false = human confirmed
	Reason          string `json:"reason"`
}

// Plan is a structured list of items that the plan-first gate accepts
// before the loop will let Coder take real actions. A revision starts at 1
// and is bumped on every Plan-objection / Plan-revision cycle.
type Plan struct {
	Revision int        `json:"revision"`
	Items    []PlanItem `json:"items"`
}

// PlanItem is a single checklist entry inside a Plan. ID is the stable
// identifier Coder references when binding an action to a specific item —
// reordering or editing the Items slice does not change IDs.
type PlanItem struct {
	ID     int    `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"` // PlanItemPending | PlanItemInProgress | PlanItemDone
}

// planSnapshot is the wrapper written into the Entry.Content field for
// TypeProposedPlan entries. It pairs the encoded plan with a short human
// reason so the transcript explains why a snapshot was recorded ("initial
// approval", "rejected — Coder did not address objection", "item 2 done",
// etc.).
type planSnapshot struct {
	Reason string `json:"reason"`
	Plan   *Plan  `json:"plan"`
}

// EncodePlan returns the JSON encoding of p. Errors are returned verbatim
// from json.Marshal and should be treated as the plan being unencodable
// (only possible if a future field is non-marshalable).
func EncodePlan(p *Plan) (string, error) {
	if p == nil {
		return "", fmt.Errorf("EncodePlan: nil plan")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("EncodePlan: %w", err)
	}
	return string(data), nil
}

// DecodePlan parses a JSON-encoded Plan. It accepts both a bare Plan and
// a planSnapshot wrapper (the form written by AppendPlanSnapshot), so
// callers reading either the wire format or a snapshot entry use the same
// helper.
func DecodePlan(s string) (*Plan, error) {
	if s == "" {
		return nil, fmt.Errorf("DecodePlan: empty input")
	}
	// Try the bare Plan shape first.
	var p Plan
	if err := json.Unmarshal([]byte(s), &p); err == nil && p.Revision != 0 {
		return &p, nil
	}
	// Fall back to the planSnapshot wrapper.
	var snap planSnapshot
	if err := json.Unmarshal([]byte(s), &snap); err == nil && snap.Plan != nil {
		return snap.Plan, nil
	}
	// Last-chance: maybe the bare plan had Revision 0 — accept it.
	if err := json.Unmarshal([]byte(s), &p); err == nil {
		return &p, nil
	}
	return nil, fmt.Errorf("DecodePlan: input is neither a Plan nor a planSnapshot")
}

// AppendPlanSnapshot writes a TypeProposedPlan entry to t, encoding the
// plan alongside a short human-readable reason. The reason is meant for
// transcript readers ("initial approval", "item done", "revision #2"); the
// Plan is what the loop / TUI parses to recover state.
func AppendPlanSnapshot(t *Transcript, p *Plan, reason string) error {
	if t == nil {
		return fmt.Errorf("AppendPlanSnapshot: nil transcript")
	}
	if p == nil {
		return fmt.Errorf("AppendPlanSnapshot: nil plan")
	}
	snap := planSnapshot{Reason: reason, Plan: p}
	encoded, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("AppendPlanSnapshot: marshal: %w", err)
	}
	return t.Append(Entry{
		Speaker:   SpeakerSystem,
		Type:      TypeProposedPlan,
		Content:   string(encoded),
		Timestamp: time.Now(),
	})
}
