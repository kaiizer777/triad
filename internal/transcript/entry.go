package transcript

import "time"

// Speaker types
const (
	SpeakerYou      = "You"
	SpeakerCoder    = "Coder"
	SpeakerReviewer = "Reviewer"
	SpeakerSystem   = "System"
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
