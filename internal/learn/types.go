package learn

import "time"

// ItemType defines the source pattern of an extracted learning.
type ItemType string

const (
	// TypeReviewerObjection represents a Coder proposal that was objected to by Reviewer and subsequently revised/resolved.
	TypeReviewerObjection ItemType = "reviewer_objection"
	// TypeHumanCorrection represents a human interjection mid-task that redirected Coder.
	TypeHumanCorrection ItemType = "human_correction"
)

// Status defines the review lifecycle state of an extracted candidate learning.
type Status string

const (
	// StatusUnreviewed indicates the item is logged in the daily log but not yet reviewed via /learn.
	StatusUnreviewed Status = "unreviewed"
	// StatusPromoted indicates the item was promoted by the human to a curated topic file.
	StatusPromoted Status = "promoted"
	// StatusDismissed indicates the human declined to promote the item (retained in daily log only).
	StatusDismissed Status = "dismissed"
)

// Item represents a candidate learning extracted from transcript entries.
type Item struct {
	ID        string    `json:"id"`
	Type      ItemType  `json:"type"`
	Summary   string    `json:"summary"`
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Context   string    `json:"context"`
	Timestamp time.Time `json:"timestamp"`
	Status    Status    `json:"status"`
}
