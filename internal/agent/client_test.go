package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaiizer777/triad/internal/transcript"
)

func TestClient_Respond_SuccessAndPayload(t *testing.T) {
	var capturedReq ChatCompletionRequest
	var capturedAuth string
	var capturedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		capturedAuth = r.Header.Get("Authorization")
		capturedContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := json.Unmarshal(body, &capturedReq); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		resp := ChatCompletionResponse{
			ID:     "chatcmpl-123",
			Object: "chat.completion",
			Model:  capturedReq.Model,
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ResponseChatMessage{
						Role:    "assistant",
						Content: "I understand. I will review the webhook handler.",
					},
					FinishReason: "stop",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	cfg := AgentConfig{
		Name:     "Reviewer",
		BaseURL:  server.URL,
		APIKey:   "secret-token-123",
		Model:    "mimo-v2.5-free",
		HasTools: false,
	}

	entries := []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Add a webhook handler."},
		{ID: 2, Speaker: transcript.SpeakerCoder, Type: transcript.TypeProposedAction, Content: "I will write handler.go"},
		{ID: 3, Speaker: transcript.SpeakerReviewer, Type: transcript.TypeMessage, Content: "Looks good to me."},
		{ID: 4, Speaker: transcript.SpeakerSystem, Type: transcript.TypeMessage, Content: "System check passed."},
	}

	reply, err := client.Respond(context.Background(), cfg, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedReply := "I understand. I will review the webhook handler."
	if reply.Text != expectedReply {
		t.Errorf("expected reply %q, got %q", expectedReply, reply.Text)
	}
	if len(reply.ToolCalls) != 0 {
		t.Errorf("expected no tool calls for Reviewer, got %d", len(reply.ToolCalls))
	}

	if capturedAuth != "Bearer secret-token-123" {
		t.Errorf("expected Authorization header 'Bearer secret-token-123', got %q", capturedAuth)
	}

	if capturedContentType != "application/json" {
		t.Errorf("expected Content-Type header 'application/json', got %q", capturedContentType)
	}

	if capturedReq.Model != "mimo-v2.5-free" {
		t.Errorf("expected model 'mimo-v2.5-free', got %q", capturedReq.Model)
	}

	if len(capturedReq.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(capturedReq.Messages))
	}

	expectedMessages := []struct {
		role    string
		content string
	}{
		{"user", "[You]: Add a webhook handler."},
		{"assistant", "[Coder]: I will write handler.go"},
		{"assistant", "[Reviewer]: Looks good to me."},
		{"assistant", "[System]: System check passed."},
	}

	for i, exp := range expectedMessages {
		msg := capturedReq.Messages[i]
		if msg.Role != exp.role {
			t.Errorf("msg[%d] role: expected %q, got %q", i, exp.role, msg.Role)
		}
		if msg.Content != exp.content {
			t.Errorf("msg[%d] content: expected %q, got %q", i, exp.content, msg.Content)
		}
	}
}

func TestClient_Respond_AuthorizationHeaderFormatting(t *testing.T) {
	tests := []struct {
		name           string
		apiKey         string
		expectedHeader string
	}{
		{
			name:           "raw api key",
			apiKey:         "my-key-456",
			expectedHeader: "Bearer my-key-456",
		},
		{
			name:           "pre-formatted bearer key",
			apiKey:         "Bearer my-key-789",
			expectedHeader: "Bearer my-key-789",
		},
		{
			name:           "empty api key",
			apiKey:         "",
			expectedHeader: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = r.Header.Get("Authorization")
				resp := ChatCompletionResponse{
					Choices: []ChatChoice{
						{Message: ResponseChatMessage{Content: "ok"}},
					},
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			client := NewClient(2 * time.Second)
			cfg := AgentConfig{
				BaseURL: server.URL,
				APIKey:  tt.apiKey,
				Model:   "mimo-v2.5-free",
			}

			_, err := client.Respond(context.Background(), cfg, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedAuth != tt.expectedHeader {
				t.Errorf("expected Auth header %q, got %q", tt.expectedHeader, capturedAuth)
			}
		})
	}
}

func TestClient_Respond_ErrorStatusHandling(t *testing.T) {
	t.Run("401 Unauthorized with JSON error payload", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			errResp := APIErrorResponse{
				Error: APIErrorDetail{
					Message: "Invalid API key provided",
					Type:    "invalid_request_error",
				},
			}
			json.NewEncoder(w).Encode(errResp)
		}))
		defer server.Close()

		client := NewClient(2 * time.Second)
		cfg := AgentConfig{BaseURL: server.URL, Model: "mimo-v2.5-free"}

		_, err := client.Respond(context.Background(), cfg, nil)
		if err == nil {
			t.Fatal("expected error for 401 response, got nil")
		}

		if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Invalid API key provided") {
			t.Errorf("unexpected error format: %v", err)
		}
	})

	t.Run("429 Rate Limit with plain text body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Rate limit exceeded. Please try again later."))
		}))
		defer server.Close()

		client := NewClient(2 * time.Second)
		cfg := AgentConfig{BaseURL: server.URL, Model: "mimo-v2.5-free"}

		_, err := client.Respond(context.Background(), cfg, nil)
		if err == nil {
			t.Fatal("expected error for 429 response, got nil")
		}

		if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "Rate limit exceeded") {
			t.Errorf("unexpected error format: %v", err)
		}
	})

	t.Run("500 Server Error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		client := NewClient(2 * time.Second)
		cfg := AgentConfig{BaseURL: server.URL, Model: "mimo-v2.5-free"}

		_, err := client.Respond(context.Background(), cfg, nil)
		if err == nil {
			t.Fatal("expected error for 500 response, got nil")
		}

		if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "Internal Server Error") {
			t.Errorf("unexpected error format: %v", err)
		}
	})
}

func TestClient_Respond_ContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"delayed"}}]}`))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	cfg := AgentConfig{BaseURL: server.URL, Model: "mimo-v2.5-free"}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := client.Respond(ctx, cfg, nil)
	if err == nil {
		t.Fatal("expected timeout/cancellation error, got nil")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

// TestClient_Respond_ToolCallResponse verifies that when the model returns tool_calls
// the AgentResponse surfaces them correctly with no Text.
func TestClient_Respond_ToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			Choices: []ChatChoice{
				{
					Message: ResponseChatMessage{
						Role:    "assistant",
						Content: "",
						ToolCalls: []ToolCall{
							{
								ID:   "call_abc123",
								Type: "function",
								Function: ToolCallFunction{
									Name:      "write_file",
									Arguments: `{"path":"hello.txt","content":"hello world"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	cfg := AgentConfig{
		Name:     "Coder",
		BaseURL:  server.URL,
		APIKey:   "test-key",
		Model:    "mimo-v2.5-free",
		HasTools: true,
	}

	reply, err := client.Respond(context.Background(), cfg, []transcript.Entry{
		{ID: 1, Speaker: transcript.SpeakerYou, Type: transcript.TypeMessage, Content: "Create hello.txt with 'hello world'."},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if reply.Text != "" {
		t.Errorf("expected empty Text for tool call response, got %q", reply.Text)
	}
	if len(reply.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(reply.ToolCalls))
	}

	tc := reply.ToolCalls[0]
	if tc.Function.Name != "write_file" {
		t.Errorf("expected tool name 'write_file', got %q", tc.Function.Name)
	}
	if tc.ID != "call_abc123" {
		t.Errorf("expected tool call id 'call_abc123', got %q", tc.ID)
	}
	if !strings.Contains(tc.Function.Arguments, "hello.txt") {
		t.Errorf("expected arguments to contain 'hello.txt', got %q", tc.Function.Arguments)
	}
}

// TestClient_Respond_ToolsAttachedOnlyForCoder verifies that the 'tools' field is included
// in the request when HasTools==true and absent when HasTools==false.
func TestClient_Respond_ToolsAttachedOnlyForCoder(t *testing.T) {
	tests := []struct {
		name        string
		hasTools    bool
		expectTools bool
	}{
		{"Coder has tools", true, true},
		{"Reviewer has no tools", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var toolsPresent bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var req ChatCompletionRequest
				json.Unmarshal(body, &req) //nolint:errcheck
				toolsPresent = len(req.Tools) > 0

				resp := ChatCompletionResponse{
					Choices: []ChatChoice{
						{Message: ResponseChatMessage{Content: "ok"}},
					},
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			client := NewClient(2 * time.Second)
			cfg := AgentConfig{
				BaseURL:  server.URL,
				Model:    "mimo-v2.5-free",
				HasTools: tt.hasTools,
			}

			_, err := client.Respond(context.Background(), cfg, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if toolsPresent != tt.expectTools {
				t.Errorf("toolsPresent=%v, want %v", toolsPresent, tt.expectTools)
			}
		})
	}
}
