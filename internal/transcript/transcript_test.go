package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTranscriptAppendAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.jsonl")

	tr := NewTranscript(sessionPath)

	now := time.Now().Truncate(time.Millisecond)

	entry1 := Entry{
		ID:        1,
		Speaker:   SpeakerYou,
		Type:      TypeMessage,
		Content:   "Add a Razorpay webhook handler.",
		Timestamp: now,
	}

	entry2 := Entry{
		ID:        2,
		Speaker:   SpeakerCoder,
		Type:      TypeProposedAction,
		Content:   "I will create handlers/razorpay_webhook.go",
		Timestamp: now.Add(time.Second),
	}

	if err := tr.Append(entry1); err != nil {
		t.Fatalf("Append entry1 failed: %v", err)
	}

	if err := tr.Append(entry2); err != nil {
		t.Fatalf("Append entry2 failed: %v", err)
	}

	if len(tr.Entries()) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(tr.Entries()))
	}

	loaded, err := LoadFromFile(sessionPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	loadedEntries := loaded.Entries()
	if len(loadedEntries) != 2 {
		t.Fatalf("Expected 2 loaded entries, got %d", len(loadedEntries))
	}

	if loadedEntries[0].Content != entry1.Content {
		t.Errorf("Expected entry 1 content %q, got %q", entry1.Content, loadedEntries[0].Content)
	}
	if loadedEntries[1].Content != entry2.Content {
		t.Errorf("Expected entry 2 content %q, got %q", entry2.Content, loadedEntries[1].Content)
	}
}

func TestTranscriptSaveToFile(t *testing.T) {
	tempDir := t.TempDir()
	savePath := filepath.Join(tempDir, "export.jsonl")

	tr := NewTranscript("")

	entry := Entry{
		ID:        1,
		Speaker:   SpeakerReviewer,
		Type:      TypeMessage,
		Content:   "Looks good to me.",
		Timestamp: time.Now(),
	}

	if err := tr.Append(entry); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if err := tr.SaveToFile(savePath); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	if _, err := os.Stat(savePath); os.IsNotExist(err) {
		t.Fatalf("Exported file does not exist at %s", savePath)
	}

	loaded, err := LoadFromFile(savePath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if len(loaded.Entries()) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(loaded.Entries()))
	}
}

func TestTranscriptClear(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.jsonl")

	tr := NewTranscript(sessionPath)
	if err := tr.Append(Entry{Speaker: SpeakerYou, Content: "Hello"}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if len(tr.Entries()) != 1 {
		t.Fatalf("Expected 1 entry before clear, got %d", len(tr.Entries()))
	}

	if err := tr.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if len(tr.Entries()) != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", len(tr.Entries()))
	}

	loaded, err := LoadFromFile(sessionPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed after clear: %v", err)
	}
	if len(loaded.Entries()) != 0 {
		t.Errorf("Expected 0 loaded entries after clear, got %d", len(loaded.Entries()))
	}
}
