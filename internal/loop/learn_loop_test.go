package loop_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/transcript"
)

func TestPhase9_AutoExtractAndLearnLoop(t *testing.T) {
	tempDir := t.TempDir()
	mem, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create memory manager: %v", err)
	}

	tr := transcript.NewTranscript(filepath.Join(tempDir, "session.jsonl"))

	now := time.Now()
	// Populate transcript with a resolved objection and a human mid-task correction
	_ = tr.Append(transcript.Entry{ID: 1, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Build logger", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 2, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"log.go\",\"content\":\"fmt.Println(msg)\"}", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 3, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "OBJECTION: Do not use fmt.Println directly, use structured logger.", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 4, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"log.go\",\"content\":\"logger.Info(msg)\"}", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 5, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Structured logger used.", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 6, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Wait, set log level to Debug by default", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 7, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "Tool: write_file\nArguments: {\"path\":\"log.go\",\"content\":\"logger.SetLevel(Debug)\"}", Timestamp: now})
	_ = tr.Append(transcript.Entry{ID: 8, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "APPROVED: Debug level configured.", Timestamp: now})

	l := loop.New(tr, agent.AgentConfig{}, agent.AgentConfig{}, nil, tempDir)
	l.SetMemory(mem)

	// Trigger auto extraction (simulating end of active cycle)
	items, err := l.AutoExtractLearnings()
	if err != nil {
		t.Fatalf("AutoExtractLearnings failed: %v", err)
	}

	// Requirement 9.2.1: Confirm both are correctly auto-extracted into that day's daily log
	if len(items) != 2 {
		t.Fatalf("expected 2 auto-extracted candidate items, got %d", len(items))
	}

	dailyLog, err := mem.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}
	if !strings.Contains(dailyLog, "[AUTO_EXTRACT_LEARNING]") {
		t.Fatalf("expected daily log to contain auto-extracted learnings, got:\n%s", dailyLog)
	}

	// Requirement 9.2.2: Confirm /learn surfaces only unreviewed items & dismiss retains daily log
	unreviewed := l.Learn.GetUnreviewedItems()
	if len(unreviewed) != 2 {
		t.Fatalf("expected 2 unreviewed items, got %d", len(unreviewed))
	}

	// Promote item 0 to "conventions"
	if err := l.Learn.Promote(items[0].ID, "conventions"); err != nil {
		t.Fatalf("failed to promote item: %v", err)
	}
	// Dismiss item 1
	if err := l.Learn.Dismiss(items[1].ID); err != nil {
		t.Fatalf("failed to dismiss item: %v", err)
	}

	// Verify unreviewed is now 0
	if len(l.Learn.GetUnreviewedItems()) != 0 {
		t.Errorf("expected 0 unreviewed items after promote/dismiss, got %d", len(l.Learn.GetUnreviewedItems()))
	}

	// Verify daily log still retains both items (append-only log is unchanged)
	dailyLogAfter, err := mem.ReadDailyLog(now)
	if err != nil || !strings.Contains(dailyLogAfter, items[0].ID) || !strings.Contains(dailyLogAfter, items[1].ID) {
		t.Errorf("expected daily log to retain both item IDs, got:\n%s", dailyLogAfter)
	}

	// Requirement 9.2.3: Invariant test — confirm auto-extraction pass NEVER writes to topics/*.md or INDEX.md
	indexBefore, _ := mem.LoadIndex()
	_, _ = l.AutoExtractLearnings()
	indexAfter, _ := mem.LoadIndex()

	if indexBefore != indexAfter {
		t.Errorf("INVARIANT VIOLATION: INDEX.md was mutated during auto-extraction pass!")
	}
}
