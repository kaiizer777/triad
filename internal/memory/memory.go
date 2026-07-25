// Package memory manages the index, preferences, topic, and daily-log memory storage structure for Triad.
//
// Following the principle "small, curated, and structured beats large and comprehensive", memory is stored
// in plain markdown files under the `memory/` directory of the project root:
//
//	memory/
//	├── INDEX.md              # Pointer file + quick facts, read every session (kept < 150-200 lines)
//	├── preferences.md        # Personal preferences (style, communication)
//	├── daily/
//	│   └── YYYY-MM-DD.md     # Raw, append-only, verbatim, per-session log
//	└── topics/
//	    ├── architecture.md   # Curated: key architecture decisions + why
//	    ├── conventions.md    # Curated: naming/style/testing conventions
//	    └── <topic>.md        # Curated, one file per recurring theme
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	IndexFileName       = "INDEX.md"
	PreferencesFileName = "preferences.md"
	DailyDirName        = "daily"
	TopicsDirName       = "topics"
)

// Default Seed Content for Memory Files
const DefaultIndexContent = `# Triad Memory Index

## Quick Facts
- Project: Triad
- Architecture: Multi-agent CLI/TUI (Coder, Reviewer, Orchestrator)

## Preferences
- User Preferences: see memory/preferences.md

## Topics Index
- architecture: memory/topics/architecture.md - Key architecture decisions + why
- conventions: memory/topics/conventions.md - Naming, style, and testing conventions
`

const DefaultPreferencesContent = `# User Preferences

- Communication: Clear, concise responses formatted in GitHub-style markdown.
- Testing: Thorough unit and integration tests for every package.
`

const DefaultArchitectureTopicContent = `# Architecture Decisions

- Multi-Agent Approval Loop: Coder proposes changes, Reviewer inspects and approves or objects.
- Deterministic Routing & Traceability: All orchestrator routing decisions and trace logs are recorded.
`

const DefaultConventionsTopicContent = `# Project Conventions

- Code Style: Idiomatic Go, error checking at call sites, modular internal packages.
- Testing: Table-driven unit tests with zero network dependencies in test mode.
`

// Manager handles reading and writing memory files.
type Manager struct {
	mu      sync.Mutex
	workDir string
	memDir  string
}

// NewManager creates a Manager targeting workDir/memory, creating directory structure
// and default seed files if missing.
func NewManager(workDir string) (*Manager, error) {
	memDir := filepath.Join(workDir, "memory")
	m := &Manager{
		workDir: workDir,
		memDir:  memDir,
	}

	if err := m.ensureDirectoriesAndSeeds(); err != nil {
		return nil, fmt.Errorf("memory: failed to initialize memory structure: %w", err)
	}

	return m, nil
}

// Dir returns the absolute path to the memory directory.
func (m *Manager) Dir() string {
	return m.memDir
}

// ensureDirectoriesAndSeeds creates the folder structure and seed files if they do not exist.
func (m *Manager) ensureDirectoriesAndSeeds() error {
	dailyDir := filepath.Join(m.memDir, DailyDirName)
	topicsDir := filepath.Join(m.memDir, TopicsDirName)

	if err := os.MkdirAll(dailyDir, 0755); err != nil {
		return fmt.Errorf("failed to create daily dir: %w", err)
	}
	if err := os.MkdirAll(topicsDir, 0755); err != nil {
		return fmt.Errorf("failed to create topics dir: %w", err)
	}

	// Create INDEX.md if missing
	indexPath := filepath.Join(m.memDir, IndexFileName)
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		if err := os.WriteFile(indexPath, []byte(DefaultIndexContent), 0644); err != nil {
			return fmt.Errorf("failed to create default INDEX.md: %w", err)
		}
	}

	// Create preferences.md if missing
	prefPath := filepath.Join(m.memDir, PreferencesFileName)
	if _, err := os.Stat(prefPath); os.IsNotExist(err) {
		if err := os.WriteFile(prefPath, []byte(DefaultPreferencesContent), 0644); err != nil {
			return fmt.Errorf("failed to create default preferences.md: %w", err)
		}
	}

	// Create topics/architecture.md if missing
	archPath := filepath.Join(topicsDir, "architecture.md")
	if _, err := os.Stat(archPath); os.IsNotExist(err) {
		if err := os.WriteFile(archPath, []byte(DefaultArchitectureTopicContent), 0644); err != nil {
			return fmt.Errorf("failed to create default architecture topic: %w", err)
		}
	}

	// Create topics/conventions.md if missing
	convPath := filepath.Join(topicsDir, "conventions.md")
	if _, err := os.Stat(convPath); os.IsNotExist(err) {
		if err := os.WriteFile(convPath, []byte(DefaultConventionsTopicContent), 0644); err != nil {
			return fmt.Errorf("failed to create default conventions topic: %w", err)
		}
	}

	return nil
}

// LoadIndex reads and returns the content of memory/INDEX.md.
// This is the ONLY memory file loaded automatically at session start.
func (m *Manager) LoadIndex() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	indexPath := filepath.Join(m.memDir, IndexFileName)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return "", fmt.Errorf("memory: failed to read INDEX.md: %w", err)
	}
	return string(data), nil
}

// LoadPreferences reads and returns the content of memory/preferences.md.
func (m *Manager) LoadPreferences() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefPath := filepath.Join(m.memDir, PreferencesFileName)
	data, err := os.ReadFile(prefPath)
	if err != nil {
		return "", fmt.Errorf("memory: failed to read preferences.md: %w", err)
	}
	return string(data), nil
}

// LoadTopic reads and returns the content of a curated topic file (e.g., "architecture" -> memory/topics/architecture.md).
func (m *Manager) LoadTopic(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.TrimSuffix(name, ".md")
	topicPath := filepath.Join(m.memDir, TopicsDirName, name+".md")

	data, err := os.ReadFile(topicPath)
	if err != nil {
		return "", fmt.Errorf("memory: topic %q not found or readable: %w", name, err)
	}
	return string(data), nil
}

// AppendDailyLog appends a session entry to memory/daily/YYYY-MM-DD.md using append-only mode.
func (m *Manager) AppendDailyLog(date time.Time, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fileName := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(m.memDir, DailyDirName, fileName)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("memory: failed to open daily log %s: %w", fileName, err)
	}
	defer f.Close()

	timestamp := date.Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s\n", timestamp, strings.TrimSpace(content))

	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("memory: failed to write to daily log %s: %w", fileName, err)
	}
	return nil
}

// ReadDailyLog reads and returns the full daily log content for a given date.
func (m *Manager) ReadDailyLog(date time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fileName := date.Format("2006-01-02") + ".md"
	filePath := filepath.Join(m.memDir, DailyDirName, fileName)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("memory: failed to read daily log %s: %w", fileName, err)
	}
	return string(data), nil
}

// WriteTopicEntry appends an entry to memory/topics/<topicName>.md manually without corrupting existing entries.
// If the topic file does not exist, it creates it with a header and appends the entry.
// Also ensures INDEX.md contains a reference to the topic if it's new.
func (m *Manager) WriteTopicEntry(topicName string, entry string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	topicName = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(topicName)), ".md")
	if topicName == "" {
		return fmt.Errorf("memory: topic name cannot be empty")
	}

	topicsDir := filepath.Join(m.memDir, TopicsDirName)
	topicPath := filepath.Join(topicsDir, topicName+".md")

	isNewFile := false
	if _, err := os.Stat(topicPath); os.IsNotExist(err) {
		isNewFile = true
	}

	f, err := os.OpenFile(topicPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("memory: failed to open topic file %s.md: %w", topicName, err)
	}

	var writeErr error
	if isNewFile {
		header := fmt.Sprintf("# %s Topic Memory\n\n", strings.Title(topicName))
		_, writeErr = f.WriteString(header)
	}

	if writeErr == nil {
		dateStr := time.Now().Format("2006-01-02")
		formattedEntry := fmt.Sprintf("- [%s] %s\n", dateStr, strings.TrimSpace(entry))
		_, writeErr = f.WriteString(formattedEntry)
	}

	f.Close()
	if writeErr != nil {
		return fmt.Errorf("memory: failed to write entry to topic %s.md: %w", topicName, writeErr)
	}

	// If a new topic file was created, update INDEX.md pointer if missing
	if isNewFile {
		indexPath := filepath.Join(m.memDir, IndexFileName)
		indexData, err := os.ReadFile(indexPath)
		if err == nil {
			topicRef := fmt.Sprintf("memory/topics/%s.md", topicName)
			if !strings.Contains(string(indexData), topicRef) {
				newPointer := fmt.Sprintf("- %s: %s - Curated %s topic\n", topicName, topicRef, topicName)
				fIdx, err := os.OpenFile(indexPath, os.O_WRONLY|os.O_APPEND, 0644)
				if err == nil {
					_, _ = fIdx.WriteString(newPointer)
					fIdx.Close()
				}
			}
		}
	}

	return nil
}

// FetchTopicOnDemand searches for topic content matching a topic name or query pointer.
func (m *Manager) FetchTopicOnDemand(query string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	query = strings.ToLower(strings.TrimSpace(query))
	entries, err := os.ReadDir(filepath.Join(m.memDir, TopicsDirName))
	if err != nil {
		return "", fmt.Errorf("memory: failed to list topics: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		baseName := strings.TrimSuffix(entry.Name(), ".md")
		if strings.Contains(query, baseName) || strings.Contains(baseName, query) {
			topicPath := filepath.Join(m.memDir, TopicsDirName, entry.Name())
			data, err := os.ReadFile(topicPath)
			if err == nil {
				return string(data), nil
			}
		}
	}

	return "", fmt.Errorf("memory: no matching topic found for query %q", query)
}
