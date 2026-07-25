package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Transcript manages a thread-safe list of Entries and persistence to JSON Lines format.
type Transcript struct {
	mu       sync.RWMutex
	entries  []Entry
	filePath string
}

// NewTranscript initializes a new empty Transcript optionally bound to a session file path.
func NewTranscript(filePath string) *Transcript {
	return &Transcript{
		entries:  make([]Entry, 0),
		filePath: filePath,
	}
}

// SetFilePath updates the session file path for live append operations.
func (t *Transcript) SetFilePath(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.filePath = path
}

// FilePath returns the current bound session file path.
func (t *Transcript) FilePath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.filePath
}

// Entries returns a thread-safe copy of all entries in the transcript.
func (t *Transcript) Entries() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	copied := make([]Entry, len(t.entries))
	copy(copied, t.entries)
	return copied
}

// Append adds an entry to memory and immediately appends it as a single JSON line to the bound session file if set.
func (t *Transcript) Append(entry Entry) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Assign ID if not explicitly set
	if entry.ID == 0 {
		entry.ID = len(t.entries) + 1
	}

	t.entries = append(t.entries, entry)

	if t.filePath != "" {
		return appendLineToFile(t.filePath, entry)
	}

	return nil
}


// appendLineToFile appends a single Entry as a JSON line to the specified path.
func appendLineToFile(path string, entry Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory for session file: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open session file for append: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry to JSON: %w", err)
	}

	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write entry to session file: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync session file: %w", err)
	}

	return nil
}

// SaveToFile exports the entire transcript to the specified path in JSON Lines format.
func (t *Transcript) SaveToFile(path string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create transcript file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, entry := range t.entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal entry ID %d: %w", entry.ID, err)
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("failed to write entry ID %d: %w", entry.ID, err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush transcript file: %w", err)
	}

	return nil
}

// LoadFromFile reads a JSON Lines file from path and returns a Transcript populated with entries.
func LoadFromFile(path string) (*Transcript, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open transcript file: %w", err)
	}
	defer file.Close()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("failed to parse JSON line %d: %w", lineNum, err)
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading transcript file: %w", err)
	}

	return &Transcript{
		entries:  entries,
		filePath: path,
	}, nil
}

// FindLatestSession searches dir for .jsonl session files and returns the path to the most recently modified one.
// If no session files exist in dir, it returns an error.
func FindLatestSession(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var latestPath string
	var latestTime int64

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			log.Printf("warning: FindLatestSession: could not stat %q: %v", entry.Name(), err)
			continue
		}

		modTime := info.ModTime().UnixNano()
		if modTime > latestTime {
			latestTime = modTime
			latestPath = filepath.Join(dir, entry.Name())
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no session files found in %s", dir)
	}

	return latestPath, nil
}

