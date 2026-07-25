package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
		return AgentResponse{}, fmt.Errorf("API request error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return AgentResponse{}, fmt.Errorf("failed to read API response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr APIErrorResponse
		if jsonErr := json.Unmarshal(respBody, &apiErr); jsonErr == nil && apiErr.Error.Message != "" {
			return AgentResponse{}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return AgentResponse{}, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

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
		return AgentResponse{ToolCalls: msg.ToolCalls}, nil
	}

	return AgentResponse{Text: msg.Content}, nil
}
