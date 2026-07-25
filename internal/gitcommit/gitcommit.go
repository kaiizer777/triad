// Package gitcommit implements automatic per-action git commits and /undo for
// the Triad session. See docs/work2.md §2.
//
// Design summary:
//   - Every executed write_file / run_command action gets a single commit
//     whose subject is "[triad] entry #N: <short intent>", listing exactly
//     the file(s) the action touched.
//   - Rejected proposals never touch git, by construction: the loop only
//     calls CommitAction after a tool has been executed and produced a
//     non-empty result.
//   - A no-op (file written with identical content, or a command that
//     changes nothing) is skipped silently — no empty commits are created.
//   - Missing git user.name / user.email is surfaced once as a System
//     transcript entry and the auto-commit call returns ErrNotConfigured,
//     so the session keeps running instead of crashing on every action.
//   - /undo is implemented as `git revert --no-edit` against the most
//     recent commit whose subject begins with the [triad] marker, leaving
//     a new commit in history (no destructive resets).
package gitcommit

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kaiizer777/triad/internal/logger"
)

// CommitSubjectPrefix is the marker that identifies an auto-commit made by
// Triad in the repository's history. Used by LastTriadCommit and RevertLast.
const CommitSubjectPrefix = "[triad]"

// CommitMessage holds the pieces that go into a Triad auto-commit's message.
type CommitMessage struct {
	// EntryID is the transcript entry ID of the action_result that the
	// commit is recording (i.e. the System entry that just landed in the
	// transcript before this commit is created).
	EntryID int
	// Intent is a short, human-readable description of what the action
	// was trying to do (e.g. "add HMAC signature verification"). This is
	// the excerpt shown in the subject line.
	Intent string
	// ToolName is the name of the executed tool (write_file, run_command).
	ToolName string
	// SessionPath is the relative or absolute path to the JSONL transcript
	// that this action came from. Recorded in the commit body so a future
	// reader can correlate a git change back to the agent's reasoning.
	SessionPath string
	// ProposedBy is the speaker who proposed the action (always "Coder"
	// in v1, kept as a field for forward-compat).
	ProposedBy string
	// ApprovedBy is the speaker who approved the action (always "Reviewer"
	// in v1, kept as a field for forward-compat).
	ApprovedBy string
}

// FormatSubject builds the single-line subject of the commit.
func FormatSubject(msg CommitMessage) string {
	intent := strings.TrimSpace(msg.Intent)
	if intent == "" {
		intent = msg.ToolName
	}
	// Cap subject length so git doesn't complain on some platforms.
	const maxSubject = 90
	if len(intent) > maxSubject {
		intent = intent[:maxSubject-3] + "..."
	}
	return fmt.Sprintf("%s entry #%d: %s", CommitSubjectPrefix, msg.EntryID, intent)
}

// FormatBody builds the multi-line body of the commit message, including
// the proposed-by / approved-by / session-path metadata.
func FormatBody(msg CommitMessage) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Proposed by: %s\n", fallback(msg.ProposedBy, "Coder")))
	b.WriteString(fmt.Sprintf("Approved by: %s\n", fallback(msg.ApprovedBy, "Reviewer")))
	b.WriteString(fmt.Sprintf("Session: %s\n", fallback(msg.SessionPath, "(unknown)")))
	b.WriteString(fmt.Sprintf("Tool: %s\n", fallback(msg.ToolName, "(unknown)")))
	return b.String()
}

// FormatFullMessage concatenates subject + blank line + body in the form
// git expects for `git commit -m`.
func FormatFullMessage(msg CommitMessage) string {
	return FormatSubject(msg) + "\n" + FormatBody(msg)
}

func fallback(s, dflt string) string {
	if strings.TrimSpace(s) == "" {
		return dflt
	}
	return s
}

// ---------------------------------------------------------------------------
// Repository bootstrap
// ---------------------------------------------------------------------------

// ErrAlreadyRepo is returned by EnsureRepo when the working directory is
// already a git repository (i.e. nothing was done).
type ErrAlreadyRepo struct{}

func (ErrAlreadyRepo) Error() string { return "already a git repository" }

// EnsureRepo makes sure workDir is a git repository. If it isn't, runs
// `git init` and returns the init's stdout/stderr in a wrapped error.
// If it is, returns ErrAlreadyRepo to signal that nothing was done.
//
// The "already a repo" case is not really an error — callers typically
// type-assert and ignore it.
func EnsureRepo(workDir string) error {
	if IsRepo(workDir) {
		return ErrAlreadyRepo{}
	}

	cmd := exec.Command("git", "init") //nolint:gosec // intentional local git invocation
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gitcommit: git init failed in %q: %w (output: %s)", workDir, err, out.String())
	}

	logger.L().Info("gitcommit: initialised new repository", "workDir", workDir)
	return nil
}

// IsRepo reports whether workDir is already inside a git working tree.
// It uses `git rev-parse --is-inside-work-tree` which returns 0 + "true"
// when inside a repo and non-zero otherwise — a more permissive check
// than looking for a .git directory directly, since it also handles
// submodules and worktrees correctly.
func IsRepo(workDir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree") //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) == "true"
}

// ---------------------------------------------------------------------------
// User identity check
// ---------------------------------------------------------------------------

// ErrNotConfigured is returned by CommitAction when git is installed but
// user.name / user.email are not set, so a commit would fail. Callers
// should surface this once and then keep running — subsequent actions
// still produce transcript entries, they just don't get committed.
type ErrNotConfigured struct {
	Reason string
}

func (e *ErrNotConfigured) Error() string {
	return "git user not configured: " + e.Reason
}

// IsNotConfigured reports whether err is (or wraps) ErrNotConfigured.
func IsNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*ErrNotConfigured)
	return ok
}

// CheckUserConfigured reports whether git has a user.name and user.email
// available (either via repo config, global config, or env vars). If not,
// returns an *ErrNotConfigured describing what's missing.
//
// We use `git config --get` with no scope flag, which respects git's
// normal resolution order (local → global → system). For Triad's
// per-action use this is the right behavior: in production, the user's
// real global config is what should satisfy the check.
func CheckUserConfigured(workDir string) error {
	name := gitConfig(workDir, "user.name")
	email := gitConfig(workDir, "user.email")
	switch {
	case strings.TrimSpace(name) == "" && strings.TrimSpace(email) == "":
		return &ErrNotConfigured{Reason: "both user.name and user.email are unset (set them with `git config --global user.name/email`)"}
	case strings.TrimSpace(name) == "":
		return &ErrNotConfigured{Reason: "user.name is unset (set with `git config --global user.name \"Your Name\"`)"}
	case strings.TrimSpace(email) == "":
		return &ErrNotConfigured{Reason: "user.email is unset (set with `git config --global user.email \"you@example.com\"`)"}
	}
	return nil
}

func gitConfig(workDir, key string) string {
	cmd := exec.Command("git", "config", "--get", key) //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Non-zero exit is fine — it just means the key isn't set.
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

// ---------------------------------------------------------------------------
// Change detection (for run_command)
// ---------------------------------------------------------------------------

// ChangedPaths returns the set of file paths that have uncommitted changes
// in workDir, relative to the current HEAD. Intended for use after a
// run_command action executes, to discover which file(s) it touched.
//
// Output format follows `git status --porcelain`. Each line looks like:
//   " M path/to/file"
//   "?? new/file"
//   "A  staged/file"
//   "MM modified-and-staged/file"
// The first two characters (XY) are status; everything after the spaces
// is the path (with quotes around it if it contains special chars).
//
// Untracked directories' contents are NOT listed (we pass -uall to include
// them, matching the intuitive expectation that touching a brand-new file
// counts as a change). Renames show as "R  old -> new" — we keep "new".
func ChangedPaths(workDir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "-uall") //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitcommit: git status failed in %q: %w (output: %s)", workDir, err, out.String())
	}

	var paths []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Format: "XY path" where XY is exactly 2 chars (plus leading space).
		// Path can be quoted if it contains special chars.
		path := extractPathFromPorcelain(line)
		if path == "" {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func extractPathFromPorcelain(line string) string {
	// Trim trailing CR for Windows line endings.
	line = strings.TrimRight(line, "\r")
	if len(line) < 4 {
		// "XY " is 3 chars minimum, plus at least one char of path.
		return ""
	}
	// Drop the 2-char status and following space(s).
	rest := line[3:]
	// If path is quoted (contains spaces or special chars), unquote it.
	if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
		rest = rest[1 : len(rest)-1]
	}
	// For renames/copies, status is "R " or "C " and rest is "old -> new";
	// keep the new path.
	if idx := strings.Index(rest, " -> "); idx != -1 {
		rest = rest[idx+len(" -> "):]
	}
	return rest
}

// ---------------------------------------------------------------------------
// Commit
// ---------------------------------------------------------------------------

// CommitResult describes the outcome of CommitAction.
type CommitResult struct {
	// Hash is the short hash of the commit that was created, or "" if no
	// commit was made (NoChanges or NotConfigured).
	Hash string
	// NoChanges is true when CommitAction was called but the workDir has
	// no actual diff for the listed paths (e.g. write_file with identical
	// content) — the commit was skipped deliberately.
	NoChanges bool
	// NotConfigured mirrors the IsNotConfigured flag for caller convenience.
	NotConfigured bool
}

// CommitAction stages the given file paths (relative to workDir) and
// creates a single commit with msg as its message. paths must be non-empty
// and must each exist relative to workDir (or be already-tracked).
//
// Behaviour:
//   - Empty paths → returns NoChanges=true, no error.
//   - git user not configured → returns *ErrNotConfigured (caller should
//     surface as a System transcript entry and stop trying to commit;
//     session keeps running).
//   - Any other git failure → returns the wrapped error.
//   - On success, returns the short commit hash and NoChanges=false.
func CommitAction(workDir string, paths []string, msg CommitMessage) (CommitResult, error) {
	// Filter out any empty paths the caller might have passed in.
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		logger.L().Debug("gitcommit: no paths to commit, skipping",
			"workDir", workDir, "entryID", msg.EntryID)
		return CommitResult{NoChanges: true}, nil
	}

	// Check user config first — a missing user.name/email is a permanent
	// failure on this machine, no point in staging files we'll never commit.
	if err := CheckUserConfigured(workDir); err != nil {
		return CommitResult{NotConfigured: true}, err
	}

	// Stage exactly the listed paths (never `git add .`).
	args := append([]string{"add", "--"}, cleaned...)
	addCmd := exec.Command("git", args...) //nolint:gosec
	addCmd.Dir = workDir
	var addOut bytes.Buffer
	addCmd.Stdout = &addOut
	addCmd.Stderr = &addOut
	if err := addCmd.Run(); err != nil {
		return CommitResult{}, fmt.Errorf("gitcommit: git add failed in %q: %w (output: %s)", workDir, err, addOut.String())
	}

	// Check whether the staging area actually differs from HEAD. If the
	// caller wrote identical content, `git add` is a no-op and `git diff
	// --cached --quiet` exits 0 — we treat that as "no changes" and
	// skip the commit entirely.
	diffCmd := exec.Command("git", "diff", "--cached", "--quiet") //nolint:gosec
	diffCmd.Dir = workDir
	if err := diffCmd.Run(); err == nil {
		// Exit 0 → no staged changes → nothing to commit.
		logger.L().Debug("gitcommit: no staged changes after add, skipping commit",
			"workDir", workDir, "entryID", msg.EntryID)
		return CommitResult{NoChanges: true}, nil
	}

	// Build and run the commit.
	full := FormatFullMessage(msg)
	commitCmd := exec.Command("git", "commit", "-m", full) //nolint:gosec
	commitCmd.Dir = workDir
	var commitOut bytes.Buffer
	commitCmd.Stdout = &commitOut
	commitCmd.Stderr = &commitOut
	if err := commitCmd.Run(); err != nil {
		return CommitResult{}, fmt.Errorf("gitcommit: git commit failed in %q: %w (output: %s)", workDir, err, commitOut.String())
	}

	// Get the short hash so callers can include it in the System entry.
	hash, hashErr := shortHeadHash(workDir)
	if hashErr != nil {
		// Commit succeeded but we couldn't read the hash — not fatal,
		// just log and return an empty hash.
		logger.L().Warn("gitcommit: commit succeeded but could not read hash",
			"workDir", workDir, "error", hashErr.Error())
		return CommitResult{}, nil
	}

	logger.L().Info("gitcommit: created commit",
		"workDir", workDir,
		"entryID", msg.EntryID,
		"hash", hash,
		"paths", cleaned,
	)
	return CommitResult{Hash: hash}, nil
}

func shortHeadHash(workDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD") //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// ---------------------------------------------------------------------------
// /undo support
// ---------------------------------------------------------------------------

// LastTriadCommit returns the short hash of the most recent commit in
// workDir whose subject begins with the Triad marker, or "" if no such
// commit exists in history.
//
// We walk the log from HEAD backward, comparing each subject's prefix
// (case-sensitive — keep it consistent with how we write it). This is
// done in-process instead of via `git log --grep` because we want the
// *first* match, and `git log --grep` semantics are "all matches" with
// a different flag combination needed for "first only".
func LastTriadCommit(workDir string) (string, error) {
	logCmd := exec.Command("git", "log", "--pretty=%H %s", "-n", "50") //nolint:gosec
	logCmd.Dir = workDir
	var out bytes.Buffer
	logCmd.Stdout = &out
	logCmd.Stderr = &out
	if err := logCmd.Run(); err != nil {
		return "", fmt.Errorf("gitcommit: git log failed in %q: %w (output: %s)", workDir, err, out.String())
	}

	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// Format: "<hash> <subject>". The hash has no spaces.
		sp := strings.IndexByte(line, ' ')
		if sp <= 0 {
			continue
		}
		hash := line[:sp]
		subject := strings.TrimSpace(line[sp+1:])
		if strings.HasPrefix(subject, CommitSubjectPrefix) {
			return hash, nil
		}
	}
	return "", nil
}

// RevertResult captures the outcome of a /undo for surfacing in the
// transcript.
type RevertResult struct {
	OriginalHash    string
	RevertCommitMsg string
	Conflict        bool // true if the revert produced a merge conflict
}

// RevertLast reverts the most recent Triad commit (as identified by
// LastTriadCommit) using `git revert --no-edit`. The revert itself is
// committed, so the history of what was done and what was undone is
// preserved.
//
// If there is no Triad commit to revert, returns an error so the caller
// can surface "nothing to undo" in the transcript. If workDir isn't
// even a git repo, returns the same "nothing to undo" error rather than
// surfacing a raw "fatal: not a git repository" — that's the user-facing
// message that makes sense in both situations.
func RevertLast(workDir string) (RevertResult, error) {
	if !IsRepo(workDir) {
		return RevertResult{}, fmt.Errorf("nothing to undo: %q is not a git repository", workDir)
	}
	hash, err := LastTriadCommit(workDir)
	if err != nil {
		return RevertResult{}, err
	}
	if hash == "" {
		return RevertResult{}, fmt.Errorf("nothing to undo: no %q commits found in %q", CommitSubjectPrefix, workDir)
	}

	cmd := exec.Command("git", "revert", "--no-edit", hash) //nolint:gosec
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// A merge conflict during revert is the most common failure mode.
		// Detect it by checking for the standard "CONFLICT" marker in
		// the output or by inspecting the exit status text.
		outStr := out.String()
		if strings.Contains(outStr, "CONFLICT") {
			return RevertResult{
				OriginalHash: hash,
				Conflict:     true,
			}, fmt.Errorf("gitcommit: revert of %s produced merge conflicts in %q (resolve manually with `git add` + `git revert --continue`, or abort with `git revert --abort`). Output: %s", hash, workDir, outStr)
		}
		return RevertResult{OriginalHash: hash}, fmt.Errorf("gitcommit: git revert of %s failed in %q: %w (output: %s)", hash, workDir, err, outStr)
	}

	// Read the message of the revert commit for the transcript.
	msgCmd := exec.Command("git", "log", "-1", "--pretty=%s", "HEAD") //nolint:gosec
	msgCmd.Dir = workDir
	var msgOut bytes.Buffer
	msgCmd.Stdout = &msgOut
	msgCmd.Stderr = &msgOut
	_ = msgCmd.Run()
	revertMsg := strings.TrimSpace(msgOut.String())

	logger.L().Info("gitcommit: reverted last triad commit",
		"workDir", workDir, "hash", hash, "revertMsg", revertMsg)

	return RevertResult{
		OriginalHash:    hash,
		RevertCommitMsg: revertMsg,
	}, nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// NormalizePath returns a cleaned, forward-slash path relative to workDir.
// We use forward slashes even on Windows so the paths we pass to `git`
// are portable and match what `git status` reports back.
func NormalizePath(workDir, relPath string) string {
	relPath = filepath.ToSlash(relPath)
	relPath = strings.TrimPrefix(relPath, "./")
	return relPath
}
