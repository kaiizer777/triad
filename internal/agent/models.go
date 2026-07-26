package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelInfo is a single model entry returned by a provider's
// /models endpoint, parsed from the OpenAI-compatible
// {"object":"list","data":[{"id":"..."}]} shape. We deliberately
// model the bare minimum (id + owned_by) so any provider that emits
// the standard shape works without bespoke parsing.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// modelsListResponse is the OpenAI-compatible /models response.
type modelsListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ModelError records a single provider failure during a fan-out
// ListAllModels call. Surface to the UI so the user can see which
// providers responded and which didn't.
type ModelError struct {
	Provider string
	BaseURL  string
	Err      string
}

// AnnotatedModel is a model tagged with which provider it came from.
// The TUI's /models picker renders these as one unified list.
type AnnotatedModel struct {
	Provider string    // provider key, e.g. "opencode_zen", "xiaomi_direct"
	Info     ModelInfo // id + owned_by
}

// OwnedByLabel returns a human-friendly provider tag for this
// model row. If OwnedBy is empty, falls back to a dash so the
// column is never blank. Used by the TUI's /models picker.
func (am AnnotatedModel) OwnedByLabel() string {
	if am.Info.OwnedBy == "" {
		return "(unknown)"
	}
	return am.Info.OwnedBy
}

// ListModels does a GET on cfg.BaseURL + "/models" and parses the
// standard OpenAI-compatible list response. Works against OpenCode
// Zen (https://opencode.ai/zen/v1/models) and any other provider
// that follows the same shape.
//
// Returns the raw ModelInfo list (not annotated). For a unified
// cross-provider listing, use ListAllModels.
func (c *Client) ListModels(ctx context.Context, cfg AgentConfig) ([]ModelInfo, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("ListModels: cfg.BaseURL is empty")
	}
	endpointURL := strings.TrimRight(cfg.BaseURL, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ListModels: create request: %w", err)
	}
	if cfg.APIKey != "" {
		auth := cfg.APIKey
		if !strings.HasPrefix(auth, "Bearer ") {
			auth = "Bearer " + auth
		}
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ListModels: HTTP request to %s: %w", endpointURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("ListModels: read response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ListModels: %s returned status %d: %s",
			endpointURL, resp.StatusCode, truncate(string(bodyBytes), 256))
	}

	var parsed modelsListResponse
	if uErr := json.Unmarshal(bodyBytes, &parsed); uErr != nil {
		return nil, fmt.Errorf("ListModels: parse response from %s: %w", endpointURL, uErr)
	}

	// Filter out empty ids and dedupe (preserving first-seen order).
	seen := make(map[string]bool, len(parsed.Data))
	out := make([]ModelInfo, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ModelInfo{ID: id, OwnedBy: m.OwnedBy})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListAllModels fans out ListModels across every provider in cfg.Providers
// in parallel (bounded by per-provider timeout). Each provider gets
// 8 seconds. Errors from individual providers are collected and
// returned alongside the merged model list so the UI can show
// "X models from Y providers (N failed)".
//
// The returned list is sorted by (provider, model id).
func (c *Client) ListAllModels(ctx context.Context, cfg *Config) ([]AnnotatedModel, []ModelError) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil, nil
	}

	type result struct {
		provider string
		models   []ModelInfo
		err      error
		baseURL  string
	}

	results := make(chan result, len(cfg.Providers))
	var wg sync.WaitGroup

	// Per-provider timeout — 8s, generous enough for any well-behaved
	// /models endpoint, short enough that one slow provider doesn't
	// stall the whole picker.
	perProvider := 8 * time.Second

	for name, p := range cfg.Providers {
		wg.Add(1)
		go func(name string, p ProviderConfig) {
			defer wg.Done()
			subCtx, cancel := context.WithTimeout(ctx, perProvider)
			defer cancel()
			ac := AgentConfig{BaseURL: p.BaseURL, APIKey: p.APIKey}
			models, err := c.ListModels(subCtx, ac)
			results <- result{provider: name, models: models, err: err, baseURL: p.BaseURL}
		}(name, p)
	}

	wg.Wait()
	close(results)

	// Drain the channel into a map (keyed by provider) before
	// iterating the provider list. Iterating a channel while
	// also wanting to break early on a per-name match leaves
	// undrained entries that the next iteration can't see.
	resultsByProvider := make(map[string]result, len(cfg.Providers))
	for r := range results {
		resultsByProvider[r.provider] = r
	}

	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)

	var merged []AnnotatedModel
	var errs []ModelError
	for _, name := range names {
		r, ok := resultsByProvider[name]
		if !ok {
			// Shouldn't happen — every provider has a goroutine.
			errs = append(errs, ModelError{
				Provider: name,
				BaseURL:  cfg.Providers[name].BaseURL,
				Err:      "no response received (internal: missing goroutine result)",
			})
			continue
		}
		if r.err != nil {
			errs = append(errs, ModelError{
				Provider: name,
				BaseURL:  r.baseURL,
				Err:      r.err.Error(),
			})
			continue
		}
		for _, m := range r.models {
			merged = append(merged, AnnotatedModel{Provider: name, Info: m})
		}
	}

	// Final deterministic sort: by provider, then id.
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Provider != merged[j].Provider {
			return merged[i].Provider < merged[j].Provider
		}
		return merged[i].Info.ID < merged[j].Info.ID
	})

	return merged, errs
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
