package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteWebSearch_Success(t *testing.T) {
	mockResponse := `{
		"success": true,
		"data": [
			{
				"title": "Go Programming Language",
				"url": "https://golang.org",
				"markdown": "# Go\nAn open source programming language.",
				"description": "Go docs"
			},
			{
				"title": "Firecrawl Documentation",
				"url": "https://docs.firecrawl.dev",
				"markdown": "## Firecrawl Search\nClean web search API.",
				"description": "Firecrawl docs"
			}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-firecrawl-key" {
			t.Errorf("expected Authorization header 'Bearer test-firecrawl-key', got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer ts.Close()

	// Use custom client pointing to mock server
	client := ts.Client()

	// Override endpoint for testing by creating a request directly or wrapping client
	// Since FirecrawlSearchEndpoint is a constant, we can test via ExecuteWebSearchWithClient
	// by replacing transport to redirect FirecrawlSearchEndpoint to ts.URL.
	client.Transport = &rewriteTransport{
		targetURL: ts.URL,
		transport: client.Transport,
	}

	result, err := ExecuteWebSearchWithClient(context.Background(), "golang docs", "test-firecrawl-key", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Search results for \"golang docs\":") {
		t.Errorf("expected search header in result, got: %s", result)
	}
	if !strings.Contains(result, "1. Go Programming Language") {
		t.Errorf("expected result item 1 title, got: %s", result)
	}
	if !strings.Contains(result, "URL: https://golang.org") {
		t.Errorf("expected result item 1 URL, got: %s", result)
	}
	if !strings.Contains(result, "# Go\nAn open source programming language.") {
		t.Errorf("expected result item 1 markdown content, got: %s", result)
	}
	if !strings.Contains(result, "2. Firecrawl Documentation") {
		t.Errorf("expected result item 2 title, got: %s", result)
	}
}

func TestExecuteWebSearch_QuotaExceeded_HTTP402(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error": "Quota exceeded. Upgrade your plan."}`))
	}))
	defer ts.Close()

	client := ts.Client()
	client.Transport = &rewriteTransport{
		targetURL: ts.URL,
		transport: client.Transport,
	}

	result, err := ExecuteWebSearchWithClient(context.Background(), "test query", "test-key", client)
	if err != nil {
		t.Fatalf("unexpected Go error on quota exceeded: %v", err)
	}

	if !strings.Contains(result, "quota likely exceeded") {
		t.Errorf("expected 'quota likely exceeded' message, got: %q", result)
	}
}

func TestExecuteWebSearch_QuotaExceeded_HTTP429(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error": "Rate limit / credit quota reached"}`))
	}))
	defer ts.Close()

	client := ts.Client()
	client.Transport = &rewriteTransport{
		targetURL: ts.URL,
		transport: client.Transport,
	}

	result, err := ExecuteWebSearchWithClient(context.Background(), "test query", "test-key", client)
	if err != nil {
		t.Fatalf("unexpected Go error on rate limit/quota: %v", err)
	}

	if !strings.Contains(result, "quota likely exceeded") {
		t.Errorf("expected 'quota likely exceeded' message, got: %q", result)
	}
}

func TestExecuteWebSearch_EmptyResults(t *testing.T) {
	mockResponse := `{"success": true, "data": []}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer ts.Close()

	client := ts.Client()
	client.Transport = &rewriteTransport{
		targetURL: ts.URL,
		transport: client.Transport,
	}

	result, err := ExecuteWebSearchWithClient(context.Background(), "obscure term XYZ123", "test-key", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "no results found") {
		t.Errorf("expected 'no results found' message, got: %q", result)
	}
}

func TestExecuteWebSearch_MissingAPIKey(t *testing.T) {
	result, err := ExecuteWebSearchWithClient(context.Background(), "test query", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "search API key not configured") {
		t.Errorf("expected 'search API key not configured' message, got: %q", result)
	}
}

func TestExecuteWebSearch_MissingQuery(t *testing.T) {
	_, err := ExecuteWebSearchWithClient(context.Background(), "   ", "test-key", nil)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("expected 'query' in error message, got: %v", err)
	}
}

// rewriteTransport redirects all requests to targetURL for mocking.
type rewriteTransport struct {
	targetURL string
	transport http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, t.targetURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	tr := t.transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	return tr.RoundTrip(newReq)
}
