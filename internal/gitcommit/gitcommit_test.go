package gitcommit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/gitcommit"
)

// requireGit skips the test if git isn't available on the host. Most CI
// environments and developer machines have it, but we don't want a missing
// git binary to surface as a confusing test failure.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping")
	}
}

// makeRepo initialises a temp git repo with user.name/email set, plus an
// empty initial commit, so tests can run `git log` immediately and so
// identity never falls back to the developer's global git config.
// Returns the workDir.
//
// We disable HOME-based global config via `git config --global` settings
// inside the temp dir (no, that touches the real HOME — instead we use
// the -c flag for each command, and also set GIT_CONFIG_GLOBAL via the
// environment so any descendant `git` invocation is isolated). The most
// reliable way is to set `GIT_CONFIG_GLOBAL=/dev/null` (or a tempfile)
// so the developer's `user.name` doesn't leak in.
func makeRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec
		cmd.Dir = dir
		// Point GIT_CONFIG_GLOBAL at /dev/null so the developer's
		// real global config doesn't leak in. On Windows, /dev/null
		// isn't a real path — use NUL instead.
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+devNullPath(),
			"GIT_CONFIG_SYSTEM="+devNullPath(),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "--local", "user.email", "test@example.com")
	run("config", "--local", "user.name", "Triad Test")
	// Create an empty initial commit so `git log` works immediately.
	run("commit", "--allow-empty", "-q", "-m", "initial")
	return dir
}

// devNullPath returns a writable black-hole path for the current platform.
func devNullPath() string {
	if _, err := os.Stat("NUL"); err == nil {
		return "NUL"
	}
	return "/dev/null"
}

// ---------------------------------------------------------------------------
// EnsureRepo / IsRepo
// ---------------------------------------------------------------------------

func TestEnsureRepo_AlreadyARepo(t *testing.T) {
	dir := makeRepo(t)

	err := gitcommit.EnsureRepo(dir)
	if err == nil {
		t.Fatal("expected ErrAlreadyRepo, got nil")
	}
	if _, ok := err.(gitcommit.ErrAlreadyRepo); !ok {
		t.Errorf("expected ErrAlreadyRepo, got %T: %v", err, err)
	}
}

func TestEnsureRepo_InitialisesNewRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()

	err := gitcommit.EnsureRepo(dir)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !gitcommit.IsRepo(dir) {
		t.Error("expected IsRepo to be true after EnsureRepo")
	}
}

func TestIsRepo_FreshDir(t *testing.T) {
	dir := t.TempDir()
	if gitcommit.IsRepo(dir) {
		t.Error("expected IsRepo to be false in a fresh dir")
	}
}

// ---------------------------------------------------------------------------
// CheckUserConfigured
// ---------------------------------------------------------------------------

func TestCheckUserConfigured_OK(t *testing.T) {
	dir := makeRepo(t)
	if err := gitcommit.CheckUserConfigured(dir); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCheckUserConfigured_Missing(t *testing.T) {
	dir := makeRepo(t)
	// Isolate from the developer's real global git config so the only
	// identity source is the repo's --local config we just set in
	// makeRepo. Then unset those local keys so the check fails.
	t.Setenv("GIT_CONFIG_GLOBAL", devNullPath())
	t.Setenv("GIT_CONFIG_SYSTEM", devNullPath())
	for _, key := range []string{"user.email", "user.name"} {
		cmd := exec.Command("git", "config", "--local", "--unset", key) //nolint:gosec
		cmd.Dir = dir
		_ = cmd.Run()
	}
	err := gitcommit.CheckUserConfigured(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !gitcommit.IsNotConfigured(err) {
		t.Errorf("expected IsNotConfigured to be true, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// CommitAction
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestCommitAction_HappyPath(t *testing.T) {
	dir := makeRepo(t)
	writeFile(t, dir, "hello.txt", "first content")

	res, err := gitcommit.CommitAction(dir,
		[]string{"hello.txt"},
		gitcommit.CommitMessage{
			EntryID:    42,
			Intent:     "create hello world file",
			ToolName:   "write_file",
			SessionPath: "sessions/test.jsonl",
		},
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if res.NoChanges {
		t.Error("expected NoChanges=false")
	}
	if res.Hash == "" {
		t.Error("expected non-empty hash")
	}

	// Verify the subject line was formatted as expected.
	subjectCmd := exec.Command("git", "log", "-1", "--pretty=%s") //nolint:gosec
	subjectCmd.Dir = dir
	out, err := subjectCmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	subject := strings.TrimSpace(string(out))
	if !strings.HasPrefix(subject, "[triad] entry #42:") {
		t.Errorf("expected subject to start with '[triad] entry #42:', got %q", subject)
	}
	if !strings.Contains(subject, "create hello world file") {
		t.Errorf("expected subject to include intent, got %q", subject)
	}
}

func TestCommitAction_NoChanges(t *testing.T) {
	dir := makeRepo(t)
	// Create a file, commit it once.
	writeFile(t, dir, "stable.txt", "stable content")
	if _, err := gitcommit.CommitAction(dir,
		[]string{"stable.txt"},
		gitcommit.CommitMessage{EntryID: 1, Intent: "first commit", ToolName: "write_file"},
	); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	// Now commit the same file with identical content — should be a no-op.
	res, err := gitcommit.CommitAction(dir,
		[]string{"stable.txt"},
		gitcommit.CommitMessage{EntryID: 2, Intent: "second commit", ToolName: "write_file"},
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !res.NoChanges {
		t.Error("expected NoChanges=true for identical content")
	}
}

func TestCommitAction_NotConfigured(t *testing.T) {
	dir := makeRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", devNullPath())
	t.Setenv("GIT_CONFIG_SYSTEM", devNullPath())
	for _, key := range []string{"user.email", "user.name"} {
		cmd := exec.Command("git", "config", "--local", "--unset", key) //nolint:gosec
		cmd.Dir = dir
		_ = cmd.Run()
	}
	writeFile(t, dir, "no_user.txt", "x")

	res, err := gitcommit.CommitAction(dir,
		[]string{"no_user.txt"},
		gitcommit.CommitMessage{EntryID: 1, Intent: "should fail", ToolName: "write_file"},
	)
	if !gitcommit.IsNotConfigured(err) {
		t.Errorf("expected IsNotConfigured, got %v", err)
	}
	if !res.NotConfigured {
		t.Error("expected res.NotConfigured to be true")
	}
}

func TestCommitAction_EmptyPaths(t *testing.T) {
	dir := makeRepo(t)
	res, err := gitcommit.CommitAction(dir, nil,
		gitcommit.CommitMessage{EntryID: 1, ToolName: "write_file"},
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !res.NoChanges {
		t.Error("expected NoChanges=true for empty paths")
	}
}

// ---------------------------------------------------------------------------
// ChangedPaths
// ---------------------------------------------------------------------------

func TestChangedPaths_AfterWriteFile(t *testing.T) {
	dir := makeRepo(t)
	writeFile(t, dir, "fresh.txt", "x")
	// Also modify the file we created to make sure it's reported.

	paths, err := gitcommit.ChangedPaths(dir)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}
	found := false
	for _, p := range paths {
		if p == "fresh.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fresh.txt in changes, got %v", paths)
	}
}

func TestChangedPaths_MultipleFiles(t *testing.T) {
	dir := makeRepo(t)
	writeFile(t, dir, "a.txt", "1")
	writeFile(t, dir, "subdir/b.txt", "2")
	writeFile(t, dir, "subdir/deep/c.txt", "3")

	paths, err := gitcommit.ChangedPaths(dir)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}

	want := map[string]bool{
		"a.txt":           false,
		"subdir/b.txt":    false,
		"subdir/deep/c.txt": false,
	}
	for _, p := range paths {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected %q in changes, got %v", name, paths)
		}
	}
}

// ---------------------------------------------------------------------------
// /undo
// ---------------------------------------------------------------------------

func TestLastTriadCommit(t *testing.T) {
	dir := makeRepo(t)

	// No triad commit yet → empty hash, no error.
	hash, err := gitcommit.LastTriadCommit(dir)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if hash != "" {
		t.Errorf("expected empty hash, got %q", hash)
	}

	// Make a non-triad commit first.
	writeFile(t, dir, "manual.txt", "manual")
	manualCmd := exec.Command("git", "add", ".") //nolint:gosec
	manualCmd.Dir = dir
	_ = manualCmd.Run()
	manualCommit := exec.Command("git", "commit", "-m", "manual commit by user") //nolint:gosec
	manualCommit.Dir = dir
	_ = manualCommit.Run()

	// Then a triad commit.
	writeFile(t, dir, "auto.txt", "auto")
	if _, err := gitcommit.CommitAction(dir,
		[]string{"auto.txt"},
		gitcommit.CommitMessage{EntryID: 7, Intent: "auto", ToolName: "write_file"},
	); err != nil {
		t.Fatalf("CommitAction: %v", err)
	}

	hash, err = gitcommit.LastTriadCommit(dir)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestRevertLast_NoTriadCommit(t *testing.T) {
	dir := makeRepo(t)
	_, err := gitcommit.RevertLast(dir)
	if err == nil {
		t.Fatal("expected error when no triad commit exists")
	}
	if !strings.Contains(err.Error(), "nothing to undo") {
		t.Errorf("expected 'nothing to undo' message, got %q", err.Error())
	}
}

func TestRevertLast_Roundtrip(t *testing.T) {
	dir := makeRepo(t)
	writeFile(t, dir, "subject.txt", "the content")

	if _, err := gitcommit.CommitAction(dir,
		[]string{"subject.txt"},
		gitcommit.CommitMessage{EntryID: 99, Intent: "create subject", ToolName: "write_file"},
	); err != nil {
		t.Fatalf("CommitAction: %v", err)
	}

	// Confirm the file exists.
	if _, err := os.Stat(filepath.Join(dir, "subject.txt")); err != nil {
		t.Fatalf("expected file to exist before revert: %v", err)
	}

	res, err := gitcommit.RevertLast(dir)
	if err != nil {
		t.Fatalf("RevertLast: %v", err)
	}
	if res.Conflict {
		t.Error("expected no conflict on clean revert")
	}

	// The file should be gone after the revert.
	if _, err := os.Stat(filepath.Join(dir, "subject.txt")); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed after revert, stat err: %v", err)
	}

	// A revert commit should now be the most recent commit, and the
	// previous triad commit should still be in history.
	logCmd := exec.Command("git", "log", "--pretty=%s", "-n", "5") //nolint:gosec
	logCmd.Dir = dir
	out, _ := logCmd.Output()
	log := string(out)
	if !strings.Contains(log, "Revert") {
		t.Errorf("expected a Revert commit in log, got:\n%s", log)
	}
	if !strings.Contains(log, "[triad]") {
		t.Errorf("expected the original [triad] commit to still be in history, got:\n%s", log)
	}
}

// ---------------------------------------------------------------------------
// Message formatting
// ---------------------------------------------------------------------------

func TestFormatSubject(t *testing.T) {
	s := gitcommit.FormatSubject(gitcommit.CommitMessage{
		EntryID:  42,
		Intent:   "add HMAC signature verification",
		ToolName: "write_file",
	})
	if !strings.HasPrefix(s, "[triad] entry #42:") {
		t.Errorf("unexpected subject prefix: %q", s)
	}
	if !strings.Contains(s, "HMAC") {
		t.Errorf("expected intent in subject, got: %q", s)
	}
}

func TestFormatSubject_LongIntentTruncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	s := gitcommit.FormatSubject(gitcommit.CommitMessage{
		EntryID: 1,
		Intent:  long,
	})
	// The subject itself can exceed 100 chars (the cap is on the
	// *intent*, not the whole subject line), but it should still
	// end with the truncation marker so a human reader can see it
	// was shortened.
	if !strings.HasSuffix(s, "...") {
		t.Errorf("expected truncation marker at end, got length %d: %q", len(s), s)
	}
	// And the intent should be capped near 90 chars (87 + "...").
	prefix := "[triad] entry #1: "
	tail := strings.TrimPrefix(s, prefix)
	if !strings.HasSuffix(tail, "...") {
		t.Errorf("expected tail to end with ..., got: %q", tail)
	}
	beforeMarker := strings.TrimSuffix(tail, "...")
	if len(beforeMarker) > 90 {
		t.Errorf("expected intent portion ≤ 90 chars, got %d: %q", len(beforeMarker), beforeMarker)
	}
}

func TestFormatBody(t *testing.T) {
	body := gitcommit.FormatBody(gitcommit.CommitMessage{
		EntryID:     42,
		ToolName:    "write_file",
		ProposedBy:  "Coder",
		ApprovedBy:  "Reviewer",
		SessionPath: "sessions/x.jsonl",
	})
	for _, want := range []string{"Proposed by: Coder", "Approved by: Reviewer", "Session: sessions/x.jsonl", "Tool: write_file"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

// ---------------------------------------------------------------------------
// /summary tests
// ---------------------------------------------------------------------------

func TestGetSessionSummary_ZeroCommits(t *testing.T) {
	dir := makeRepo(t)

	summary, err := gitcommit.GetSessionSummary(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.CommitCount != 0 {
		t.Errorf("expected 0 commits, got %d", summary.CommitCount)
	}
	if len(summary.FilesTouched) != 0 {
		t.Errorf("expected 0 files touched, got %d", len(summary.FilesTouched))
	}
}

func TestGetSessionSummary_TaggedCommits(t *testing.T) {
	dir := makeRepo(t)

	writeFile(t, dir, "a.txt", "line1\nline2\n")
	msg1 := gitcommit.CommitMessage{EntryID: 1, Intent: "add a.txt", ToolName: "write_file"}
	res1, err := gitcommit.CommitAction(dir, []string{"a.txt"}, msg1)
	if err != nil || res1.Hash == "" {
		t.Fatalf("CommitAction 1 failed: %v", err)
	}

	writeFile(t, dir, "b.txt", "hello world\n")
	msg2 := gitcommit.CommitMessage{EntryID: 2, Intent: "add b.txt", ToolName: "write_file"}
	res2, err := gitcommit.CommitAction(dir, []string{"b.txt"}, msg2)
	if err != nil || res2.Hash == "" {
		t.Fatalf("CommitAction 2 failed: %v", err)
	}

	validIDs := map[int]bool{1: true, 2: true}
	summary, err := gitcommit.GetSessionSummary(dir, validIDs)
	if err != nil {
		t.Fatalf("GetSessionSummary failed: %v", err)
	}

	if summary.CommitCount != 2 {
		t.Errorf("CommitCount: got %d, want 2", summary.CommitCount)
	}
	if len(summary.FilesTouched) != 2 || summary.FilesTouched[0] != "a.txt" || summary.FilesTouched[1] != "b.txt" {
		t.Errorf("FilesTouched: got %v, want [a.txt b.txt]", summary.FilesTouched)
	}
	if summary.LinesAdded < 3 {
		t.Errorf("LinesAdded: got %d, expected at least 3", summary.LinesAdded)
	}
}

func TestGetSessionSummary_MixedRepo(t *testing.T) {
	dir := makeRepo(t)

	// Manual non-Triad commit
	writeFile(t, dir, "manual.txt", "user manual work\n")
	cmd := exec.Command("git", "add", "manual.txt")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "user manual commit")
	cmd.Dir = dir
	_ = cmd.Run()

	// Triad commit
	writeFile(t, dir, "triad.txt", "triad work\n")
	msg := gitcommit.CommitMessage{EntryID: 5, Intent: "add triad.txt", ToolName: "write_file"}
	_, err := gitcommit.CommitAction(dir, []string{"triad.txt"}, msg)
	if err != nil {
		t.Fatalf("CommitAction failed: %v", err)
	}

	validIDs := map[int]bool{5: true}
	summary, err := gitcommit.GetSessionSummary(dir, validIDs)
	if err != nil {
		t.Fatalf("GetSessionSummary failed: %v", err)
	}

	if summary.CommitCount != 1 {
		t.Errorf("CommitCount: got %d, want 1", summary.CommitCount)
	}
	if len(summary.FilesTouched) != 1 || summary.FilesTouched[0] != "triad.txt" {
		t.Errorf("FilesTouched: got %v, want [triad.txt]", summary.FilesTouched)
	}
}

