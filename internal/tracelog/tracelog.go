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
)

// Entry represents a single high-level event in the session trace log.
type Entry struct {
	Timestamp   string `json:"timestamp"`
	Entity      string `json:"entity"`
	EventType   string `json:"event_type"`
	Description string `json:"description"`
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
func FormatTraceOutput(entries []Entry) string {
	if len(entries) == 0 {
		return "No trace events recorded for this session yet."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Session Trace Log (%d events):\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&sb, "  [%s] [%s] (%s) %s\n", e.Timestamp, e.Entity, e.EventType, e.Description)
	}
	return strings.TrimRight(sb.String(), "\n")
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
