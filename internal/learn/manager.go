package learn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/transcript"
)

var pathRegex = regexp.MustCompile(`(?i)(?:[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+|/[a-zA-Z0-9_-]+)\.(go|md|json|yaml|yml|ts|js|txt|py|java)\b`)

const StateFileName = "learn_state.json"

// Service manages candidate learning extractions, raw daily log appends,
// state persistence, and human-gated promotion/dismissal.
type Service struct {
	mu     sync.Mutex
	mem    *memory.Manager
	state  map[string]Item
	stFile string
}

// NewService constructs a learn.Service bound to the given memory.Manager.
func NewService(mem *memory.Manager) (*Service, error) {
	if mem == nil {
		return nil, fmt.Errorf("learn: memory manager cannot be nil")
	}

	stFile := filepath.Join(mem.Dir(), StateFileName)
	s := &Service{
		mem:    mem,
		state:  make(map[string]Item),
		stFile: stFile,
	}

	if err := s.loadState(); err != nil {
		return nil, fmt.Errorf("learn: failed to load state: %w", err)
	}

	return s, nil
}

func (s *Service) loadState() error {
	if _, err := os.Stat(s.stFile); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(s.stFile)
	if err != nil {
		return err
	}

	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}

	for _, item := range items {
		s.state[item.ID] = item
	}
	return nil
}

func (s *Service) saveState() error {
	var items []Item
	for _, item := range s.state {
		items = append(items, item)
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal learn state: %w", err)
	}

	return os.WriteFile(s.stFile, data, 0644)
}

// AutoExtractAndLog scans transcript entries for learnings, appends NEW learnings
// as raw entries to memory/daily/<date>.md automatically, and persists them as unreviewed.
// NO code path in AutoExtractAndLog EVER writes to topics/*.md or INDEX.md.
func (s *Service) AutoExtractAndLog(entries []transcript.Entry, date time.Time) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	extracted := ExtractLearnings(entries)
	var newItems []Item

	for _, item := range extracted {
		if _, exists := s.state[item.ID]; exists {
			continue
		}

		// Append to raw daily log automatically
		logContent := fmt.Sprintf("[AUTO_EXTRACT_LEARNING] ID: %s | Type: %s | %s", item.ID, item.Type, item.Summary)
		if err := s.mem.AppendDailyLog(date, logContent); err != nil {
			return nil, fmt.Errorf("learn: failed to append daily log: %w", err)
		}

		item.Status = StatusUnreviewed
		s.state[item.ID] = item
		newItems = append(newItems, item)
	}

	if len(newItems) > 0 {
		if err := s.saveState(); err != nil {
			return nil, err
		}
	}

	return newItems, nil
}

// GetUnreviewedItems returns all candidate learnings currently awaiting review.
func (s *Service) GetUnreviewedItems() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	var unreviewed []Item
	for _, item := range s.state {
		if item.Status == StatusUnreviewed {
			unreviewed = append(unreviewed, item)
		}
	}
	return unreviewed
}

type OverlapError struct {
	ExistingEntry string
	NewEntry      string
}

func (e *OverlapError) Error() string {
	return "overlap detected"
}

func extractTokens(text string) map[string]bool {
	tokens := make(map[string]bool)
	words := strings.Fields(strings.ToLower(text))
	stopwords := map[string]bool{"the": true, "and": true, "a": true, "to": true, "of": true, "in": true, "i": true, "is": true, "that": true, "it": true, "on": true, "you": true, "this": true, "for": true, "but": true, "with": true, "are": true, "have": true, "be": true, "at": true, "or": true, "as": true, "was": true, "so": true, "if": true, "out": true, "not": true}
	for _, w := range words {
		w = strings.Trim(w, ".,!?;:()[]{}'\"")
		if len(w) > 2 && !stopwords[w] {
			tokens[w] = true
		}
	}
	return tokens
}

func hasOverlap(newText, existingText string) bool {
	newTokens := extractTokens(newText)
	if len(newTokens) == 0 {
		return false
	}
	existingTokens := extractTokens(existingText)

	matchCount := 0
	for token := range newTokens {
		if existingTokens[token] {
			matchCount++
		}
	}

	return float64(matchCount)/float64(len(newTokens)) > 0.6
}

// Promote promotes an unreviewed or candidate learning to a curated topic file (e.g. "conventions").
// This is the ONLY method that writes to memory/topics/*.md and is strictly human-gated.
func (s *Service) Promote(id string, topicName string, force bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.state[id]
	if !exists {
		return "", fmt.Errorf("learn: learning ID %q not found", id)
	}

	if !force {
		existingContent, err := s.mem.LoadTopic(topicName)
		if err == nil && existingContent != "" {
			entries := parseTopicEntries(existingContent)
			for _, entry := range entries {
				if hasOverlap(item.Summary, entry.OriginalText) {
					return "", &OverlapError{
						ExistingEntry: strings.TrimSpace(entry.OriginalText),
						NewEntry:      item.Summary + "\n" + item.Context,
					}
				}
			}
		}
	}

	hasPath := pathRegex.MatchString(item.Summary) || pathRegex.MatchString(item.Context)
	warningMsg := ""
	pathTag := ""
	if hasPath {
		warningMsg = "This lesson references a file path, which rots fastest. Consider phrasing around the concept instead."
		pathTag = " | path-reference: true"
	}

	dateStr := time.Now().Format("2006-01-02")
	lessonContent := fmt.Sprintf("### [%s] %s: %s\n<!-- confidence: high | verified: %s | source: %s%s -->\n%s", 
		dateStr, 
		strings.ToUpper(string(item.Type)), 
		item.Summary, 
		dateStr, 
		id, 
		pathTag,
		strings.TrimSpace(item.Context))
	
	if err := s.mem.WriteTopicEntry(topicName, lessonContent); err != nil {
		return "", fmt.Errorf("learn: failed to promote to topic %s: %w", topicName, err)
	}

	item.Status = StatusPromoted
	s.state[id] = item
	return warningMsg, s.saveState()
}

// Dismiss marks a candidate learning as dismissed.
// The item remains in the append-only daily log without deleting or altering past log entries.
func (s *Service) Dismiss(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.state[id]
	if !exists {
		return fmt.Errorf("learn: learning ID %q not found", id)
	}

	item.Status = StatusDismissed
	s.state[id] = item
	return s.saveState()
}

// PromoteAll promotes all currently unreviewed candidate learnings to a target topic.
func (s *Service) PromoteAll(topicName string) (int, error) {
	unreviewed := s.GetUnreviewedItems()
	count := 0
	for _, item := range unreviewed {
		if _, err := s.Promote(item.ID, topicName, true); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// DismissAll marks all currently unreviewed candidate learnings as dismissed.
func (s *Service) DismissAll() (int, error) {
	unreviewed := s.GetUnreviewedItems()
	count := 0
	for _, item := range unreviewed {
		if err := s.Dismiss(item.ID); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// FormatDigest returns a human-readable summary digest of unreviewed candidate learnings.
func FormatDigest(unreviewed []Item) string {
	if len(unreviewed) == 0 {
		return "[Self-Learning Review Digest]\nNo new unreviewed candidate learnings found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Self-Learning Review Digest]\nFound %d unreviewed candidate learning(s) logged:\n\n", len(unreviewed)))

	for i, item := range unreviewed {
		sb.WriteString(fmt.Sprintf("%d. [ID: %s] [Type: %s]\n", i+1, item.ID, item.Type))
		sb.WriteString(fmt.Sprintf("   Summary: %s\n", item.Summary))
		if item.Context != "" {
			sb.WriteString(fmt.Sprintf("   Context: %s\n", item.Context))
		}
		sb.WriteString(fmt.Sprintf("   Promote: /learn promote %s <topic>\n", item.ID))
		sb.WriteString(fmt.Sprintf("   Dismiss: /learn dismiss %s\n\n", item.ID))
	}

	sb.WriteString("Options:\n")
	sb.WriteString("- /learn promote <id> <topic> : Promote item to memory/topics/<topic>.md\n")
	sb.WriteString("- /learn dismiss <id>         : Dismiss item (retains raw daily log entry)\n")
	sb.WriteString("- /learn promote-all <topic>  : Promote all to <topic>\n")
	sb.WriteString("- /learn dismiss-all          : Dismiss all unreviewed items\n")

	return strings.TrimSpace(sb.String())
}

// InjectItemForTest injects an item directly into the state for testing purposes.
func (s *Service) InjectItemForTest(item Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]Item)
	}
	s.state[item.ID] = item
}
