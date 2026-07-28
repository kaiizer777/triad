package learn_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/learn"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/transcript"
)

func TestExtractLearnings(t *testing.T) {
	now := time.Now()
	entries := []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Implement query builder", Timestamp: now},
		{ID: 2, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"db.go\",\"content\":\"fmt.Sprintf(\\\"SELECT * FROM users WHERE id = %s\\\", id)\"}", Timestamp: now},
		{ID: 3, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "OBJECTION: Direct SQL string formatting is vulnerable to injection.", Timestamp: now},
		{ID: 4, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"db.go\",\"content\":\"db.Query(\\\"SELECT * FROM users WHERE id = ?\\\", id)\"}", Timestamp: now},
		{ID: 5, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Parameterized query is safe.", Timestamp: now},
		{ID: 6, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Wait, also add a 30s context timeout to db.Query call", Timestamp: now},
		{ID: 7, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"db.go\",\"content\":\"ctx, cancel := context.WithTimeout(ctx, 30*time.Second)\"}", Timestamp: now},
	}

	items := learn.ExtractLearnings(entries)
	if len(items) != 2 {
		t.Fatalf("expected 2 extracted learnings, got %d", len(items))
	}

	// Verify Reviewer objection extraction
	if items[0].Type != learn.TypeReviewerObjection {
		t.Errorf("expected first item type to be %s, got %s", learn.TypeReviewerObjection, items[0].Type)
	}
	if !strings.Contains(items[0].Summary, "Reviewer objection resolved") {
		t.Errorf("expected objection summary, got %q", items[0].Summary)
	}

	// Verify Human correction extraction
	if items[1].Type != learn.TypeHumanCorrection {
		t.Errorf("expected second item type to be %s, got %s", learn.TypeHumanCorrection, items[1].Type)
	}
	if !strings.Contains(items[1].Summary, "Human mid-task correction") {
		t.Errorf("expected human correction summary, got %q", items[1].Summary)
	}
}

func TestAutoExtractAndLog(t *testing.T) {
	tempDir := t.TempDir()
	mem, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create memory manager: %v", err)
	}

	svc, err := learn.NewService(mem)
	if err != nil {
		t.Fatalf("failed to create learn service: %v", err)
	}

	now := time.Now()
	entries := []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Raw query", Timestamp: now},
		{ID: 2, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "OBJECTION: Unsafe query", Timestamp: now},
		{ID: 3, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Safe query", Timestamp: now},
		{ID: 4, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Looks good", Timestamp: now},
	}

	newItems, err := svc.AutoExtractAndLog(entries, now)
	if err != nil {
		t.Fatalf("AutoExtractAndLog failed: %v", err)
	}
	if len(newItems) != 1 {
		t.Fatalf("expected 1 new item extracted, got %d", len(newItems))
	}

	// Verify item was logged in daily log
	dailyContent, err := mem.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}
	if !strings.Contains(dailyContent, "[AUTO_EXTRACT_LEARNING]") {
		t.Errorf("expected daily log to contain auto extract tag, got: %s", dailyContent)
	}

	// Re-running AutoExtractAndLog on same transcript should yield 0 new items (idempotent)
	dupItems, err := svc.AutoExtractAndLog(entries, now)
	if err != nil {
		t.Fatalf("second AutoExtractAndLog failed: %v", err)
	}
	if len(dupItems) != 0 {
		t.Errorf("expected 0 new items on duplicate pass, got %d", len(dupItems))
	}
}

func TestPromoteAndDismiss(t *testing.T) {
	tempDir := t.TempDir()
	mem, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create memory manager: %v", err)
	}

	svc, err := learn.NewService(mem)
	if err != nil {
		t.Fatalf("failed to create learn service: %v", err)
	}

	now := time.Now()
	entries := []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Action A", Timestamp: now},
		{ID: 2, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "OBJECTION: Problem A", Timestamp: now},
		{ID: 3, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Action B", Timestamp: now},
		{ID: 4, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Solved A", Timestamp: now},
		{ID: 5, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Correction C", Timestamp: now},
		{ID: 6, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Action C", Timestamp: now},
	}

	items, err := svc.AutoExtractAndLog(entries, now)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected 2 extracted items, got %d (err: %v)", len(items), err)
	}

	unreviewed := svc.GetUnreviewedItems()
	if len(unreviewed) != 2 {
		t.Fatalf("expected 2 unreviewed items, got %d", len(unreviewed))
	}

	// Promote first item to "conventions"
	itemToPromote := items[0]
	if _, err := svc.Promote(itemToPromote.ID, "conventions"); err != nil {
		t.Fatalf("failed to promote item: %v", err)
	}

	// Dismiss second item
	itemToDismiss := items[1]
	if err := svc.Dismiss(itemToDismiss.ID); err != nil {
		t.Fatalf("failed to dismiss item: %v", err)
	}

	// Verify no unreviewed items remain
	if len(svc.GetUnreviewedItems()) != 0 {
		t.Errorf("expected 0 unreviewed items remaining, got %d", len(svc.GetUnreviewedItems()))
	}

	// Verify topic file contains promoted lesson
	topicContent, err := mem.LoadTopic("conventions")
	if err != nil {
		t.Fatalf("failed to load conventions topic: %v", err)
	}
	if !strings.Contains(topicContent, itemToPromote.Summary) {
		t.Errorf("expected topic content to contain promoted lesson, got:\n%s", topicContent)
	}

	// Verify daily log still contains both entries (daily log remains append-only and intact)
	dailyLog, err := mem.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}
	if !strings.Contains(dailyLog, itemToPromote.ID) || !strings.Contains(dailyLog, itemToDismiss.ID) {
		t.Errorf("expected daily log to retain both entries, got:\n%s", dailyLog)
	}
}

func TestNoAutoPromotionInvariant(t *testing.T) {
	tempDir := t.TempDir()
	mem, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create memory manager: %v", err)
	}

	// Record initial topic files and INDEX.md content
	indexBefore, err := mem.LoadIndex()
	if err != nil {
		t.Fatalf("failed to load initial index: %v", err)
	}

	svc, err := learn.NewService(mem)
	if err != nil {
		t.Fatalf("failed to create learn service: %v", err)
	}

	now := time.Now()
	entries := []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Proposal", Timestamp: now},
		{ID: 2, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "OBJECTION: Reject", Timestamp: now},
		{ID: 3, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Revised proposal", Timestamp: now},
		{ID: 4, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Accept", Timestamp: now},
	}

	// Run auto extraction
	_, err = svc.AutoExtractAndLog(entries, now)
	if err != nil {
		t.Fatalf("AutoExtractAndLog failed: %v", err)
	}

	// Invariant Check: Verify INDEX.md has NOT changed
	indexAfter, err := mem.LoadIndex()
	if err != nil {
		t.Fatalf("failed to load index after auto extract: %v", err)
	}
	if indexBefore != indexAfter {
		t.Errorf("INVARIANT VIOLATION: INDEX.md was modified during auto extraction!\nBefore:\n%s\nAfter:\n%s", indexBefore, indexAfter)
	}

	// Invariant Check: Verify topics directory files have NOT changed
	topicsDir := filepath.Join(mem.Dir(), "topics")
	entriesDir, _ := os.ReadDir(topicsDir)
	for _, entry := range entriesDir {
		content, _ := os.ReadFile(filepath.Join(topicsDir, entry.Name()))
		if strings.Contains(string(content), "[AUTO_EXTRACT_LEARNING]") || strings.Contains(string(content), "Objection") {
			t.Errorf("INVARIANT VIOLATION: topic file %s was modified during auto extraction!", entry.Name())
		}
	}
}
