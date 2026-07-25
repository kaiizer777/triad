package learn

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/transcript"
)

// ExtractLearnings scans transcript entries for resolved Reviewer objections
// and human mid-task corrections, returning candidate learnings with deterministic IDs.
func ExtractLearnings(entries []transcript.Entry) []Item {
	var items []Item
	if len(entries) == 0 {
		return items
	}

	// 1. Scan for resolved Reviewer objections
	// Pattern: Coder proposal -> Reviewer objection -> Coder revision -> Reviewer approval
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e.Speaker == transcript.SpeakerReviewer && isObjection(e.Content) {
			// Find preceding Coder proposal (before context)
			var beforeProposal string
			for j := i - 1; j >= 0; j-- {
				if entries[j].Speaker == transcript.SpeakerCoder {
					beforeProposal = entries[j].Content
					break
				}
			}

			// Search forward for Coder revision and subsequent Reviewer approval
			var afterProposal string
			var approvedTime time.Time
			foundApproval := false

			for k := i + 1; k < len(entries); k++ {
				next := entries[k]
				if next.Speaker == transcript.SpeakerCoder && (next.Type == transcript.TypeProposedAction || next.Type == transcript.TypeMessage) {
					afterProposal = next.Content
				}
				if next.Speaker == transcript.SpeakerReviewer && isApproval(next.Content) {
					approvedTime = next.Timestamp
					foundApproval = true
					break
				}
				// If a new task or new objection starts before approval, break
				if next.Speaker == transcript.SpeakerYou {
					break
				}
			}

			if foundApproval && beforeProposal != "" {
				objText := cleanText(e.Content)
				summary := fmt.Sprintf("Reviewer objection resolved: %s", truncateText(objText, 120))
				id := generateID("obj", e.ID, objText)
				itemTimestamp := e.Timestamp
				if itemTimestamp.IsZero() {
					itemTimestamp = time.Now()
				}
				if approvedTime.IsZero() {
					approvedTime = itemTimestamp
				}

				items = append(items, Item{
					ID:        id,
					Type:      TypeReviewerObjection,
					Summary:   summary,
					Before:    beforeProposal,
					After:     afterProposal,
					Context:   fmt.Sprintf("Objection: %s", objText),
					Timestamp: approvedTime,
					Status:    StatusUnreviewed,
				})
			}
		}
	}

	// 2. Scan for human mid-task corrections
	// Pattern: Coder turn -> Human message (SpeakerYou) mid-task -> Coder adaptation
	for i := 1; i < len(entries); i++ {
		e := entries[i]
		if e.Speaker == transcript.SpeakerYou {
			// Check if this human message occurred mid-task (after a Coder proposal or message)
			hasPrecedingCoder := false
			var beforeContext string
			for j := i - 1; j >= 0; j-- {
				if entries[j].Speaker == transcript.SpeakerCoder {
					hasPrecedingCoder = true
					beforeContext = entries[j].Content
					break
				}
				if entries[j].Speaker == transcript.SpeakerYou {
					// Preceding message was also human, not a mid-task interjection after Coder
					break
				}
			}

			// Check if Coder followed up after the human message
			var afterContext string
			hasFollowingCoder := false
			for k := i + 1; k < len(entries); k++ {
				if entries[k].Speaker == transcript.SpeakerCoder {
					hasFollowingCoder = true
					afterContext = entries[k].Content
					break
				}
			}

			if hasPrecedingCoder && hasFollowingCoder {
				correctionText := cleanText(e.Content)
				summary := fmt.Sprintf("Human mid-task correction: %s", truncateText(correctionText, 120))
				id := generateID("hum", e.ID, correctionText)
				itemTimestamp := e.Timestamp
				if itemTimestamp.IsZero() {
					itemTimestamp = time.Now()
				}

				items = append(items, Item{
					ID:        id,
					Type:      TypeHumanCorrection,
					Summary:   summary,
					Before:    beforeContext,
					After:     afterContext,
					Context:   fmt.Sprintf("Correction: %s", correctionText),
					Timestamp: itemTimestamp,
					Status:    StatusUnreviewed,
				})
			}
		}
	}

	return items
}

func isObjection(content string) bool {
	upper := strings.ToUpper(strings.TrimSpace(content))
	return strings.HasPrefix(upper, "OBJECTION:") || strings.Contains(upper, "VETO") || strings.Contains(upper, "REJECT")
}

func isApproval(content string) bool {
	upper := strings.ToUpper(strings.TrimSpace(content))
	return strings.HasPrefix(upper, "APPROVED")
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func generateID(prefix string, entryID int, content string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", entryID, content)))
	return fmt.Sprintf("ext-%s-%x", prefix, h[:4])
}
