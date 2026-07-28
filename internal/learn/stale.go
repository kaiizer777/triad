package learn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TopicEntry struct {
	OriginalText string
	Date         time.Time
	HasHeader    bool
}

func parseTopicEntries(content string) []TopicEntry {
	var entries []TopicEntry
	var current strings.Builder
	var currentEntry TopicEntry
	var inEntry bool

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		isNewOldEntry := strings.HasPrefix(line, "- ")
		isNewNewEntry := strings.HasPrefix(line, "### ")
		
		if isNewOldEntry || isNewNewEntry {
			if inEntry && current.Len() > 0 {
				currentEntry.OriginalText = current.String()
				entries = append(entries, currentEntry)
				current.Reset()
				currentEntry = TopicEntry{}
			}
			
			inEntry = true
			current.WriteString(line + "\n")
			
			dateStr := extractDate(line)
			if dateStr != "" {
				parsed, err := time.Parse("2006-01-02", dateStr)
				if err == nil {
					currentEntry.Date = parsed
					currentEntry.HasHeader = true
				}
			}
		} else {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "# ") {
				// Ignore empty lines and main `# ` title headers if not in an entry
				if inEntry {
					current.WriteString(line + "\n")
				}
			} else {
				// A line that is not empty and not a title header, and not a standard entry start
				if !inEntry {
					inEntry = true // start a new unknown entry
				}
				current.WriteString(line + "\n")
			}
		}
	}
	if inEntry && current.Len() > 0 {
		currentEntry.OriginalText = current.String()
		entries = append(entries, currentEntry)
	}
	return entries
}

func extractDate(line string) string {
	start := strings.Index(line, "[")
	end := strings.Index(line, "]")
	if start != -1 && end != -1 && end > start+1 {
		return line[start+1 : end]
	}
	return ""
}

// StaleTopics returns a formatted report of topic entries older than the threshold.
func (s *Service) StaleTopics(threshold time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	topicsDir := filepath.Join(s.mem.Dir(), "topics")
	entries, err := os.ReadDir(topicsDir)
	if err != nil {
		return "", fmt.Errorf("failed to read topics dir: %w", err)
	}

	cutoff := time.Now().Add(-threshold)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Self-Learning] Stale Topics Report (older than %v days):\n\n", int(threshold.Hours()/24)))

	foundStale := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		topicName := strings.TrimSuffix(entry.Name(), ".md")
		topicPath := filepath.Join(topicsDir, entry.Name())
		data, err := os.ReadFile(topicPath)
		if err != nil {
			continue
		}
		
		topicEntries := parseTopicEntries(string(data))
		for _, te := range topicEntries {
			if te.HasHeader && !te.Date.IsZero() && te.Date.Before(cutoff) {
				sb.WriteString(fmt.Sprintf("Topic: %s | Date: %s\n%s\n---\n", topicName, te.Date.Format("2006-01-02"), strings.TrimSpace(te.OriginalText)))
				foundStale = true
			}
		}
	}

	if !foundStale {
		sb.WriteString("No stale entries found.\n")
	}
	return sb.String(), nil
}
