package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// IsRetryableError classification tests
// ---------------------------------------------------------------------------

func TestIsRetryableError_TransientErrors(t *testing.T) {
	retryableErrors := []struct {
		name string
		err  error
	}{
		{"timeout", errors.New("run_command: timed out after 30s")},
		{"deadline exceeded", errors.New("context deadline exceeded")},
		{"connection refused", errors.New("dial tcp: connection refused")},
		{"connection reset", errors.New("read: connection reset by peer")},
		{"broken pipe", errors.New("write: broken pipe")},
		{"EOF", errors.New("unexpected EOF")},
		{"temporarily unavailable", errors.New("resource temporarily unavailable")},
		{"sharing violation", errors.New("sharing violation: C:\\file.txt")},
		{"being used by another process", errors.New("file is being used by another process")},
		{"navigation failed", errors.New("navigation failed: timeout")},
		{"page crashed", errors.New("page crashed")},
		{"target closed", errors.New("target closed")},
		{"session closed", errors.New("session closed")},
		{"lock violation", errors.New("lock violation")},
	}

	for _, tc := range retryableErrors {
		t.Run(tc.name, func(t *testing.T) {
			if !IsRetryableError(tc.err) {
				t.Errorf("expected %q to be retryable", tc.err)
			}
		})
	}
}

func TestIsRetryableError_NonRetryableErrors(t *testing.T) {
	nonRetryableErrors := []struct {
		name string
		err  error
	}{
		{"absolute path", errors.New("path safety: absolute paths are not allowed (got \"C:\\file.txt\")")},
		{"dot-dot traversal", errors.New("path safety: '..' traversal is not allowed")},
		{"outside working dir", errors.New("path safety: resolved path is outside working directory")},
		{"no such file", errors.New("read_file: failed to read: no such file or directory")},
		{"file does not exist", errors.New("file does not exist")},
		{"cannot find file", errors.New("The system cannot find the file specified")},
		{"cannot find path", errors.New("The system cannot find the path specified")},
		{"permission denied", errors.New("open: permission denied")},
		{"access denied", errors.New("Access is denied")},
		{"malformed tool call", errors.New("malformed tool call arguments for \"write_file\"")},
		{"missing required arg", errors.New("write_file: required argument 'path' is missing or empty")},
		{"unknown tool", errors.New("ExecuteTool: unknown tool name \"delete_all\"")},
		{"intercepted tool", errors.New("ExecuteTool: spawn_subagent must be intercepted by the caller")},
		{"not recognized command", errors.New("'foo' is not recognized as an internal or external command")},
	}

	for _, tc := range nonRetryableErrors {
		t.Run(tc.name, func(t *testing.T) {
			if IsRetryableError(tc.err) {
				t.Errorf("expected %q to be non-retryable", tc.err)
			}
		})
	}
}

func TestIsRetryableError_NilError(t *testing.T) {
	if IsRetryableError(nil) {
		t.Error("expected nil error to be non-retryable")
	}
}

// ---------------------------------------------------------------------------
// ExecuteWithRetry tests
// ---------------------------------------------------------------------------

// TestExecuteWithRetry_TransientFailureRetriesAndSucceeds verifies that a
// transient failure retries up to 5 times and succeeds on a later attempt.
func TestExecuteWithRetry_TransientFailureRetriesAndSucceeds(t *testing.T) {
	attempts := 0
	retryCallbacks := 0

	result, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond, // fast for testing
		OnRetry: func(attempt, maxAttempts int, err error) {
			retryCallbacks++
			if attempt >= maxAttempts {
				t.Errorf("OnRetry should not be called on the last attempt")
			}
		},
	}, func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("connection refused") // retryable
		}
		return "success", nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got %q", result)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if retryCallbacks != 2 {
		t.Errorf("expected 2 retry callbacks (attempts 1 and 2), got %d", retryCallbacks)
	}
}

// TestExecuteWithRetry_TransientFailureExhaustsRetries verifies that a
// transient failure that never succeeds exhausts all 5 retries and returns
// a RetryExhaustedError.
func TestExecuteWithRetry_TransientFailureExhaustsRetries(t *testing.T) {
	attempts := 0
	retryCallbacks := 0

	_, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(attempt, maxAttempts int, err error) {
			retryCallbacks++
		},
	}, func() (string, error) {
		attempts++
		return "", errors.New("timed out after 30s") // always retryable
	})

	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}

	var retryErr *RetryExhaustedError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected RetryExhaustedError, got %T: %v", err, err)
	}
	if retryErr.MaxAttempts != 5 {
		t.Errorf("expected MaxAttempts=5, got %d", retryErr.MaxAttempts)
	}
	if !strings.Contains(retryErr.Error(), "5 attempts") {
		t.Errorf("expected error to mention '5 attempts', got: %v", retryErr)
	}

	if attempts != 5 {
		t.Errorf("expected 5 attempts, got %d", attempts)
	}
	if retryCallbacks != 4 {
		t.Errorf("expected 4 retry callbacks (attempts 1-4), got %d", retryCallbacks)
	}
}

// TestExecuteWithRetry_NonRetryableFailsFast verifies that a non-retryable
// error fails immediately without burning remaining retries.
func TestExecuteWithRetry_NonRetryableFailsFast(t *testing.T) {
	attempts := 0
	retryCallbacks := 0

	result, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(attempt, maxAttempts int, err error) {
			retryCallbacks++
		},
	}, func() (string, error) {
		attempts++
		return "", errors.New("path safety: absolute paths are not allowed") // non-retryable
	})

	if err == nil {
		t.Fatal("expected error for non-retryable failure, got nil")
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}

	// Should fail on first attempt — no retries for non-retryable errors.
	if attempts != 1 {
		t.Errorf("expected 1 attempt (fail fast), got %d", attempts)
	}
	if retryCallbacks != 0 {
		t.Errorf("expected 0 retry callbacks (non-retryable), got %d", retryCallbacks)
	}

	// Should NOT be a RetryExhaustedError — it's a direct failure.
	var retryErr *RetryExhaustedError
	if errors.As(err, &retryErr) {
		t.Error("non-retryable error should not produce RetryExhaustedError")
	}
}

// TestExecuteWithRetry_MixedRetryableAndNonRetryable verifies that a function
// that fails with retryable errors first, then a non-retryable error, stops
// immediately at the non-retryable error.
func TestExecuteWithRetry_MixedRetryableAndNonRetryable(t *testing.T) {
	attempts := 0

	_, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
	}, func() (string, error) {
		attempts++
		if attempts <= 2 {
			return "", errors.New("connection refused") // retryable
		}
		return "", errors.New("no such file or directory") // non-retryable
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should have tried 3 times: 2 retryable + 1 non-retryable (fail fast).
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	// The error should be the non-retryable one, NOT RetryExhaustedError.
	if strings.Contains(err.Error(), "attempts") {
		t.Errorf("expected direct error, not retry-exhausted: %v", err)
	}
}

// TestExecuteWithRetry_NilCallback verifies retry works without an OnRetry callback.
func TestExecuteWithRetry_NilCallback(t *testing.T) {
	attempts := 0

	result, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		OnRetry:     nil,
	}, func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("timeout")
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

// TestExecuteWithRetry_DefaultOptions verifies that zero-value RetryOptions
// uses defaults (5 attempts, default base delay).
func TestExecuteWithRetry_DefaultOptions(t *testing.T) {
	attempts := 0

	_, err := ExecuteWithRetry(RetryOptions{
		// MaxAttempts and BaseDelay are zero — should use defaults.
		BaseDelay: 1 * time.Millisecond, // override for speed
	}, func() (string, error) {
		attempts++
		return "", errors.New("timed out")
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Default max attempts is 5.
	if attempts != 5 {
		t.Errorf("expected 5 default attempts, got %d", attempts)
	}
}

// TestExecuteWithRetry_FirstAttemptSucceeds verifies that when the first
// attempt succeeds, no retry logic runs.
func TestExecuteWithRetry_FirstAttemptSucceeds(t *testing.T) {
	attempts := 0
	retryCallbacks := 0

	result, err := ExecuteWithRetry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(attempt, maxAttempts int, err error) {
			retryCallbacks++
		},
	}, func() (string, error) {
		attempts++
		return "immediate success", nil
	})

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != "immediate success" {
		t.Errorf("expected 'immediate success', got %q", result)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
	if retryCallbacks != 0 {
		t.Errorf("expected 0 retry callbacks, got %d", retryCallbacks)
	}
}

// TestExecuteWithRetry_OnRetryCallbackReceivesCorrectInfo verifies that the
// OnRetry callback receives the correct attempt number and max attempts.
func TestExecuteWithRetry_OnRetryCallbackReceivesCorrectInfo(t *testing.T) {
	type callbackInfo struct {
		attempt     int
		maxAttempts int
		errMsg      string
	}
	var callbacks []callbackInfo

	ExecuteWithRetry(RetryOptions{
		MaxAttempts: 4,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(attempt, maxAttempts int, err error) {
			callbacks = append(callbacks, callbackInfo{
				attempt:     attempt,
				maxAttempts: maxAttempts,
				errMsg:      err.Error(),
			})
		},
	}, func() (string, error) {
		return "", fmt.Errorf("connection refused: attempt failed") // retryable
	})

	if len(callbacks) != 3 {
		t.Fatalf("expected 3 callbacks (attempts 1-3), got %d", len(callbacks))
	}
	for i, cb := range callbacks {
		expectedAttempt := i + 1
		if cb.attempt != expectedAttempt {
			t.Errorf("callback %d: expected attempt=%d, got %d", i, expectedAttempt, cb.attempt)
		}
		if cb.maxAttempts != 4 {
			t.Errorf("callback %d: expected maxAttempts=4, got %d", i, cb.maxAttempts)
		}
		if !strings.Contains(cb.errMsg, "connection refused") {
			t.Errorf("callback %d: expected errMsg to contain 'connection refused', got %q", i, cb.errMsg)
		}
	}
}

// TestExecuteWithRetry_TranscribeSurfacing verifies that the retry mechanism
// can be used to surface retry attempts to a transcript (simulated).
func TestExecuteWithRetry_TranscriptSurfacing(t *testing.T) {
	var transcriptMessages []string

	ExecuteWithRetry(RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		OnRetry: func(attempt, maxAttempts int, err error) {
			msg := fmt.Sprintf("[Retry]: attempt %d/%d failed (%v). Retrying...", attempt, maxAttempts, err)
			transcriptMessages = append(transcriptMessages, msg)
		},
	}, func() (string, error) {
		return "", errors.New("connection refused")
	})

	if len(transcriptMessages) != 2 {
		t.Fatalf("expected 2 transcript messages, got %d", len(transcriptMessages))
	}
	for i, msg := range transcriptMessages {
		if !strings.Contains(msg, "[Retry]:") {
			t.Errorf("message %d should contain '[Retry]:', got %q", i, msg)
		}
		if !strings.Contains(msg, "Retrying...") {
			t.Errorf("message %d should contain 'Retrying...', got %q", i, msg)
		}
	}
}

// TestRetryExhaustedError_Unwrap verifies that RetryExhaustedError correctly
// wraps the last underlying error so errors.Is/As can reach it.
func TestRetryExhaustedError_Unwrap(t *testing.T) {
	cause := errors.New("connection refused")
	retryErr := &RetryExhaustedError{
		MaxAttempts: 5,
		LastErr:     cause,
	}

	if !errors.Is(retryErr, cause) {
		t.Error("errors.Is should find cause through RetryExhaustedError.Unwrap()")
	}
	if !strings.Contains(retryErr.Error(), "5 attempts") {
		t.Errorf("expected error to mention '5 attempts', got: %v", retryErr)
	}
	if !strings.Contains(retryErr.Error(), "connection refused") {
		t.Errorf("expected error to mention cause, got: %v", retryErr)
	}
}

// TestExecuteWithRetry_EmitsCorrectNumberOfRetryAttempts verifies the exact
// number of retry attempts matches the configured MaxAttempts.
func TestExecuteWithRetry_EmitsCorrectNumberOfRetryAttempts(t *testing.T) {
	for _, maxAttempts := range []int{1, 3, 5, 10} {
		t.Run(fmt.Sprintf("maxAttempts=%d", maxAttempts), func(t *testing.T) {
			attempts := 0
			_, _ = ExecuteWithRetry(RetryOptions{
				MaxAttempts: maxAttempts,
				BaseDelay:   1 * time.Millisecond,
			}, func() (string, error) {
				attempts++
				return "", errors.New("timeout")
			})
			if attempts != maxAttempts {
				t.Errorf("expected %d attempts, got %d", maxAttempts, attempts)
			}
		})
	}
}
