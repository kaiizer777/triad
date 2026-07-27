package transcript

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupSessionsArchivesExpiredArtifactsAndProtectsActiveSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	old := time.Now().AddDate(0, 0, -31)
	recent := time.Now().AddDate(0, 0, -1)
	paths := []string{
		filepath.Join(root, "old-main.jsonl"),
		filepath.Join(root, "subagents", "old-subagent.jsonl"),
		filepath.Join(root, "twins", "old-twin.jsonl"),
		filepath.Join(root, "traces", "old-trace.jsonl"),
	}
	for _, path := range paths {
		writeSessionFixture(t, path, "old", old)
	}
	active := filepath.Join(root, "active.jsonl")
	writeSessionFixture(t, active, "active", old)
	activeTrace := filepath.Join(root, "traces", "active.jsonl")
	writeSessionFixture(t, activeTrace, "active trace", old)
	recentPath := filepath.Join(root, "recent.jsonl")
	writeSessionFixture(t, recentPath, "recent", recent)

	result, err := CleanupSessions(root, 30*24*time.Hour, active, activeTrace)
	if err != nil {
		t.Fatalf("CleanupSessions() error = %v", err)
	}
	if result.ArchivedCount() != len(paths) {
		t.Fatalf("archived %d files, want %d", result.ArchivedCount(), len(paths))
	}
	for _, source := range paths {
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Errorf("expired source %q still exists; stat error = %v", source, err)
		}
	}
	for _, path := range []string{active, activeTrace, recentPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("protected or recent source %q missing: %v", path, err)
		}
	}
	for _, archive := range result.Archived {
		if got := readGzip(t, archive); got != "old" {
			t.Errorf("archive %q content = %q, want old", archive, got)
		}
	}
}

func writeSessionFixture(t *testing.T, path, content string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func readGzip(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	b, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
