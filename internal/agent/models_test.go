package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_ListModels_OpenAIListShape(t *testing.T) {
	var gotAuth string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(modelsListResponse{
			Object: "list",
			Data: []ModelInfo{
				{ID: "mimo-v2.5-free", OwnedBy: "opencode_zen"},
				{ID: "mimo-v2.5-pro", OwnedBy: "opencode_zen"},
				{ID: "", OwnedBy: "garbage"}, // empty id should be filtered
			},
		})
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	cfg := AgentConfig{BaseURL: server.URL, APIKey: "secret-key"}

	models, err := client.ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if gotPath != "/models" {
		t.Errorf("expected path /models, got %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("expected auth 'Bearer secret-key', got %q", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models (empty id filtered), got %d: %+v", len(models), models)
	}
	if models[0].ID != "mimo-v2.5-free" {
		t.Errorf("expected first id mimo-v2.5-free, got %q", models[0].ID)
	}
	if models[1].ID != "mimo-v2.5-pro" {
		t.Errorf("expected second id mimo-v2.5-pro, got %q", models[1].ID)
	}
}

func TestClient_ListModels_NoAPIKeyOmitsAuth(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()

	client := NewClient(2 * time.Second)
	cfg := AgentConfig{BaseURL: server.URL}

	_, err := client.ListModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestClient_ListModels_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	client := NewClient(2 * time.Second)
	_, err := client.ListModels(context.Background(), AgentConfig{BaseURL: server.URL, APIKey: "x"})
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected status 401 in error, got %v", err)
	}
}

func TestClient_ListAllModels_ParallelFanOutAndErrors(t *testing.T) {
	var aHits, bHits, cHits int32
	makeProvider := func(id string, body string, status int, hit *int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(hit, 1)
			w.Header().Set("Content-Type", "application/json")
			if status != 200 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("boom"))
				return
			}
			_, _ = w.Write([]byte(body))
		}))
	}
	a := makeProvider("a", `{"object":"list","data":[{"id":"a-1"},{"id":"a-2"}]}`, 200, &aHits)
	b := makeProvider("b", "", 500, &bHits)
	c := makeProvider("c", `{"object":"list","data":[{"id":"c-1"}]}`, 200, &cHits)
	defer a.Close()
	defer b.Close()
	defer c.Close()

	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"opencode_zen": {BaseURL: a.URL, APIKey: "k1"},
			"xiaomi_direct": {BaseURL: b.URL, APIKey: "k2"},
			"other":         {BaseURL: c.URL, APIKey: "k3"},
		},
	}
	client := NewClient(2 * time.Second)

	models, errs := client.ListAllModels(context.Background(), cfg)

	if atomic.LoadInt32(&aHits) == 0 || atomic.LoadInt32(&cHits) == 0 {
		t.Fatalf("providers a and c should have been hit; a=%d c=%d", aHits, cHits)
	}
	if atomic.LoadInt32(&bHits) == 0 {
		t.Fatalf("provider b should have been hit (and failed); b=%d", bHits)
	}
	if len(errs) != 1 || errs[0].Provider != "xiaomi_direct" {
		t.Errorf("expected one error for xiaomi_direct, got %+v", errs)
	}
	// 2 from a + 1 from c = 3.
	if len(models) != 3 {
		t.Fatalf("expected 3 models merged, got %d: %+v", len(models), models)
	}
	// Sorted by provider then id.
	if models[0].Provider != "opencode_zen" || models[0].Info.ID != "a-1" {
		t.Errorf("expected first model to be opencode_zen/a-1, got %s/%s", models[0].Provider, models[0].Info.ID)
	}
	if models[2].Provider != "other" {
		t.Errorf("expected last provider to be 'other', got %q", models[2].Provider)
	}
}

func TestClient_Respond_ReasoningAndThinkingInjection(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		resp := ChatCompletionResponse{
			Choices: []ChatChoice{{Message: ResponseChatMessage{Content: "ok"}}},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(2 * time.Second)
	cfg := AgentConfig{
		Name:            "Coder",
		BaseURL:         server.URL,
		APIKey:          "k",
		Model:           "mimo-v2.5-pro",
		ReasoningLevel:  ReasoningLevelHigh,
		ThinkingMode:    ThinkingModeEnabled,
	}

	_, err := client.Respond(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}

	// Verify both shapes show up in the request body.
	var parsed map[string]interface{}
	if uErr := json.Unmarshal(captured, &parsed); uErr != nil {
		t.Fatalf("unmarshal captured: %v", uErr)
	}

	if got := parsed["reasoning_effort"]; got != "high" {
		t.Errorf("expected flat reasoning_effort=high, got %v", got)
	}
	r, ok := parsed["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reasoning object in body, got %v", parsed["reasoning"])
	}
	if r["effort"] != "high" {
		t.Errorf("expected reasoning.effort=high, got %v", r["effort"])
	}
	th, ok := parsed["thinking"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected thinking object in body, got %v", parsed["thinking"])
	}
	if th["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", th["type"])
	}
}

func TestClient_Respond_NoReasoningNoThinkingOmitsFields(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client := NewClient(2 * time.Second)
	cfg := AgentConfig{BaseURL: server.URL, Model: "mimo-v2.5-free"} // no reasoning/thinking
	_, err := client.Respond(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if strings.Contains(string(captured), `"reasoning"`) {
		t.Errorf("expected no 'reasoning' field, got %s", captured)
	}
	if strings.Contains(string(captured), `"thinking"`) {
		t.Errorf("expected no 'thinking' field, got %s", captured)
	}
	if strings.Contains(string(captured), `"reasoning_effort"`) {
		t.Errorf("expected no 'reasoning_effort' field, got %s", captured)
	}
}
