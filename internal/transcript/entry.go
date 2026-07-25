package transcript

import "time"

// Speaker types
const (
	SpeakerYou      = "You"
	SpeakerCoder    = "Coder"
	SpeakerReviewer = "Reviewer"
	SpeakerSystem   = "System"

	// SpeakerSubagent is the speaker value used for entries that come from
	// a spawned subagent. The subagent's own transcript uses the same
	// SpeakerYou/SpeakerSubagent/SpeakerSystem identifiers; entries
	// appearing in the MAIN transcript with Speaker="Subagent" (or
	// "Subagent:<id>") represent the summary that the parent loop
	// bubbled up after the subagent finished. See docs/work2.md §3.
	SpeakerSubagent = "Subagent"
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
)

// Entry represents a single turn or action in the shared transcript.
type Entry struct {
	ID        int       `json:"id"`
	Speaker   string    `json:"speaker"`   // "You" | "Coder" | "Reviewer" | "System"
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
