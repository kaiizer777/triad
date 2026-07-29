package agent

import (
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/logger"
)

// ---------------------------------------------------------------------------
// Retry configuration
// ---------------------------------------------------------------------------

// RetryMaxAttempts is the default maximum number of attempts before giving up.
// The first attempt is the normal execution; retries are attempts 2..N.
const RetryMaxAttempts = 5

// RetryBaseDelay is the base delay for exponential backoff between retries.
// Actual delay = RetryBaseDelay * 2^(attempt-1), capped at 5 seconds.
const RetryBaseDelay = 200 * time.Millisecond

// RetryMaxDelay caps the exponential backoff so a runaway timeout sequence
// doesn't stall the session for minutes.
const RetryMaxDelay = 5 * time.Second

// ---------------------------------------------------------------------------
// Retry callback — surfaces per-attempt progress to the caller
// ---------------------------------------------------------------------------

// RetryCallback is called during retry attempts to surface progress to the
// caller (transcript, TUI status bar, etc.). It is never called on the first
// attempt (success or non-retryable failure) or on the final failure.
//   - attempt: the attempt number that just failed (1-indexed)
//   - maxAttempts: the configured maximum
//   - err: the error from the failed attempt
type RetryCallback func(attempt int, maxAttempts int, err error)

// ---------------------------------------------------------------------------
// Retry options
// ---------------------------------------------------------------------------

// RetryOptions configures the retry behavior for ExecuteWithRetry.
type RetryOptions struct {
	// MaxAttempts is the total number of attempts (including the first).
	// 0 or negative uses RetryMaxAttempts.
	MaxAttempts int
	// BaseDelay is the base delay for exponential backoff.
	// 0 uses RetryBaseDelay.
	BaseDelay time.Duration
	// OnRetry is called on each failed retry attempt to surface progress.
	// nil is safe — retry progress is logged but not surfaced to the caller.
	OnRetry RetryCallback
}

// DefaultRetryOptions returns a RetryOptions with sensible defaults.
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxAttempts: RetryMaxAttempts,
		BaseDelay:   RetryBaseDelay,
	}
}

// ---------------------------------------------------------------------------
// ExecuteWithRetry — the shared retry wrapper
// ---------------------------------------------------------------------------

// ExecuteWithRetry wraps a tool execution function with retry logic.
// It calls fn() up to opts.MaxAttempts times, classifying each failure as
// retryable or non-retryable. Non-retryable errors fail fast without burning
// remaining attempts. Retryable errors trigger exponential backoff between
// attempts.
//
// On each retryable failure, opts.OnRetry is called (if non-nil) to let the
// caller surface progress to the transcript/TUI. The function logs every
// attempt internally via the logger.
//
// Returns the result and error from the final attempt (or the first
// non-retryable error).
func ExecuteWithRetry(opts RetryOptions, fn func() (string, error)) (string, error) {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = RetryMaxAttempts
	}
	baseDelay := opts.BaseDelay
	if baseDelay <= 0 {
		baseDelay = RetryBaseDelay
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			if attempt > 1 {
				logger.L().Info("retry succeeded",
					"attempt", attempt,
					"max_attempts", maxAttempts,
				)
			}
			return result, nil
		}

		lastErr = err

		// Classify: is this error retryable?
		if !IsRetryableError(err) {
			logger.L().Debug("non-retryable error, failing fast",
				"attempt", attempt,
				"error", err.Error(),
			)
			return result, err
		}

		// Last attempt — don't schedule another retry.
		if attempt >= maxAttempts {
			logger.L().Warn("all retry attempts exhausted",
				"max_attempts", maxAttempts,
				"last_error", err.Error(),
			)
			break
		}

		// Notify the caller (transcript/TUI) about the retry.
		if opts.OnRetry != nil {
			opts.OnRetry(attempt, maxAttempts, err)
		}

		// Exponential backoff: baseDelay * 2^(attempt-1), capped.
		delay := baseDelay * time.Duration(1<<uint(attempt-1))
		if delay > RetryMaxDelay {
			delay = RetryMaxDelay
		}

		logger.L().Warn("tool execution failed, retrying",
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"retry_in", delay.String(),
			"error", err.Error(),
		)

		time.Sleep(delay)
	}

	return "", &RetryExhaustedError{
		MaxAttempts: maxAttempts,
		LastErr:     lastErr,
	}
}

// ---------------------------------------------------------------------------
// RetryExhaustedError — returned when all attempts are exhausted
// ---------------------------------------------------------------------------

// RetryExhaustedError is returned when all retry attempts have been exhausted
// on a retryable error. It wraps the last underlying error.
type RetryExhaustedError struct {
	MaxAttempts int
	LastErr     error
}

func (e *RetryExhaustedError) Error() string {
	return FormatRetryExhaustedError(e.MaxAttempts, e.LastErr)
}

func (e *RetryExhaustedError) Unwrap() error {
	return e.LastErr
}

// FormatRetryExhaustedError produces the human-readable final error message
// shown in the transcript when retries are exhausted.
func FormatRetryExhaustedError(maxAttempts int, lastErr error) string {
	var b strings.Builder
	b.WriteString("tool failed after ")
	// maxAttempts is always a small integer; format manually to avoid
	// importing strconv or fmt just for this.
	n := maxAttempts
	if n <= 0 {
		b.WriteByte('0')
	} else {
		var buf [8]byte
		pos := len(buf)
		for n > 0 {
			pos--
			buf[pos] = byte('0' + n%10)
			n /= 10
		}
		b.Write(buf[pos:])
	}
	b.WriteString(" attempts")
	if lastErr != nil {
		b.WriteString(": ")
		b.WriteString(lastErr.Error())
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Error classification — retryable vs non-retryable
// ---------------------------------------------------------------------------

// IsRetryableError determines whether an error is transient and worth retrying.
//
// Retryable errors (transient/environment):
//   - Command timeouts (context.DeadlineExceeded wrapped in run_command)
//   - Temporary I/O errors (os.ErrTemporary or "temporarily unavailable")
//   - Network/HTTP errors ("connection refused", "timeout", "EOF" in browser/API calls)
//   - File locking / sharing violations on Windows ("being used by another process")
//   - Browser navigation timeouts
//
// Non-retryable errors (logic/permanent):
//   - Path safety violations ("absolute paths are not allowed", ".." traversal)
//   - Missing files ("file does not exist", "no such file")
//   - Permission denied
//   - Invalid tool arguments (malformed JSON, missing required fields)
//   - Unknown tool names
//   - Intercepted tool errors (spawn_subagent must be intercepted, etc.)
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	// --- Non-retryable patterns (fail fast) ---

	// Path safety violations
	if strings.Contains(msg, "absolute paths are not allowed") {
		return false
	}
	if strings.Contains(msg, "traversal is not allowed") {
		return false
	}
	if strings.Contains(msg, "outside working directory") {
		return false
	}

	// Missing files / not found
	if strings.Contains(msg, "no such file") {
		return false
	}
	if strings.Contains(msg, "file does not exist") {
		return false
	}
	if strings.Contains(msg, "cannot find the file") { // Windows phrasing
		return false
	}
	if strings.Contains(msg, "the system cannot find the path specified") { // Windows
		return false
	}
	if strings.Contains(msg, "cannot find the path") { // Windows
		return false
	}

	// Permission denied
	if strings.Contains(msg, "permission denied") {
		return false
	}
	if strings.Contains(msg, "access is denied") { // Windows
		return false
	}

	// Invalid arguments / malformed tool calls
	if strings.Contains(msg, "malformed tool call") {
		return false
	}
	if strings.Contains(msg, "required argument") {
		return false
	}
	if strings.Contains(msg, "unknown tool") {
		return false
	}

	// Intercepted tool errors (logic errors, not transient)
	if strings.Contains(msg, "must be intercepted by the caller") {
		return false
	}

	// Syntax errors in commands
	if strings.Contains(msg, "is not recognized as an internal or external command") { // Windows
		return false
	}

	// --- Retryable patterns (transient/environment) ---

	// Timeouts
	if strings.Contains(msg, "timed out") {
		return true
	}
	if strings.Contains(msg, "timeout") {
		return true
	}
	if strings.Contains(msg, "deadline exceeded") {
		return true
	}

	// Network / connection errors
	if strings.Contains(msg, "connection refused") {
		return true
	}
	if strings.Contains(msg, "connection reset") {
		return true
	}
	if strings.Contains(msg, "connection was refused") {
		return true
	}
	if strings.Contains(msg, "eof") && !strings.Contains(msg, "syntax") {
		return true
	}
	if strings.Contains(msg, "broken pipe") {
		return true
	}

	// Temporary / transient I/O
	if strings.Contains(msg, "temporarily unavailable") {
		return true
	}
	if strings.Contains(msg, "resource temporarily unavailable") {
		return true
	}

	// Windows file locking / sharing violations
	if strings.Contains(msg, "being used by another process") {
		return true
	}
	if strings.Contains(msg, "sharing violation") {
		return true
	}
	if strings.Contains(msg, "lock violation") {
		return true
	}

	// Browser-specific transient errors
	if strings.Contains(msg, "navigation failed") {
		return true
	}
	if strings.Contains(msg, "page crashed") {
		return true
	}
	if strings.Contains(msg, "target closed") {
		return true
	}
	if strings.Contains(msg, "session closed") {
		return true
	}

	// Default: treat unknown errors as non-retryable to avoid wasting
	// retries on permanent failures we haven't categorized yet.
	return false
}
