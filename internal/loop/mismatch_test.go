package loop_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

func TestCheckModeMismatch_Unit(t *testing.T) {
	tests := []struct {
		name     string
		mode     loop.Mode
		task     string
		wantNote bool
		wantText string
	}{
		{
			name:     "orchestrator mode - no mismatch note ever",
			mode:     loop.ModeOrchestrator,
			task:     "hello",
			wantNote: false,
		},
		{
			name:     "triad mode - trivial task triggers triad mismatch note",
			mode:     loop.ModeTriad,
			task:     "hello, what time is it?",
			wantNote: true,
			wantText: "[System]: Note — you're in Triad mode; this looks trivial, /mode general would skip the review overhead.",
		},
		{
			name:     "triad mode - complex task triggers no mismatch note",
			mode:     loop.ModeTriad,
			task:     "Refactor auth and payment logic across multiple files in internal/auth and internal/payment",
			wantNote: false,
		},
		{
			name:     "general mode - complex task triggers general mismatch note",
			mode:     loop.ModeGeneral,
			task:     "Refactor auth and payment logic across multiple files in internal/auth and internal/payment",
			wantNote: true,
			wantText: "[System]: Note — you're in General mode; this task looks complex/sensitive, /mode triad would provide Reviewer oversight.",
		},
		{
			name:     "general mode - trivial task triggers no mismatch note",
			mode:     loop.ModeGeneral,
			task:     "hello, what time is it?",
			wantNote: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loop.CheckModeMismatch(tt.mode, tt.task)
			if tt.wantNote {
				if got != tt.wantText {
					t.Errorf("CheckModeMismatch(%v, %q) = %q; want %q", tt.mode, tt.task, got, tt.wantText)
				}
			} else {
				if got != "" {
					t.Errorf("CheckModeMismatch(%v, %q) = %q; want empty string", tt.mode, tt.task, got)
				}
			}
		})
	}
}

func TestLoop_ModeMismatchNotice_ForcedTriad(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(dir, "session.jsonl"))

	client := newMockClient()
	client.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{Text: "I can answer that simple question right away."},
	})
	client.addResponse("Reviewer", mockResponse{
		resp: agent.AgentResponse{Text: "APPROVED: simple answer looks fine."},
	})

	coderCfg := agent.AgentConfig{Name: "Coder"}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer"}

	l := loop.New(tr, coderCfg, reviewerCfg, client, dir)
	l.CurrentMode = loop.ModeTriad

	taskChan := make(chan string, 1)
	taskChan <- "hello, what is Go?"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Loop.Run failed: %v", err)
	}

	entries := tr.Entries()

	// Verify that a System mismatch entry exists
	var sysNoteFound bool
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem && e.Content == "[System]: Note — you're in Triad mode; this looks trivial, /mode general would skip the review overhead." {
			sysNoteFound = true
			break
		}
	}

	if !sysNoteFound {
		t.Errorf("expected passive mismatch note in transcript, but none was found. Entries: %+v", entries)
	}

	// Verify full Triad execution occurred (Coder turn and Reviewer turn executed without silent downgrade)
	var coderFound, reviewerFound bool
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerCoder {
			coderFound = true
		}
		if e.Speaker == transcript.SpeakerReviewer {
			reviewerFound = true
		}
	}

	if !coderFound || !reviewerFound {
		t.Errorf("expected full Triad execution (Coder: %v, Reviewer: %v), should not silently downgrade", coderFound, reviewerFound)
	}
}

func TestLoop_ModeMismatchNotice_ForcedGeneral(t *testing.T) {
	dir := t.TempDir()
	tr := transcript.NewTranscript(filepath.Join(dir, "session.jsonl"))

	client := newMockClient()
	client.addResponse("Coder", mockResponse{
		resp: agent.AgentResponse{Text: "Refactor plan prepared."},
	})

	coderCfg := agent.AgentConfig{Name: "Coder"}
	reviewerCfg := agent.AgentConfig{Name: "Reviewer"}

	l := loop.New(tr, coderCfg, reviewerCfg, client, dir)
	l.CurrentMode = loop.ModeGeneral

	taskChan := make(chan string, 1)
	taskChan <- "Refactor auth and payment logic across multiple files in internal/auth and internal/payment"
	close(taskChan)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := l.Run(ctx, taskChan); err != nil {
		t.Fatalf("Loop.Run failed: %v", err)
	}

	entries := tr.Entries()

	// Verify System mismatch entry exists for General mode
	var sysNoteFound bool
	for _, e := range entries {
		if e.Speaker == transcript.SpeakerSystem && e.Content == "[System]: Note — you're in General mode; this task looks complex/sensitive, /mode triad would provide Reviewer oversight." {
			sysNoteFound = true
			break
		}
	}

	if !sysNoteFound {
		t.Errorf("expected passive mismatch note in General mode, but none was found. Entries: %+v", entries)
	}
}
