package loop_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/transcript"
)

func TestLoop_SessionStart_LoadsIndexOnly(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	coderCfg := agent.AgentConfig{Name: "Coder", Model: "test"}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", Model: "test"}
	client := newMockClient()

	l := loop.New(tr, coderCfg, reviewerCfg, client, tempDir)

	err := l.InitSessionMemory()
	if err != nil {
		t.Fatalf("InitSessionMemory failed: %v", err)
	}

	entries := tr.Entries()
	if len(entries) == 0 {
		t.Fatalf("expected at least one entry in transcript after InitSessionMemory")
	}

	foundIndex := false
	for _, entry := range entries {
		if entry.Speaker == transcript.SpeakerSystem && strings.Contains(entry.Content, "[Memory Index]") {
			foundIndex = true
			if !strings.Contains(entry.Content, "Triad Memory Index") {
				t.Errorf("expected INDEX.md content in system entry, got: %s", entry.Content)
			}
			// Verify topic file content was NOT loaded automatically
			if strings.Contains(entry.Content, "Multi-Agent Approval Loop") || strings.Contains(entry.Content, "Project Conventions") {
				t.Errorf("topic files should not be automatically loaded into context at session start!")
			}
		}
	}

	if !foundIndex {
		t.Errorf("expected [Memory Index] entry in transcript, none found")
	}
}

func TestLoop_DailyLog_AccumulatesAppendOnly(t *testing.T) {
	tempDir := t.TempDir()
	sessionPath := filepath.Join(tempDir, "session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	coderCfg := agent.AgentConfig{Name: "Coder", Model: "test"}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer", Model: "test"}
	client := newMockClient()
	client.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{Text: "I am ready to help."},
	})
	client.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{Text: "Task 2 underway."},
	})

	l := loop.New(tr, coderCfg, reviewerCfg, client, tempDir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 1)
	taskChan <- "Task 1: check memory"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = l.Run(ctx, taskChan)

	// Check daily log file
	now := time.Now()
	dailyLogContent, err := l.Memory.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}

	if !strings.Contains(dailyLogContent, "Task 1: check memory") {
		t.Errorf("expected daily log to contain human task 1, got: %s", dailyLogContent)
	}

	// Run second task in same day and confirm accumulation (append-only)
	taskChan2 := make(chan string, 1)
	taskChan2 <- "Task 2: verify append-only"
	close(taskChan2)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	_ = l.Run(ctx2, taskChan2)

	dailyLogContent2, err := l.Memory.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log after second run: %v", err)
	}

	if !strings.Contains(dailyLogContent2, "Task 1: check memory") {
		t.Errorf("expected daily log to retain Task 1 after second session run")
	}
	if !strings.Contains(dailyLogContent2, "Task 2: verify append-only") {
		t.Errorf("expected daily log to contain Task 2 after second session run")
	}
}

func TestLoop_ManualTopicWritePath_PreservesExisting(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create memory manager: %v", err)
	}

	// Write topic entry manually
	err = mgr.WriteTopicEntry("architecture", "Decoupled memory management via internal/memory package.")
	if err != nil {
		t.Fatalf("WriteTopicEntry failed: %v", err)
	}

	content, err := mgr.LoadTopic("architecture")
	if err != nil {
		t.Fatalf("LoadTopic failed: %v", err)
	}

	// Verify both seed content and manual entry exist
	if !strings.Contains(content, "Multi-Agent Approval Loop") {
		t.Errorf("existing seed content was overwritten or missing: %s", content)
	}
	if !strings.Contains(content, "Decoupled memory management via internal/memory package.") {
		t.Errorf("new manual topic entry was missing: %s", content)
	}

	// Write second manual entry
	err = mgr.WriteTopicEntry("architecture", "Daily log files are append-only.")
	if err != nil {
		t.Fatalf("WriteTopicEntry second call failed: %v", err)
	}

	content2, err := mgr.LoadTopic("architecture")
	if err != nil {
		t.Fatalf("LoadTopic failed: %v", err)
	}

	if !strings.Contains(content2, "Decoupled memory management via internal/memory package.") ||
		!strings.Contains(content2, "Daily log files are append-only.") {
		t.Errorf("topic entries corrupted after second write: %s", content2)
	}
}
