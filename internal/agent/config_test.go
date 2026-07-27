package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFromYaml(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	content := []byte(`
base_url: "https://custom.endpoint/v1"
api_key: "test-key-123"
model: "custom-model"
`)

	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.BaseURL != "https://custom.endpoint/v1" {
		t.Errorf("Expected BaseURL 'https://custom.endpoint/v1', got %q", cfg.BaseURL)
	}
	if cfg.APIKey != "test-key-123" {
		t.Errorf("Expected APIKey 'test-key-123', got %q", cfg.APIKey)
	}
	if cfg.Model != "custom-model" {
		t.Errorf("Expected Model 'custom-model', got %q", cfg.Model)
	}

	if !cfg.Coder.HasTools {
		t.Errorf("Coder.HasTools should be true")
	}
	if cfg.Reviewer.HasTools {
		t.Errorf("Reviewer.HasTools should be false")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	nonExistentPath := filepath.Join(t.TempDir(), "nonexistent.yaml")

	cfg, err := LoadConfig(nonExistentPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("Expected default BaseURL %q, got %q", DefaultBaseURL, cfg.BaseURL)
	}
	if cfg.Model != DefaultModel {
		t.Errorf("Expected default Model %q, got %q", DefaultModel, cfg.Model)
	}
	if cfg.Coder.Name != "Coder" || cfg.Reviewer.Name != "Reviewer" {
		t.Errorf("Agent names incorrect")
	}
	if cfg.Coder.ContextWindow != DefaultContextWindow {
		t.Errorf("Expected Coder.ContextWindow %d, got %d", DefaultContextWindow, cfg.Coder.ContextWindow)
	}
	if cfg.SessionRetentionDays != DefaultSessionRetentionDays {
		t.Errorf("Expected SessionRetentionDays %d, got %d", DefaultSessionRetentionDays, cfg.SessionRetentionDays)
	}
	if cfg.LogMaxBytes != DefaultLogMaxBytes || cfg.LogMaxBackups != DefaultLogMaxBackups {
		t.Errorf("log rotation defaults = (%d, %d), want (%d, %d)", cfg.LogMaxBytes, cfg.LogMaxBackups, DefaultLogMaxBytes, DefaultLogMaxBackups)
	}
}

func TestLoadConfigSessionRetentionDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("session_retention_days: 14\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionRetentionDays != 14 {
		t.Errorf("SessionRetentionDays = %d, want 14", cfg.SessionRetentionDays)
	}
}

func TestLoadConfigLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log_max_bytes: 2048\nlog_max_backups: 3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogMaxBytes != 2048 || cfg.LogMaxBackups != 3 {
		t.Errorf("log rotation config = (%d, %d), want (2048, 3)", cfg.LogMaxBytes, cfg.LogMaxBackups)
	}
}

func TestLoadConfig_TokenCostAndContextWindow(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	content := []byte(`
base_url: "https://custom.endpoint/v1"
api_key: "test-key-123"
model: "custom-model"
input_cost_per_token: 0.000003
output_cost_per_token: 0.000015
context_window: 128000
`)

	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Coder.InputCostPerToken != 0.000003 {
		t.Errorf("Expected Coder.InputCostPerToken 0.000003, got %f", cfg.Coder.InputCostPerToken)
	}
	if cfg.Reviewer.OutputCostPerToken != 0.000015 {
		t.Errorf("Expected Reviewer.OutputCostPerToken 0.000015, got %f", cfg.Reviewer.OutputCostPerToken)
	}
	if cfg.Coder.ContextWindow != 128000 {
		t.Errorf("Expected Coder.ContextWindow 128000, got %d", cfg.Coder.ContextWindow)
	}
}
