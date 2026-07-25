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

	"github.com/kaiizer777/triad/internal/logger"
)

// FirecrawlSearchEndpoint is the Firecrawl Search API endpoint URL.
const FirecrawlSearchEndpoint = "https://api.firecrawl.dev/v1/search"

// FirecrawlSearchResult represents a single search result returned by Firecrawl.
type FirecrawlSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Markdown    string `json:"markdown"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
	Content     string `json:"content"`
}

// FirecrawlSearchResponse is the top-level API response from Firecrawl v1 /search.
type FirecrawlSearchResponse struct {
	Success bool                    `json:"success"`
	Data    []FirecrawlSearchResult `json:"data"`
	Error   string                  `json:"error"`
}

// ExecuteWebSearch searches the web using Firecrawl Search API (default HTTP client with timeout).
func ExecuteWebSearch(query, apiKey string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return ExecuteWebSearchWithClient(ctx, query, apiKey, http.DefaultClient)
}

// ExecuteWebSearchWithClient executes web_search with a context and custom http.Client (useful for unit testing).
func ExecuteWebSearchWithClient(ctx context.Context, query, apiKey string, client *http.Client) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("web_search: required argument 'query' is missing or empty")
	}

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "web_search: search API key not configured (set search_api_key in config.yaml or FIRECRAWL_API_KEY environment variable)", nil
	}

	if client == nil {
		client = http.DefaultClient
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"query": query,
		"limit": 5,
	})
	if err != nil {
		return "", fmt.Errorf("web_search: failed to encode request JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, FirecrawlSearchEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("web_search: failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		logger.L().Warn("web_search HTTP request failed", "query", query, "error", err.Error())
		return fmt.Sprintf("web_search: HTTP request failed: %v", err), nil
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("web_search: failed to read response body: %v", err), nil
	}

	// Handle credit/quota/payment errors cleanly
	if resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusTooManyRequests {
		logger.L().Warn("web_search quota exceeded", "status", resp.StatusCode, "body", string(bodyBytes))
		return "web_search: search unavailable, quota likely exceeded", nil
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logger.L().Warn("web_search unauthorized", "status", resp.StatusCode)
		return "web_search: invalid or unauthorized search API key", nil
	}

	if resp.StatusCode != http.StatusOK {
		lowerBody := strings.ToLower(string(bodyBytes))
		if strings.Contains(lowerBody, "quota") || strings.Contains(lowerBody, "credit") || strings.Contains(lowerBody, "payment") || strings.Contains(lowerBody, "insufficient") {
			return "web_search: search unavailable, quota likely exceeded", nil
		}
		logger.L().Warn("web_search HTTP status error", "status", resp.StatusCode, "body", string(bodyBytes))
		return fmt.Sprintf("web_search: API request failed with status code %d", resp.StatusCode), nil
	}

	var searchResp FirecrawlSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		logger.L().Warn("web_search failed to parse response JSON", "error", err.Error(), "body", string(bodyBytes))
		return fmt.Sprintf("web_search: failed to parse response JSON: %v", err), nil
	}

	if searchResp.Error != "" {
		lowerErr := strings.ToLower(searchResp.Error)
		if strings.Contains(lowerErr, "quota") || strings.Contains(lowerErr, "credit") || strings.Contains(lowerErr, "payment") || strings.Contains(lowerErr, "insufficient") || strings.Contains(lowerErr, "402") {
			return "web_search: search unavailable, quota likely exceeded", nil
		}
		return fmt.Sprintf("web_search: API error: %s", searchResp.Error), nil
	}

	if len(searchResp.Data) == 0 {
		return fmt.Sprintf("web_search: no results found for query: %q", query), nil
	}

	// Format results as clean readable plain text
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for %q:\n\n", query))

	// Cap at 5 results
	results := searchResp.Data
	if len(results) > 5 {
		results = results[:5]
	}

	for i, item := range results {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = "(No Title)"
		}
		url := strings.TrimSpace(item.URL)

		content := strings.TrimSpace(item.Markdown)
		if content == "" {
			content = strings.TrimSpace(item.Description)
		}
		if content == "" {
			content = strings.TrimSpace(item.Snippet)
		}
		if content == "" {
			content = strings.TrimSpace(item.Content)
		}
		if content == "" {
			content = "(No content returned)"
		}

		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   Content:\n%s\n\n", i+1, title, url, content))
	}

	return strings.TrimSpace(sb.String()), nil
}
