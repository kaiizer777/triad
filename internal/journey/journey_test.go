package journey

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed git init: %v", err)
	}

	exec.Command("git", "config", "user.name", "Test User").Dir = dir
	_ = exec.Command("git", "config", "user.name", "Test User").Run()
	_ = exec.Command("git", "config", "user.email", "test@example.com").Run()

	return dir
}

func createCommit(t *testing.T, dir, filename, subject string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("content: "+filename+"\n"), 0644); err != nil {
		t.Fatalf("failed writing test file: %v", err)
	}

	cmdAdd := exec.Command("git", "add", filename)
	cmdAdd.Dir = dir
	if err := cmdAdd.Run(); err != nil {
		t.Fatalf("failed git add: %v", err)
	}

	cmdCommit := exec.Command("git", "commit", "-m", subject)
	cmdCommit.Dir = dir
	if err := cmdCommit.Run(); err != nil {
		t.Fatalf("failed git commit: %v", err)
	}
}

func TestGetJourneyEntries_EmptyRepo(t *testing.T) {
	dir := createTestRepo(t)
	entries, err := GetJourneyEntries(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetJourneyEntries_MixMainAndTwin(t *testing.T) {
	dir := createTestRepo(t)

	// Create commits chronologically
	createCommit(t, dir, "file1.txt", "[triad] entry #1: initial setup")
	time.Sleep(10 * time.Millisecond)
	createCommit(t, dir, "file2.txt", "[triad] entry #2: [triad:twin #sub_123] write_file: helper.go")
	time.Sleep(10 * time.Millisecond)
	createCommit(t, dir, "file3.txt", "[triad] entry #3: complete task")

	entries, err := GetJourneyEntries(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify order (newest first: entry 3, entry 2, entry 1)
	if entries[0].EntryID != 3 || entries[0].Type != CommitTypeMain {
		t.Errorf("entry 0 mismatch: %+v", entries[0])
	}
	if entries[1].EntryID != 2 || entries[1].Type != CommitTypeTwin || entries[1].TwinID != "sub_123" {
		t.Errorf("entry 1 mismatch: %+v", entries[1])
	}
	if entries[2].EntryID != 1 || entries[2].Type != CommitTypeMain {
		t.Errorf("entry 2 mismatch: %+v", entries[2])
	}

	// Test filtering by validEntryIDs
	validIDs := map[int]bool{2: true}
	filtered, err := GetJourneyEntries(dir, validIDs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].EntryID != 2 {
		t.Errorf("expected 1 filtered entry (ID=2), got %d: %+v", len(filtered), filtered)
	}
}

func TestRenderASCII_ZeroAndNonZero(t *testing.T) {
	// Zero state
	zeroOut := RenderASCII(nil)
	if !strings.Contains(zeroOut, "No Triad commit history") {
		t.Errorf("expected zero state notice, got: %s", zeroOut)
	}

	// Non-zero state
	entries := []JourneyEntry{
		{
			Hash:       "a1b2c3d",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #1: main task",
			Intent:     "main task",
			EntryID:    1,
			Type:       CommitTypeMain,
		},
		{
			Hash:       "e5f6g7h",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #2: [triad:twin #t1] twin work",
			Intent:     "[triad:twin #t1] twin work",
			EntryID:    2,
			TwinID:     "t1",
			Type:       CommitTypeTwin,
		},
	}

	ascii := RenderASCII(entries)
	if !strings.Contains(ascii, "Commit Journey (2 commits)") {
		t.Errorf("missing header in ascii output: %s", ascii)
	}
	if !strings.Contains(ascii, "MAIN") || !strings.Contains(ascii, "TWIN:#t1") {
		t.Errorf("missing badges in ascii output: %s", ascii)
	}
	if !strings.Contains(ascii, "a1b2c3d") || !strings.Contains(ascii, "e5f6g7h") {
		t.Errorf("missing hashes in ascii output: %s", ascii)
	}
}

func TestRenderSidebarTimeline(t *testing.T) {
	// Zero state
	zeroOut := RenderSidebarTimeline(nil, 30)
	if !strings.Contains(zeroOut, "No Triad commit history") {
		t.Errorf("expected zero state notice, got: %s", zeroOut)
	}

	// Non-zero state
	entries := []JourneyEntry{
		{
			Hash:       "a1b2c3d",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #1: main task",
			Intent:     "main task feature implementation step",
			EntryID:    1,
			Type:       CommitTypeMain,
		},
		{
			Hash:       "e5f6g7h",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #2: [triad:twin #t1] twin work",
			Intent:     "subagent twin work task",
			EntryID:    2,
			TwinID:     "t1",
			Type:       CommitTypeTwin,
		},
	}

	sbOut := RenderSidebarTimeline(entries, 30)
	if !strings.Contains(sbOut, "MAIN") || !strings.Contains(sbOut, "TWIN:#t1") {
		t.Errorf("missing badges in sidebar output: %s", sbOut)
	}
	if !strings.Contains(sbOut, "a1b2c3d") || !strings.Contains(sbOut, "e5f6g7h") {
		t.Errorf("missing hashes in sidebar output: %s", sbOut)
	}
	if !strings.Contains(sbOut, "│ ") {
		t.Errorf("missing connecting graph lines in sidebar output: %s", sbOut)
	}
}

func TestRenderHTML_ExportHTML(t *testing.T) {
	dir := t.TempDir()

	entries := []JourneyEntry{
		{
			Hash:       "1122334",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #1: initial setup",
			Intent:     "initial setup",
			EntryID:    1,
			Type:       CommitTypeMain,
		},
		{
			Hash:       "4455667",
			AuthorDate: time.Now(),
			Subject:    "[triad] entry #2: [triad:twin #pair9] sub task",
			Intent:     "[triad:twin #pair9] sub task",
			EntryID:    2,
			TwinID:     "pair9",
			Type:       CommitTypeTwin,
		},
	}

	outPath, err := ExportHTML(dir, "custom_report.html", entries)
	if err != nil {
		t.Fatalf("ExportHTML failed: %v", err)
	}

	content, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("failed reading exported HTML file: %v", readErr)
	}

	htmlStr := string(content)
	if !strings.Contains(htmlStr, "<title>Triad Commit Journey</title>") {
		t.Errorf("missing HTML title: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "MAIN LOOP") || !strings.Contains(htmlStr, "TWIN SUBAGENT #pair9") {
		t.Errorf("missing expected badges in HTML: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, "1122334") || !strings.Contains(htmlStr, "4455667") {
		t.Errorf("missing hashes in HTML: %s", htmlStr)
	}

	// Test zero entries HTML export
	zeroPath, err := ExportHTML(dir, "zero_report.html", nil)
	if err != nil {
		t.Fatalf("ExportHTML failed for zero state: %v", err)
	}
	zeroContent, _ := os.ReadFile(zeroPath)
	if !strings.Contains(string(zeroContent), "No commits found") {
		t.Errorf("expected empty state card in HTML: %s", string(zeroContent))
	}
}

func TestGetJourneyEntries_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	entries, err := GetJourneyEntries(dir, nil)
	if err != nil {
		t.Fatalf("expected nil error for non-repo, got: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for non-repo, got %v", entries)
	}
}
