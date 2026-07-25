package agent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AgentConfig holds parameters for initializing an agent (Coder or Reviewer).
type AgentConfig struct {
	Name         string `yaml:"name" json:"name"`
	BaseURL      string `yaml:"base_url" json:"base_url"`
	APIKey       string `yaml:"api_key" json:"api_key"`
	Model        string `yaml:"model" json:"model"`
	HasTools     bool   `yaml:"has_tools" json:"has_tools"`
	SystemPrompt string `yaml:"-" json:"-"` // injected at runtime, not from config file

	// Tools is an optional override for the tool schema list sent to the
	// model when HasTools is true. When nil, the client falls back to
	// CoderTools() (the parent Coder's full tool set). Subagents set
	// this explicitly to a narrower set (see internal/subagent) so
	// their tool surface is a strict subset of the parent's. Reviewer
	// never sends tools regardless of this field because HasTools is
	// false for it.
	Tools []ToolSchema `yaml:"-" json:"-"`
}

// Config represents the root configuration file format and dual agent settings.
type Config struct {
	BaseURL               string `yaml:"base_url"`
	APIKey                string `yaml:"api_key"`
	Model                 string `yaml:"model"`
	// CommandTimeoutSeconds caps run_command execution time (default 30).
	// Set to 0 or omit in config.yaml to use the default.
	CommandTimeoutSeconds int    `yaml:"command_timeout_seconds"`
	Coder    AgentConfig
	Reviewer AgentConfig
}

const (
	DefaultBaseURL            = "https://opencode.ai/zen/v1"
	DefaultModel              = "mimo-v2.5-free"
	DefaultCommandTimeoutSecs = 30
)

// LoadConfig attempts to load configuration from path (e.g., config.yaml).
// If the file does not exist, environment variables are used as fallbacks.
func LoadConfig(path string) (*Config, error) {
	rawCfg := Config{
		BaseURL: os.Getenv("OPENCODE_BASE_URL"),
		APIKey:  os.Getenv("OPENCODE_API_KEY"),
		Model:   os.Getenv("OPENCODE_MODEL"),
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var yamlCfg Config
		if unmarshalErr := yaml.Unmarshal(data, &yamlCfg); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to parse yaml config at %s: %w", path, unmarshalErr)
		}
		if yamlCfg.BaseURL != "" {
			rawCfg.BaseURL = yamlCfg.BaseURL
		}
		if yamlCfg.APIKey != "" {
			rawCfg.APIKey = yamlCfg.APIKey
		}
		if yamlCfg.Model != "" {
			rawCfg.Model = yamlCfg.Model
		}
		if yamlCfg.CommandTimeoutSeconds > 0 {
			rawCfg.CommandTimeoutSeconds = yamlCfg.CommandTimeoutSeconds
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("error reading config file at %s: %w", path, err)
	}

	// Apply defaults if empty
	if rawCfg.BaseURL == "" {
		rawCfg.BaseURL = DefaultBaseURL
	}
	if rawCfg.Model == "" {
		rawCfg.Model = DefaultModel
	}
	if rawCfg.CommandTimeoutSeconds <= 0 {
		rawCfg.CommandTimeoutSeconds = DefaultCommandTimeoutSecs
	}

	rawCfg.Coder = AgentConfig{
		Name:         "Coder",
		BaseURL:      rawCfg.BaseURL,
		APIKey:       rawCfg.APIKey,
		Model:        rawCfg.Model,
		HasTools:     true,
		SystemPrompt: CoderSystemPrompt,
	}

	rawCfg.Reviewer = AgentConfig{
		Name:         "Reviewer",
		BaseURL:      rawCfg.BaseURL,
		APIKey:       rawCfg.APIKey,
		Model:        rawCfg.Model,
		HasTools:     false,
		SystemPrompt: ReviewerSystemPrompt,
	}

	return &rawCfg, nil
}
