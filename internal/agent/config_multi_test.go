package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfig_LegacySingleProviderUnchanged verifies that a
// config.yaml with only the legacy top-level fields (no
// `providers:` block) is loaded exactly as before. The implicit
// `providers.opencode_zen` is constructed in-memory and the
// Coder/Reviewer configs match the pre-multi-provider behavior.
func TestLoadConfig_LegacySingleProviderUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
base_url: "https://opencode.ai/zen/v1"
api_key: "sk-legacy"
model: "mimo-v2.5-free"
search_api_key: "fc-legacy"
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("BaseURL: got %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-legacy" {
		t.Errorf("APIKey: got %q", cfg.APIKey)
	}
	if cfg.Model != "mimo-v2.5-free" {
		t.Errorf("Model: got %q", cfg.Model)
	}
	if cfg.Coder.BaseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("Coder.BaseURL: got %q", cfg.Coder.BaseURL)
	}
	if cfg.Coder.Model != "mimo-v2.5-free" {
		t.Errorf("Coder.Model: got %q", cfg.Coder.Model)
	}
	if cfg.Reviewer.APIKey != "sk-legacy" {
		t.Errorf("Reviewer.APIKey: got %q", cfg.Reviewer.APIKey)
	}
	// Implicit provider was constructed.
	if _, ok := cfg.Providers[DefaultActiveProvider]; !ok {
		t.Errorf("expected implicit %q provider, got providers: %v", DefaultActiveProvider, cfg.ProviderNames())
	}
	if cfg.ActiveProvider != DefaultActiveProvider {
		t.Errorf("ActiveProvider: got %q, want %q", cfg.ActiveProvider, DefaultActiveProvider)
	}
}

// TestLoadConfig_MultiProvider verifies the new shape loads
// correctly and that the active provider is the one used for
// Coder/Reviewer.
func TestLoadConfig_MultiProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
providers:
  opencode_zen:
    base_url: "https://opencode.ai/zen/v1"
    api_key: "sk-zen"
    default_model: "mimo-v2.5-free"
  xiaomi_direct:
    base_url: "https://api.xiaomimimo.com/v1"
    api_key: "sk-xiaomi"
    default_model: "mimo-v2.5-pro"
    reasoning_level: high
    thinking_mode: enabled
active_provider: xiaomi_direct
model: "mimo-v2.5-pro"
reasoning_level: high
thinking_mode: enabled
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ActiveProvider != "xiaomi_direct" {
		t.Errorf("ActiveProvider: got %q", cfg.ActiveProvider)
	}
	if cfg.Coder.BaseURL != "https://api.xiaomimimo.com/v1" {
		t.Errorf("Coder.BaseURL: got %q", cfg.Coder.BaseURL)
	}
	if cfg.Coder.APIKey != "sk-xiaomi" {
		t.Errorf("Coder.APIKey: got %q", cfg.Coder.APIKey)
	}
	if cfg.Coder.Model != "mimo-v2.5-pro" {
		t.Errorf("Coder.Model: got %q", cfg.Coder.Model)
	}
	if cfg.Coder.ReasoningLevel != "high" {
		t.Errorf("Coder.ReasoningLevel: got %q", cfg.Coder.ReasoningLevel)
	}
	if cfg.Coder.ThinkingMode != "enabled" {
		t.Errorf("Coder.ThinkingMode: got %q", cfg.Coder.ThinkingMode)
	}
	if cfg.Reviewer.BaseURL != "https://api.xiaomimimo.com/v1" {
		t.Errorf("Reviewer.BaseURL: got %q", cfg.Reviewer.BaseURL)
	}
}

// TestLoadConfig_ProviderBackfillFromTopLevel verifies that if a
// user adds a `providers:` block but leaves fields empty on a
// provider, they get backfilled from the top-level legacy fields.
func TestLoadConfig_ProviderBackfillFromTopLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
base_url: "https://opencode.ai/zen/v1"
api_key: "sk-legacy"
model: "mimo-v2.5-free"
providers:
  xiaomi_direct: {}
active_provider: xiaomi_direct
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	xp := cfg.Providers["xiaomi_direct"]
	if xp.BaseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("xiaomi_direct.BaseURL backfill: got %q", xp.BaseURL)
	}
	if xp.APIKey != "sk-legacy" {
		t.Errorf("xiaomi_direct.APIKey backfill: got %q", xp.APIKey)
	}
	if xp.DefaultModel != "mimo-v2.5-free" {
		t.Errorf("xiaomi_direct.DefaultModel backfill: got %q", xp.DefaultModel)
	}
}

// TestSaveConfig_PreservesUnknownTopLevelFields verifies the
// yaml.Node round-trip doesn't destroy user-added top-level keys
// (e.g. custom search settings the user added themselves).
func TestSaveConfig_PreservesUnknownTopLevelFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := []byte(`
base_url: "https://opencode.ai/zen/v1"
api_key: "sk-zen"
model: "mimo-v2.5-free"
# user-added custom field — must survive a save
my_custom_setting: "hello world"
browser_mode: "headless"
chrome_cdp_port: 9222
`)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Bump model so we know SaveConfig actually wrote.
	cfg.Coder.Model = "mimo-v2.5-pro"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if !strings.Contains(string(after), "my_custom_setting") {
		t.Errorf("expected my_custom_setting to be preserved, got:\n%s", after)
	}
	if !strings.Contains(string(after), "hello world") {
		t.Errorf("expected my_custom_setting value to be preserved, got:\n%s", after)
	}
	if !strings.Contains(string(after), "chrome_cdp_port") {
		t.Errorf("expected chrome_cdp_port to be preserved, got:\n%s", after)
	}
}

// TestSaveConfig_PromotesLegacyToMultiProvider verifies that a
// legacy config gets a `providers:` block on first save.
func TestSaveConfig_PromotesLegacyToMultiProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
base_url: "https://opencode.ai/zen/v1"
api_key: "sk-zen"
model: "mimo-v2.5-free"
`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(after), "providers:") {
		t.Errorf("expected providers: block after save, got:\n%s", after)
	}
	if !strings.Contains(string(after), "opencode_zen") {
		t.Errorf("expected opencode_zen provider entry, got:\n%s", after)
	}
	if !strings.Contains(string(after), "active_provider:") {
		t.Errorf("expected active_provider key, got:\n%s", after)
	}
}

// TestModelSupportsReasoning checks the capability lookup.
func TestModelSupportsReasoning(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"mimo-v2.5-pro", true},
		{"mimo-v2.5", true},
		{"mimo-v2-pro", true},
		{"mimo-v2-omni", true},
		{"mimo-v2-flash", false},
		{"some-unknown-model", true}, // unknown → assume yes
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			if got := ModelSupportsReasoning(c.model); got != c.want {
				t.Errorf("ModelSupportsReasoning(%q) = %v, want %v", c.model, got, c.want)
			}
		})
	}
}

// TestConfig_ActiveProviderNotFound ensures an explicit
// active_provider that points at a missing key returns a clear
// error rather than silently falling back.
func TestConfig_ActiveProviderNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
providers:
  opencode_zen:
    base_url: "https://opencode.ai/zen/v1"
    api_key: "k"
active_provider: bogus
`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing active_provider")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("expected error to name the missing provider, got %v", err)
	}
}
