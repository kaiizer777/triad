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

// TestFormatSkillSelectionLine is the unit-level coverage for
// the Phase 4 multi-line skill-selection renderer. The renderer
// is the surface /trace uses to answer "which skill fired on this
// turn, at what tier, and at what token cost" — this test pins the
// layout so a future refactor doesn't quietly lose a field.
func TestFormatSkillSelectionLine(t *testing.T) {
	// Case 1: rich entry with full Data payload (the common
	// funnel-written shape).
	entry := tracelog.Entry{
		Timestamp:   "2026-07-27T00:00:00Z",
		Entity:      "skills",
		EventType:   tracelog.EventSkillSelection,
		Description: "frontend: main, backend: mini, db: main",
		Data: map[string]any{
			"task":     "wire up the booking form",
			"selected": []any{"frontend", "backend", "db"},
			"truncated": false,
			"total_tokens": 4200,
			"decisions": []any{
				map[string]any{"section": "frontend", "tier": "main", "token_cost": 2100, "forced": false},
				map[string]any{"section": "backend", "tier": "mini", "token_cost": 600, "forced": false},
				map[string]any{"section": "db", "tier": "main", "token_cost": 1500, "forced": false},
			},
		},
	}
	out := tracelog.FormatSkillSelectionLine(entry)
	for _, want := range []string{
		"task: wire up the booking form",
		"- section: frontend  tier: main  tokens: 2100",
		"- section: backend  tier: mini  tokens: 600",
		"- section: db  tier: main  tokens: 1500",
		"total tokens: 4200",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rich entry missing %q; got:\n%s", want, out)
		}
	}
	// The legacy line shape (single line with description) must
	// NOT be used for the rich path — that would mean the new
	// renderer is silently regressing.
	if strings.Count(out, "\n") < 4 {
		t.Errorf("rich entry should render multi-line, got: %q", out)
	}

	// Case 2: cap-truncated entry — the renderer must surface
	// the truncation tag so the human can see the cap fired.
	trunc := tracelog.Entry{
		Timestamp: "2026-07-27T00:00:01Z",
		Entity:    "skills",
		EventType: tracelog.EventSkillSelection,
		Data: map[string]any{
			"task":      "build everything",
			"selected":  []any{"a", "b", "c", "d"},
			"truncated": true,
			"decisions": []any{
				map[string]any{"section": "a", "tier": "main", "token_cost": 100},
				map[string]any{"section": "b", "tier": "main", "token_cost": 100},
				map[string]any{"section": "c", "tier": "main", "token_cost": 100},
			},
		},
	}
	out2 := tracelog.FormatSkillSelectionLine(trunc)
	if !strings.Contains(out2, "[cap-truncated") {
		t.Errorf("truncated entry missing cap-truncated tag; got:\n%s", out2)
	}

	// Case 3: selected but no decisions — section was picked but
	// the registry had no body. Renderer must still surface the
	// section as "(none)" so it doesn't silently disappear.
	noBody := tracelog.Entry{
		Timestamp: "2026-07-27T00:00:02Z",
		Entity:    "skills",
		EventType: tracelog.EventSkillSelection,
		Data: map[string]any{
			"task":      "try this section",
			"selected":  []any{"missing-section"},
			"decisions": []any{},
		},
	}
	out3 := tracelog.FormatSkillSelectionLine(noBody)
	if !strings.Contains(out3, "section: missing-section  tier: (none)") {
		t.Errorf("empty-body entry should surface section as (none); got:\n%s", out3)
	}

	// Case 4: legacy entry (no Data field) — fall back to the
	// pre-Phase-4 single-line format so old trace files still
	// render.
	legacy := tracelog.Entry{
		Timestamp:   "2026-07-27T00:00:03Z",
		Entity:      "skills",
		EventType:   tracelog.EventSkillSelection,
		Description: "frontend: main",
	}
	out4 := tracelog.FormatSkillSelectionLine(legacy)
	if !strings.Contains(out4, "(skill_selection) frontend: main") {
		t.Errorf("legacy entry should fall back to single-line; got: %q", out4)
	}
	// Forced tag — distinct from the non-forced case.
	forced := tracelog.Entry{
		Timestamp: "2026-07-27T00:00:04Z",
		Entity:    "skills",
		EventType: tracelog.EventSkillSelection,
		Data: map[string]any{
			"task":     "user pinned db",
			"selected": []any{"db"},
			"decisions": []any{
				map[string]any{"section": "db", "tier": "main", "token_cost": 300, "forced": true},
			},
		},
	}
	out5 := tracelog.FormatSkillSelectionLine(forced)
	if !strings.Contains(out5, "[forced]") {
		t.Errorf("forced entry should surface [forced] tag; got: %q", out5)
	}
}
