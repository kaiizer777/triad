package gitcommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitAction_GCAutoRunsAtConfiguredThresholdWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Triad Test")
	runGit(t, dir, "commit", "--allow-empty", "-q", "-m", "initial")

	oldRunner := runGCAuto
	t.Cleanup(func() {
		runGCAuto = oldRunner
		ConfigureGCHygiene(GCHygieneConfig{Enabled: true, CommitInterval: DefaultGCCommitInterval})
	})
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	runGCAuto = func(string) error {
		close(started)
		<-release
		close(finished)
		return nil
	}
	ConfigureGCHygiene(GCHygieneConfig{Enabled: true, CommitInterval: 2})

	baseline := commitTestFile(t, dir, "one.txt", "one", 1)
	commitTestFile(t, dir, "two.txt", "two", 2)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("git gc --auto was not scheduled at the second successful commit")
	}

	// While GC is deliberately held open, the next approved action still
	// commits promptly; this proves the approval/execution path never waits.
	elapsed := commitTestFile(t, dir, "three.txt", "three", 3)
	if elapsed > baseline+500*time.Millisecond {
		t.Fatalf("third auto-commit was delayed by background GC: baseline %s, with GC %s", baseline, elapsed)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("background GC did not finish")
	}
}

func commitTestFile(t *testing.T, dir, name, content string, entryID int) time.Duration {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := CommitAction(dir, []string{name}, CommitMessage{EntryID: entryID, Intent: "test commit", ToolName: "write_file"}); err != nil {
		t.Fatalf("CommitAction: %v", err)
	}
	return time.Since(start)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // fixed test command
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
