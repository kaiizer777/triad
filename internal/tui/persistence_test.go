package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

func TestPersistence_FindLatestSession(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create first session and set an explicit earlier modification time.
	file1 := filepath.Join(tmpDir, "session_20260725_010000.jsonl")
	if err := os.WriteFile(file1, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	t1 := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file1, t1, t1); err != nil {
		t.Fatalf("failed to set mtime for file1: %v", err)
	}

	// 2. Create second session with a definitively later modification time.
	file2 := filepath.Join(tmpDir, "session_20260725_020000.jsonl")
	if err := os.WriteFile(file2, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}
	t2 := time.Date(2026, 7, 25, 2, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file2, t2, t2); err != nil {
		t.Fatalf("failed to set mtime for file2: %v", err)
	}

	latest, err := transcript.FindLatestSession(tmpDir)
	if err != nil {
		t.Fatalf("FindLatestSession failed: %v", err)
	}

	if filepath.Base(latest) != "session_20260725_020000.jsonl" {
		t.Errorf("expected latest session to be file2, got %s", filepath.Base(latest))
	}
}

func TestPersistence_ImmediateDiskAppend(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "live_session.jsonl")

	tr := transcript.NewTranscript(sessionPath)
	entry1 := transcript.Entry{
		Speaker:   transcript.SpeakerYou,
		Type:      transcript.TypeMessage,
		Content:   "Hello Triad",
		Timestamp: time.Now(),
	}

	if err := tr.Append(entry1); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify disk file content immediately after Append
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read session file from disk: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Hello Triad") {
		t.Errorf("expected disk file to contain 'Hello Triad', got: %s", content)
	}

	// Append second entry
	entry2 := transcript.Entry{
		Speaker:   transcript.SpeakerCoder,
		Type:      transcript.TypeMessage,
		Content:   "I am ready",
		Timestamp: time.Now(),
	}
	_ = tr.Append(entry2)

	reloaded, err := transcript.LoadFromFile(sessionPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	entries := reloaded.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in reloaded transcript, got %d", len(entries))
	}

	// Assert ID assignment round-trip.
	if entries[0].ID != 1 {
		t.Errorf("expected entries[0].ID == 1, got %d", entries[0].ID)
	}
	if entries[1].ID != 2 {
		t.Errorf("expected entries[1].ID == 2, got %d", entries[1].ID)
	}

	// Assert Speaker and Content field integrity.
	if entries[0].Speaker != transcript.SpeakerYou {
		t.Errorf("expected entries[0].Speaker == SpeakerYou, got %v", entries[0].Speaker)
	}
	if entries[1].Content != "I am ready" {
		t.Errorf("expected entries[1].Content == 'I am ready', got %q", entries[1].Content)
	}
}

func TestPersistence_StateRecovery_IdleOnEmpty(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "empty.jsonl"))

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateIdle {
		t.Errorf("expected sessionState to be StateIdle for empty transcript, got %v", model.sessionState)
	}
	if model.initialCmd != nil {
		t.Errorf("expected nil initialCmd for empty transcript")
	}
}

func TestPersistence_StateRecovery_PendingUserPrompt(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "user_prompt.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Build a web server",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateActive {
		t.Errorf("expected StateActive when user prompt is pending, got %v", model.sessionState)
	}
	if model.initialCmd == nil {
		t.Errorf("expected non-nil initialCmd (Coder turn) when user prompt is pending")
	}
}

func TestPersistence_StateRecovery_PendingProposedAction(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "proposed.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Create hello.txt",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerCoder,
		Type:    transcript.TypeProposedAction,
		Content: "Tool: write_file\nArguments: {\"path\":\"hello.txt\",\"content\":\"world\"}",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateActive {
		t.Errorf("expected StateActive, got %v", model.sessionState)
	}
	if model.activeToolCall == nil || model.activeToolCall.Function.Name != "write_file" {
		t.Fatalf("expected activeToolCall write_file, got %+v", model.activeToolCall)
	}
	if model.initialCmd == nil {
		t.Errorf("expected non-nil initialCmd (Reviewer turn)")
	}
}

func TestPersistence_StateRecovery_ApprovedActionPendingExecution(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "approved.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Create file",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerCoder,
		Type:    transcript.TypeProposedAction,
		Content: "Tool: write_file\nArguments: {\"path\":\"test.txt\",\"content\":\"abc\"}",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerReviewer,
		Type:    transcript.TypeMessage,
		Content: "APPROVED: looks safe to create",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateActive {
		t.Errorf("expected StateActive, got %v", model.sessionState)
	}
	if model.activeToolCall == nil || model.activeToolCall.Function.Name != "write_file" {
		t.Fatalf("expected activeToolCall write_file, got %+v", model.activeToolCall)
	}
	if model.initialCmd == nil {
		t.Errorf("expected non-nil initialCmd (Tool execution Cmd)")
	}
}

func TestPersistence_StateRecovery_ReviewerObjection(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "objected.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Delete temp files",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerCoder,
		Type:    transcript.TypeProposedAction,
		Content: "Tool: run_command\nArguments: {\"command\":\"rm -rf /\"}",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerReviewer,
		Type:    transcript.TypeMessage,
		Content: "OBJECTION: recursive delete on root is catastrophic",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateActive {
		t.Errorf("expected StateActive, got %v", model.sessionState)
	}
	if model.retryCount != 1 {
		t.Errorf("expected retryCount=1, got %d", model.retryCount)
	}
	if model.initialCmd == nil {
		t.Errorf("expected non-nil initialCmd (Coder revision turn)")
	}
}

func TestPersistence_StateRecovery_CompletedTask(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "done.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Finish project",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerCoder,
		Type:    transcript.TypeProposedAction,
		Content: "Tool: task_complete\n(no arguments)",
	})
	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerReviewer,
		Type:    transcript.TypeMessage,
		Content: "APPROVED: task is complete",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	if model.sessionState != loop.StateIdle {
		t.Errorf("expected StateIdle for completed task, got %v", model.sessionState)
	}
	if model.initialCmd != nil {
		t.Errorf("expected nil initialCmd for completed task")
	}
}

func TestPersistence_InitCmdBatching(t *testing.T) {
	client := &mockClient{}
	tmpDir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(tmpDir, "batch.jsonl"))

	_ = tr.Append(transcript.Entry{
		Speaker: transcript.SpeakerYou,
		Type:    transcript.TypeMessage,
		Content: "Build app",
	})

	coder := agent.AgentConfig{Name: "Coder", HasTools: true}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false}

	model := NewModel(tr, coder, reviewer, client, tmpDir, 0, &commands.Registry{})
	initCmd := model.Init()
	if initCmd == nil {
		t.Fatalf("expected non-nil Init Cmd batch")
	}

	// Simulate window size to verify viewport content rendering on restored transcript
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(Model)
	rendered := m.View().Content
	if rendered == "" {
		t.Errorf("expected non-empty View output after restoring session")
	}
}
