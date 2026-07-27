package memory_test

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/memory"
)

func TestManager_Initialization(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("unexpected error creating memory manager: %v", err)
	}

	memDir := mgr.Dir()

	// Verify required files exist
	files := []string{
		filepath.Join(memDir, memory.IndexFileName),
		filepath.Join(memDir, memory.PreferencesFileName),
		filepath.Join(memDir, memory.TopicsDirName, "architecture.md"),
		filepath.Join(memDir, memory.TopicsDirName, "conventions.md"),
	}

	for _, file := range files {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			t.Errorf("expected seed file %s to exist", file)
		}
	}
}

func TestManager_LoadIndex(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	idxContent, err := mgr.LoadIndex()
	if err != nil {
		t.Fatalf("failed to load index: %v", err)
	}

	if !strings.Contains(idxContent, "Triad Memory Index") {
		t.Errorf("expected INDEX.md to contain title, got: %s", idxContent)
	}
	if !strings.Contains(idxContent, "Quick Facts") {
		t.Errorf("expected INDEX.md to contain Quick Facts, got: %s", idxContent)
	}
}

func TestManager_AppendDailyLog(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	now := time.Now()
	err = mgr.AppendDailyLog(now, "Session started - task 1")
	if err != nil {
		t.Fatalf("failed to append daily log: %v", err)
	}

	err = mgr.AppendDailyLog(now, "Session ended - task 1 completed")
	if err != nil {
		t.Fatalf("failed to append daily log: %v", err)
	}

	content, err := mgr.ReadDailyLog(now)
	if err != nil {
		t.Fatalf("failed to read daily log: %v", err)
	}

	if !strings.Contains(content, "Session started - task 1") {
		t.Errorf("expected daily log to contain first entry, got: %s", content)
	}
	if !strings.Contains(content, "Session ended - task 1 completed") {
		t.Errorf("expected daily log to contain second entry, got: %s", content)
	}
}

func TestManager_CleanupDailyLogsArchivesExpiredAndKeepsRecentRawLogs(t *testing.T) {
	mgr, err := memory.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	oldDate := time.Now().AddDate(0, 0, -31)
	recentDate := time.Now().AddDate(0, 0, -1)
	if err := mgr.AppendDailyLog(oldDate, "old learning audit trail"); err != nil {
		t.Fatalf("append old log: %v", err)
	}
	if err := mgr.AppendDailyLog(recentDate, "recent learning audit trail"); err != nil {
		t.Fatalf("append recent log: %v", err)
	}

	oldPath := filepath.Join(mgr.Dir(), memory.DailyDirName, oldDate.Format("2006-01-02")+".md")
	if err := os.Chtimes(oldPath, oldDate, oldDate); err != nil {
		t.Fatalf("backdate old daily log: %v", err)
	}

	result, err := mgr.CleanupDailyLogs(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("cleanup daily logs: %v", err)
	}
	if result.ArchivedCount() != 1 {
		t.Fatalf("archived %d daily logs, want 1", result.ArchivedCount())
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired raw daily log still exists; stat error = %v", err)
	}

	archive, err := os.Open(result.Archived[0])
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		archive.Close()
		t.Fatalf("open gzip archive: %v", err)
	}
	archivedContent, err := io.ReadAll(gz)
	closeErr := gz.Close()
	archive.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read gzip archive: read=%v close=%v", err, closeErr)
	}
	if !strings.Contains(string(archivedContent), "old learning audit trail") {
		t.Fatalf("archive did not preserve old daily log: %q", archivedContent)
	}

	recentContent, err := mgr.ReadDailyLog(recentDate)
	if err != nil {
		t.Fatalf("read recent daily log after cleanup: %v", err)
	}
	if !strings.Contains(recentContent, "recent learning audit trail") {
		t.Fatalf("recent daily log changed during cleanup: %q", recentContent)
	}
}

func TestManager_WriteTopicEntry(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Write entry to existing topic
	err = mgr.WriteTopicEntry("architecture", "Use flat trace logs for observability.")
	if err != nil {
		t.Fatalf("failed to write to architecture topic: %v", err)
	}

	archContent, err := mgr.LoadTopic("architecture")
	if err != nil {
		t.Fatalf("failed to load architecture topic: %v", err)
	}

	if !strings.Contains(archContent, "Use flat trace logs for observability.") {
		t.Errorf("expected architecture topic to contain new entry, got: %s", archContent)
	}
	// Verify existing seed content is preserved
	if !strings.Contains(archContent, "Multi-Agent Approval Loop") {
		t.Errorf("expected architecture topic to preserve existing content, got: %s", archContent)
	}

	// Write entry to a new topic
	err = mgr.WriteTopicEntry("security", "Always sanitize subagent inputs.")
	if err != nil {
		t.Fatalf("failed to write to new security topic: %v", err)
	}

	secContent, err := mgr.LoadTopic("security")
	if err != nil {
		t.Fatalf("failed to load security topic: %v", err)
	}
	if !strings.Contains(secContent, "Always sanitize subagent inputs.") {
		t.Errorf("expected security topic to contain entry, got: %s", secContent)
	}

	// Verify INDEX.md updated pointer for new topic
	idxContent, err := mgr.LoadIndex()
	if err != nil {
		t.Fatalf("failed to load index: %v", err)
	}
	if !strings.Contains(idxContent, "security") {
		t.Errorf("expected INDEX.md to contain pointer to security topic, got: %s", idxContent)
	}
}

func TestManager_FetchTopicOnDemand(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := memory.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	topicContent, err := mgr.FetchTopicOnDemand("conventions")
	if err != nil {
		t.Fatalf("failed to fetch conventions topic: %v", err)
	}

	if !strings.Contains(topicContent, "Project Conventions") {
		t.Errorf("expected conventions content, got: %s", topicContent)
	}
}
