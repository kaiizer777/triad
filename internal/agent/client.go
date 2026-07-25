package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/logger"
	"github.com/kaiizer777/triad/internal/transcript"
)

// ChatMessage represents a single message in an OpenAI chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest represents the JSON request payload for OpenAI-compatible /chat/completions.
type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []ToolSchema  `json:"tools,omitempty"` // only sent when agent HasTools == true
}

// ResponseChatMessage represents a message within a choice in an OpenAI chat completion response.
// It may contain either a plain-text Content (for normal replies) or ToolCalls (for tool-calling
// responses) — a response can have one without the other.
type ResponseChatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ChatChoice represents a single completion choice returned by the API.
type ChatChoice struct {
	Index        int                 `json:"index"`
	Message      ResponseChatMessage `json:"message"`
	FinishReason string              `json:"finish_reason,omitempty"`
}

// ChatCompletionResponse represents the JSON response payload for OpenAI-compatible /chat/completions.
type ChatCompletionResponse struct {
	ID      string       `json:"id,omitempty"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices"`
}

// AgentResponse holds the result of a single agent call.
// Exactly one of Text or ToolCalls will be populated:
//   - Text is set when the model returns a plain-text reply (finish_reason == "stop").
//   - ToolCalls is set when the model requests one or more tool executions (finish_reason == "tool_calls").
type AgentResponse struct {
	Text      string     // plain-text reply from the model
	ToolCalls []ToolCall // tool invocations requested by the model
}

// APIErrorDetail holds error information from an OpenAI API error response.
type APIErrorDetail struct {
	Message string      `json:"message"`
	Type    string      `json:"type,omitempty"`
	Code    interface{} `json:"code,omitempty"`
}

// APIErrorResponse represents an OpenAI API error payload structure.
type APIErrorResponse struct {
	Error APIErrorDetail `json:"error"`
}

// ErrRateLimit is returned when the API responds with HTTP 429 and all retry
// attempts are exhausted. It carries the last Retry-After value (if any) and
// the raw response body for diagnostic purposes.
type ErrRateLimit struct {
	Attempts   int
	RetryAfter string // raw Retry-After header value, may be ""
	Body       string // raw response body (may contain additional limit info)
}

func (e *ErrRateLimit) Error() string {
	if e.RetryAfter != "" {
		return fmt.Sprintf("rate limited (HTTP 429) after %d attempts; Retry-After: %s", e.Attempts, e.RetryAfter)
	}
	return fmt.Sprintf("rate limited (HTTP 429) after %d attempts", e.Attempts)
}

// ---------------------------------------------------------------------------
// Retry configuration
// ---------------------------------------------------------------------------

const (
	// maxRetryAttempts is the total number of times Respond will attempt the
	// request before giving up with ErrRateLimit. This counts the first attempt
	// too, so maxRetryAttempts=5 means 1 initial + 4 retries.
	maxRetryAttempts = 5

	// retryBaseDelay is the initial backoff delay before the first retry.
	retryBaseDelay = 2 * time.Second

	// retryMaxDelay caps the exponential backoff to avoid very long waits.
	retryMaxDelay = 60 * time.Second
)

// retryDelay computes the backoff duration for a given attempt number (1-indexed).
// Formula: min(base * 2^(attempt-1) + jitter, max)
// Jitter is ±25% of the computed delay to spread retries across concurrent callers.
func retryDelay(attempt int) time.Duration {
	exp := math.Pow(2, float64(attempt-1))
	delay := float64(retryBaseDelay) * exp

	// Apply ±25% jitter.
	jitter := delay * 0.25 * (rand.Float64()*2 - 1) //nolint:gosec // non-crypto use
	delay += jitter

	if delay > float64(retryMaxDelay) {
		delay = float64(retryMaxDelay)
	}
	if delay < 0 {
		delay = float64(retryBaseDelay)
	}
	return time.Duration(delay)
}

// parseRetryAfter parses the Retry-After header value and returns the duration
// to wait. Supports both integer seconds ("30") and HTTP-date formats.
// Returns 0 if the header is absent or unparseable.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	// Try integer seconds first (most common from OpenCode Zen / OpenAI).
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Try HTTP-date format.
	if t, err := http.ParseTime(header); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client wraps an http.Client to send chat completion requests to OpenAI-compatible endpoints.
type Client struct {
	httpClient *http.Client
}

// NewClient initializes a new Client with a given HTTP timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Respond sends transcript entries to an OpenAI-compatible /chat/completions endpoint.
// It returns an AgentResponse containing either a plain-text reply or a list of tool calls
// depending on what the model returns. When cfg.HasTools is true, the Coder tool schemas
// are included in the request.
//
// On HTTP 429 responses, Respond automatically retries up to maxRetryAttempts times using
// exponential backoff with jitter, honouring the Retry-After header when present.
// After all retries are exhausted an *ErrRateLimit is returned.
func (c *Client) Respond(ctx context.Context, cfg AgentConfig, entries []transcript.Entry) (AgentResponse, error) {
	messages := make([]ChatMessage, 0, len(entries)+1)

	// Inject system prompt as the first message if configured.
	if cfg.SystemPrompt != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: cfg.SystemPrompt,
		})
	}

	for _, entry := range entries {
		role := "assistant"
		if entry.Speaker == transcript.SpeakerYou {
			role = "user"
		}

		content := fmt.Sprintf("[%s]: %s", entry.Speaker, entry.Content)
		messages = append(messages, ChatMessage{
			Role:    role,
			Content: content,
		})
	}

	reqPayload := ChatCompletionRequest{
		Model:    cfg.Model,
		Messages: messages,
	}

	// Attach tool schemas only for the Coder agent (HasTools == true).
	if cfg.HasTools {
		reqPayload.Tools = CoderTools()
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return AgentResponse{}, fmt.Errorf("failed to marshal chat completion request: %w", err)
	}

	endpointURL := fmt.Sprintf("%s/chat/completions", strings.TrimRight(cfg.BaseURL, "/"))

	logger.L().Debug("agent request",
		"agent", cfg.Name,
		"url", endpointURL,
		"model", cfg.Model,
		"messages", len(messages),
		"has_tools", cfg.HasTools,
	)

	// ---------------------------------------------------------------------------
	// Retry loop — handles 429 with exponential backoff + Retry-After.
	// ---------------------------------------------------------------------------
	var lastRateLimitBody string
	var lastRetryAfter string

	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		// Check context before each attempt.
		if err := ctx.Err(); err != nil {
			return AgentResponse{}, fmt.Errorf("context cancelled before attempt %d: %w", attempt, err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return AgentResponse{}, fmt.Errorf("failed to create HTTP request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			authHeader := cfg.APIKey
			if !strings.HasPrefix(authHeader, "Bearer ") {
				authHeader = "Bearer " + authHeader
			}
			req.Header.Set("Authorization", authHeader)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Network/transport errors are not retried — they indicate a different
			// class of problem (DNS failure, connection refused, context cancel, etc.)
			return AgentResponse{}, fmt.Errorf("API request error: %w", err)
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return AgentResponse{}, fmt.Errorf("failed to read API response body: %w", readErr)
		}

		// --- Rate limit: back off and retry ---
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterHeader := resp.Header.Get("Retry-After")
			lastRetryAfter = retryAfterHeader
			lastRateLimitBody = string(respBody)

			// Always log the full 429 response the first several times — the body
			// and headers may reveal the actual ceiling (per work.md 9.1 guidance).
			logger.L().Warn("rate limited (429)",
				"agent", cfg.Name,
				"attempt", attempt,
				"max_attempts", maxRetryAttempts,
				"retry_after_header", retryAfterHeader,
				"response_body", lastRateLimitBody,
			)

			if attempt == maxRetryAttempts {
				// All retries exhausted.
				logger.L().Error("rate limit retries exhausted",
					"agent", cfg.Name,
					"attempts", maxRetryAttempts,
					"last_retry_after", lastRetryAfter,
				)
				return AgentResponse{}, &ErrRateLimit{
					Attempts:   maxRetryAttempts,
					RetryAfter: lastRetryAfter,
					Body:       lastRateLimitBody,
				}
			}

			// Determine wait duration: honour Retry-After if present, else exponential backoff.
			wait := parseRetryAfter(retryAfterHeader)
			if wait == 0 {
				wait = retryDelay(attempt)
			}

			logger.L().Info("retrying after backoff",
				"agent", cfg.Name,
				"attempt", attempt,
				"wait", wait.String(),
			)

			select {
			case <-ctx.Done():
				return AgentResponse{}, fmt.Errorf("context cancelled while waiting for rate-limit backoff: %w", ctx.Err())
			case <-time.After(wait):
			}
			continue
		}

		// --- Non-200, non-429: surface the API error immediately (no retry) ---
		if resp.StatusCode != http.StatusOK {
			logger.L().Error("API error response",
				"agent", cfg.Name,
				"status", resp.StatusCode,
				"body", string(respBody),
			)
			var apiErr APIErrorResponse
			if jsonErr := json.Unmarshal(respBody, &apiErr); jsonErr == nil && apiErr.Error.Message != "" {
				return AgentResponse{}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, apiErr.Error.Message)
			}
			return AgentResponse{}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
		}

		// --- Success: parse the response ---
		logger.L().Debug("API response received",
			"agent", cfg.Name,
			"attempt", attempt,
			"status", resp.StatusCode,
			"body_bytes", len(respBody),
		)

		// Log raw response body at DEBUG level for deep troubleshooting.
		// Truncated to 4096 chars to avoid enormous log lines for large responses.
		rawBody := string(respBody)
		if len(rawBody) > 4096 {
			rawBody = rawBody[:4096] + "... [truncated]"
		}
		logger.L().Debug("API response body", "agent", cfg.Name, "body", rawBody)

		var chatResp ChatCompletionResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			return AgentResponse{}, fmt.Errorf("failed to parse API response JSON: %w", err)
		}

		if len(chatResp.Choices) == 0 {
			return AgentResponse{}, fmt.Errorf("API returned empty choices")
		}

		msg := chatResp.Choices[0].Message

		// If the model returned tool calls, surface them directly.
		// A response can have tool_calls without content, or content without tool_calls.
		if len(msg.ToolCalls) > 0 {
			logger.L().Debug("agent returned tool calls",
				"agent", cfg.Name,
				"count", len(msg.ToolCalls),
				"first_tool", msg.ToolCalls[0].Function.Name,
			)
			return AgentResponse{ToolCalls: msg.ToolCalls}, nil
		}

		logger.L().Debug("agent returned text response",
			"agent", cfg.Name,
			"text_length", len(msg.Content),
		)
		return AgentResponse{Text: msg.Content}, nil
	}

	// Should be unreachable — the loop always returns or continues.
	return AgentResponse{}, fmt.Errorf("respond: unexpected exit from retry loop")
}
