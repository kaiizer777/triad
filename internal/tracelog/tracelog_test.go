package tracelog_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/tracelog"
)

func TestTracePathForSession(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", filepath.Join("sessions", "traces", "default.jsonl")},
		{"sessions/2026-07-25-session.jsonl", filepath.Join("sessions", "traces", "2026-07-25-session.jsonl")},
		{"sessions/twins/twin-001.jsonl", filepath.Join("sessions", "traces", "twin-001.jsonl")},
		{"sessions/traces/test.jsonl", filepath.Join("sessions", "traces", "test.jsonl")},
	}

	for _, tt := range tests {
		got := tracelog.TracePathForSession(tt.input)
		if filepath.Clean(got) != filepath.Clean(tt.want) {
			t.Errorf("TracePathForSession(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAppendAndLoadTrace(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "traces", "test_session.jsonl")

	entry1 := tracelog.Entry{
		Entity:      "orchestrator",
		EventType:   tracelog.EventRoutingDecision,
		Description: "Routed task to twin (medium complexity)",
	}

	entry2 := tracelog.Entry{
		Entity:      "twin:task-100",
		EventType:   tracelog.EventTwinSpawn,
		Description: "Spawned twin subagent task-100",
	}

	if err := tracelog.Append(tracePath, entry1); err != nil {
		t.Fatalf("Append entry1 failed: %v", err)
	}

	if err := tracelog.Append(tracePath, entry2); err != nil {
		t.Fatalf("Append entry2 failed: %v", err)
	}

	loaded, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 trace entries, got %d", len(loaded))
	}

	if loaded[0].Entity != "orchestrator" || loaded[0].EventType != tracelog.EventRoutingDecision {
		t.Errorf("loaded[0] mismatch: %+v", loaded[0])
	}
	if loaded[0].Timestamp == "" {
		t.Error("expected auto-populated timestamp in loaded[0]")
	}

	if loaded[1].Entity != "twin:task-100" || loaded[1].EventType != tracelog.EventTwinSpawn {
		t.Errorf("loaded[1] mismatch: %+v", loaded[1])
	}
}

func TestFormatTraceOutput(t *testing.T) {
	empty := tracelog.FormatTraceOutput(nil)
	if empty != "No trace events recorded for this session yet." {
		t.Errorf("FormatTraceOutput(nil) = %q", empty)
	}

	entries := []tracelog.Entry{
		{
			Timestamp:   "2026-07-25T19:00:00Z",
			Entity:      "orchestrator",
			EventType:   tracelog.EventRoutingDecision,
			Description: "Routed to twin",
		},
		{
			Timestamp:   "2026-07-25T19:00:01Z",
			Entity:      "twin:task-1",
			EventType:   tracelog.EventTwinSpawn,
			Description: "Started twin",
		},
	}

	output := tracelog.FormatTraceOutput(entries)
	if !strings.Contains(output, "Session Trace Log (2 events):") {
		t.Errorf("FormatTraceOutput output missing header: %q", output)
	}
	if !strings.Contains(output, "[orchestrator] (routing_decision) Routed to twin") {
		t.Errorf("FormatTraceOutput output missing entry 1: %q", output)
	}
	if !strings.Contains(output, "[twin:task-1] (twin_spawn) Started twin") {
		t.Errorf("FormatTraceOutput output missing entry 2: %q", output)
	}
}
