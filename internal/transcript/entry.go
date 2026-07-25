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
)

// Entry represents a single turn or action in the shared transcript.
type Entry struct {
	ID        int       `json:"id"`
	Speaker   string    `json:"speaker"`   // "You" | "Coder" | "Reviewer" | "System"
	Type      string    `json:"type"`      // "message" | "proposed_action" | "action_result"
	Content   string    `json:"content"`   // message text, diff, command, or execution output
	Timestamp time.Time `json:"timestamp"`
}
