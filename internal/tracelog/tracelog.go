package tracelog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EventType constants for trace entries.
const (
	EventRoutingDecision   = "routing_decision"
	EventTwinSpawn         = "twin_spawn"
	EventTwinComplete      = "twin_complete"
	EventClarifyTrigger     = "clarify_trigger"
	EventHookIntervention  = "hook_intervention"
	EventRecoveryAttempt   = "recovery_attempt"
	// EventSkillSelection is emitted by the Workflow 5 skills funnel
	// (internal/skills.ApplySelection) on every Coder turn that
	// produced a SELECTED_SECTIONS line. It carries the structured
	// per-section breakdown (section, tier, token cost) plus the
	// triggering user task in the Data field — this is what Phase 4
	// observability reads from to answer "why did Coder just do
	// something DB-flavored when I asked for a UI tweak." Without
	// this event type, /trace cannot show skill decisions at all
	// (it has always read from this log, never the transcript).
	EventSkillSelection = "skill_selection"
	EventMemoryLoaded   = "memory_loaded"
	EventTopicFetched   = "topic_fetched"
	EventLearnExtracted = "learn_extracted"
	EventLearnPromoted  = "learn_promoted"
	EventLearnDismissed = "learn_dismissed"
)

// Entry represents a single high-level event in the session trace log.
//
// Most events fit comfortably in Description as a single human-readable
// line. Skill selections don't — they have per-section fields (section,
// tier, token cost) that need to be parseable, not flattened into prose.
// For those, callers populate Data with a structured payload; the
// formatter renders it as a multi-line block, and `description` carries
// a one-line summary so legacy consumers (e.g. a `grep` over the JSONL)
// still see something useful.
//
// Data is JSON-omitempty, so older entries without it (and any future
// event types that don't need it) serialize to the same shape they did
// before this field was added — no migration needed.
type Entry struct {
	Timestamp   string         `json:"timestamp"`
	Entity      string         `json:"entity"`
	EventType   string         `json:"event_type"`
	Description string         `json:"description"`
	Data        map[string]any `json:"data,omitempty"`
}

var mu sync.Mutex

// TracePathForSession computes the trace file path (e.g. sessions/traces/<session-id>.jsonl)
// given a session transcript path or directory.
func TracePathForSession(sessionPath string) string {
	if sessionPath == "" {
		return filepath.Join("sessions", "traces", "default.jsonl")
	}

	clean := filepath.Clean(sessionPath)

	// If already in a traces directory, return as is.
	if strings.Contains(clean, string(filepath.Separator)+"traces"+string(filepath.Separator)) ||
		strings.HasPrefix(clean, "sessions"+string(filepath.Separator)+"traces"+string(filepath.Separator)) {
		return clean
	}

	dir := filepath.Dir(clean)
	base := filepath.Base(clean)

	// Strip .jsonl extension
	sessionID := strings.TrimSuffix(base, filepath.Ext(base))

	// If the parent directory is a sub-dir like "twins" or "subagents", move up to root "sessions" dir
	parentBase := filepath.Base(dir)
	if parentBase == "twins" || parentBase == "subagents" {
		parentDir := filepath.Dir(dir)
		if entries, err := os.ReadDir(parentDir); err == nil {
			var mainSessionID string
			var latestTime int64
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
					info, err := e.Info()
					if err == nil && info.ModTime().UnixNano() > latestTime {
						latestTime = info.ModTime().UnixNano()
						mainSessionID = strings.TrimSuffix(e.Name(), ".jsonl")
					}
				}
			}
			if mainSessionID != "" {
				sessionID = mainSessionID
			}
		}
		dir = parentDir
	}

	return filepath.Join(dir, "traces", sessionID+".jsonl")
}

// Append appends a trace entry as a JSON Line to tracePath.
// If entry.Timestamp is empty, the current UTC time in RFC3339 format is filled.
func Append(tracePath string, entry Entry) error {
	if tracePath == "" {
		tracePath = TracePathForSession("")
	}

	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(tracePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("tracelog: failed to create directory %q: %w", dir, err)
	}

	f, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("tracelog: failed to open file %q: %w", tracePath, err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("tracelog: failed to marshal entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("tracelog: failed to write line to %q: %w", tracePath, err)
	}

	return f.Sync()
}

// LoadTrace reads JSON Lines from tracePath and returns a slice of Entry structs.
func LoadTrace(tracePath string) ([]Entry, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tracelog: failed to open trace file %q: %w", tracePath, err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("tracelog: failed to unmarshal line: %w", err)
		}
		entries = append(entries, e)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("tracelog: error reading trace file %q: %w", tracePath, err)
	}

	return entries, nil
}

// FormatTraceOutput formats entries into a flat, chronological string for TUI display.
//
// Most event types render as a single line. Skill selections are
// multi-line by design — work.md §7 asks the trace to show, for each
// Coder turn: the triggering user message, the sections selected, the
// tier actually injected per section, and the token cost. A single
// line can't carry that without mangling it, so FormatTraceOutput
// switches on EventType: skill_selection events get a multi-line block
// (rendered by FormatSkillSelectionLine below); every other event
// keeps the legacy single-line format so existing /trace consumers
// (humans, scripts, /status, etc.) don't see a behavior change.
//
// Entries that pre-date this change (no Data field, plain Description
// only) still render correctly — FormatSkillSelectionLine falls back
// to Description when Data is absent.
func FormatTraceOutput(entries []Entry) string {
	if len(entries) == 0 {
		return "No trace events recorded for this session yet."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Session Trace Log (%d events):\n", len(entries))
	for _, e := range entries {
		if e.EventType == EventSkillSelection {
			sb.WriteString(FormatSkillSelectionLine(e))
			continue
		}
		fmt.Fprintf(&sb, "  [%s] [%s] (%s) %s\n", e.Timestamp, e.Entity, e.EventType, e.Description)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// SkillSelectionData is the structured payload FormatTraceOutput reads
// from an Entry's Data field when EventType is EventSkillSelection.
// The skills funnel writes this shape; FormatTraceOutput reads it.
//
// Fields:
//   - task: the most-recent human [You] message that triggered this
//     turn, truncated to ~200 chars. Empty if no human message had
//     been recorded yet (e.g. resume before any user input).
//   - selected: the section labels Coder declared (after registry
//     filtering, sorted, cap-truncated). May be empty for a
//     no-selection turn — those still emit a trace entry so the
//     "what was Coder thinking" question is answerable.
//   - decisions: per-section tier + token cost. Missing when the
//     section was selected but the chosen tier's body was empty
//     (Coder picked the section but the registry had no body to
//     inject — observability still wants to see "picked X, no
//     content" rather than silently dropping it).
//   - truncated: true if Coder's selection exceeded MaxSectionsPerTurn
//     and the funnel dropped the overflow. The /trace view always
//     shows this so the human can see "Coder wanted 5 sections but
//     only got 3 — the cap fired."
//   - total_tokens: sum of TokenCost across decisions, the headline
//     "how much did this turn cost" number. Work.md §7 calls this
//     out specifically as a per-turn number, not a per-section one.
type SkillSelectionData struct {
	Task        string                  `json:"task,omitempty"`
	Selected    []string                `json:"selected,omitempty"`
	Decisions   []SkillSelectionDecision `json:"decisions,omitempty"`
	Truncated   bool                    `json:"truncated,omitempty"`
	TotalTokens int                     `json:"total_tokens,omitempty"`
}

// SkillSelectionDecision is one row of the per-section breakdown in a
// SkillSelectionData. Empty Body (or empty Tier) means "section was
// selected but no body was injected" — the /trace view renders this
// distinctly so the human can see Coder wanted the section even
// though nothing landed.
type SkillSelectionDecision struct {
	Section   string `json:"section"`
	Tier      string `json:"tier,omitempty"`
	TokenCost int    `json:"token_cost,omitempty"`
	Forced    bool   `json:"forced,omitempty"`
}

// FormatSkillSelectionLine renders a single EventSkillSelection entry
// as a multi-line block for the TUI /trace view. The output is
// designed to be readable on its own (no surrounding context needed)
// so a human can copy-paste one block out of /trace and still
// understand what happened on that turn.
//
// Layout:
//
//	  [<timestamp>] [skills] (skill_selection)
//	    task: <excerpt>
//	    - section: <a>  tier: <tier>  tokens: <n>
//	    - section: <b>  tier: <tier>  tokens: <n>
//	    total tokens: <sum>   (cap-truncated)
//
// The "cap-truncated" tag is appended only when the entry actually
// records truncation. Sections in `selected` but not in `decisions`
// (e.g. unknown sections dropped by the funnel, or selected sections
// with empty bodies) are rendered with tier=(none) so the human
// doesn't wonder why a section they see in the task isn't in the
// injected list.
//
// Backward compatibility: if an entry was written without Data
// (older code path, hand-written trace, or a future writer that
// hasn't been updated), this function falls back to the legacy
// single-line `[ts] [entity] (event_type) description` format so
// the trace view never blanks out an entry.
func FormatSkillSelectionLine(e Entry) string {
	if e.Data == nil {
		// Legacy / hand-written entry: render the description as a
		// single line the way the pre-Phase-4 renderer would have.
		return fmt.Sprintf("  [%s] [%s] (%s) %s\n", e.Timestamp, e.Entity, e.EventType, e.Description)
	}

	// Decode the structured payload. We do this by re-marshaling
	// Data through JSON so any future-added optional fields don't
	// break the formatter — only the ones SkillSelectionData
	// declares are read.
	var data SkillSelectionData
	raw, err := json.Marshal(e.Data)
	if err == nil {
		_ = json.Unmarshal(raw, &data)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "  [%s] [%s] (%s)\n", e.Timestamp, e.Entity, e.EventType)
	if data.Task != "" {
		fmt.Fprintf(&sb, "    task: %s\n", data.Task)
	}

	// Render decisions first (the actually-injected sections, with
	// their tier + token cost). Then, if any sections are in
	// `selected` but not in `decisions`, render them as "(none)"
	// so the human can see they were picked but no body landed.
	emitted := make(map[string]bool, len(data.Decisions))
	for _, d := range data.Decisions {
		emitted[d.Section] = true
		tierLabel := d.Tier
		if tierLabel == "" {
			tierLabel = "(none)"
		}
		forcedTag := ""
		if d.Forced {
			forcedTag = "  [forced]"
		}
		fmt.Fprintf(&sb, "    - section: %s  tier: %s  tokens: %d%s\n", d.Section, tierLabel, d.TokenCost, forcedTag)
	}
	for _, sec := range data.Selected {
		if emitted[sec] {
			continue
		}
		fmt.Fprintf(&sb, "    - section: %s  tier: (none)  [selected but no body injected]\n", sec)
	}

	if data.Truncated {
		fmt.Fprintf(&sb, "    [cap-truncated: Coder's selection exceeded the %d-section cap]\n", 3)
	}
	if data.TotalTokens > 0 || len(data.Decisions) > 0 {
		fmt.Fprintf(&sb, "    total tokens: %d\n", data.TotalTokens)
	}
	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// LogHookIntervention is a convenience helper for logging hook/blocklist intervention events.
func LogHookIntervention(sessionPath, entity, description string) error {
	if entity == "" {
		entity = "hook:blocklist"
	}
	tracePath := TracePathForSession(sessionPath)
	return Append(tracePath, Entry{
		Entity:      entity,
		EventType:   EventHookIntervention,
		Description: description,
	})
}
