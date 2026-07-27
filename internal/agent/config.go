package agent

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentConfig holds parameters for initializing an agent (Coder or Reviewer).
type AgentConfig struct {
	Name               string  `yaml:"name" json:"name"`
	BaseURL            string  `yaml:"base_url" json:"base_url"`
	APIKey             string  `yaml:"api_key" json:"api_key"`
	Model              string  `yaml:"model" json:"model"`
	HasTools           bool    `yaml:"has_tools" json:"has_tools"`
	SystemPrompt       string  `yaml:"-" json:"-"` // injected at runtime, not from config file
	InputCostPerToken  float64 `yaml:"input_cost_per_token" json:"input_cost_per_token"`
	OutputCostPerToken float64 `yaml:"output_cost_per_token" json:"output_cost_per_token"`
	ContextWindow      int     `yaml:"context_window" json:"context_window"`

	// ReasoningLevel controls the model's reasoning effort. Sent as
	// `reasoning.effort` (Xiaomi MiMo) or `reasoning_effort` (OpenAI-style)
	// depending on the provider's request format. Valid values: "", "none",
	// "low", "medium", "high". Empty means "do not send the field".
	ReasoningLevel string `yaml:"reasoning_level,omitempty" json:"reasoning_level,omitempty"`

	// ThinkingMode controls the binary on/off "thinking" toggle (Xiaomi
	// MiMo's `thinking.type`). Sent as `thinking.type: "enabled"|"disabled"`.
	// Empty means "do not send the field".
	ThinkingMode string `yaml:"thinking_mode,omitempty" json:"thinking_mode,omitempty"`

	// Tools is an optional override for the tool schema list sent to the
	// model when HasTools is true. When nil, the client falls back to
	// CoderTools() (the parent Coder's full tool set). Subagents set
	// this explicitly to a narrower set (see internal/subagent) so
	// their tool surface is a strict subset of the parent's. Reviewer
	// never sends tools regardless of this field because HasTools is
	// false for it.
	Tools []ToolSchema `yaml:"-" json:"-"`
}

// ProviderConfig describes a single named provider entry under
// config.yaml's `providers:` map. Each provider has its own
// base_url / api_key and remembers its last-known per-provider
// defaults (model, reasoning level, thinking mode) so the
// /models picker can restore the user's last choice for that
// provider on next selection.
type ProviderConfig struct {
	// BaseURL is the OpenAI-compatible root (e.g.
	// "https://opencode.ai/zen/v1", "https://api.xiaomimimo.com/v1").
	BaseURL string `yaml:"base_url" json:"base_url"`

	// APIKey is sent as Authorization: Bearer <key> (or, for Xiaomi
	// MiMo, also accepts the `api-key:` header — the client does the
	// right thing automatically based on the base URL host).
	APIKey string `yaml:"api_key" json:"api_key"`

	// DefaultModel is the model this provider last had selected.
	// When the user switches back to this provider via /models or
	// /provider, this is the model the new active Coder/Reviewer
	// use. Empty means "use the global Model" (or the provider's
	// first /models listing if the user picks one later).
	DefaultModel string `yaml:"default_model,omitempty" json:"default_model,omitempty"`

	// ReasoningLevel is the per-provider default reasoning effort
	// the user last selected. Valid: "", "none", "low", "medium",
	// "high". Persists across /models invocations.
	ReasoningLevel string `yaml:"reasoning_level,omitempty" json:"reasoning_level,omitempty"`

	// ThinkingMode is the per-provider default thinking-mode
	// toggle. Valid: "", "enabled", "disabled". Persists across
	// /models invocations.
	ThinkingMode string `yaml:"thinking_mode,omitempty" json:"thinking_mode,omitempty"`
}

// Config represents the root configuration file format.
//
// Two shapes are accepted, fully backward-compatible:
//
//  1. Legacy single-provider (your current config.yaml):
//     base_url, api_key, model, search_api_key, browser_mode, ...
//     Loaded as before; an implicit "opencode_zen" provider is
//     constructed in-memory so the rest of the codebase can read
//     from cfg.Providers uniformly.
//
//  2. New multi-provider:
//     providers:
//     opencode_zen: {base_url, api_key, default_model, ...}
//     xiaomi_direct: {base_url, api_key, default_model, ...}
//     active_provider: opencode_zen
//     model: mimo-v2.5-free            # current effective model
//     reasoning_level: medium
//     thinking_mode: disabled
type Config struct {
	// --- legacy single-provider top-level fields (still honored) ---
	BaseURL               string  `yaml:"base_url"`
	APIKey                string  `yaml:"api_key"`
	Model                 string  `yaml:"model"`
	SearchAPIKey          string  `yaml:"search_api_key"`
	InputCostPerToken     float64 `yaml:"input_cost_per_token"`
	OutputCostPerToken    float64 `yaml:"output_cost_per_token"`
	ContextWindow         int     `yaml:"context_window"`
	CommandTimeoutSeconds int     `yaml:"command_timeout_seconds"`
	SessionRetentionDays  int     `yaml:"session_retention_days"`
	LogMaxBytes           int64   `yaml:"log_max_bytes"`
	LogMaxBackups         int     `yaml:"log_max_backups"`
	BrowserMode           string  `yaml:"browser_mode"`
	ChromeCDPPort         int     `yaml:"chrome_cdp_port"`

	// --- new multi-provider fields (optional) ---
	// Providers is the named provider map. nil means "use legacy
	// single-provider fields only"; SaveConfig promotes them into
	// a single opencode_zen entry on first save.
	Providers map[string]ProviderConfig `yaml:"providers,omitempty"`

	// ActiveProvider is the key into Providers that the active
	// Coder/Reviewer should use. Empty in legacy mode; in
	// multi-provider mode, defaults to "opencode_zen" if not set.
	ActiveProvider string `yaml:"active_provider,omitempty"`

	// --- active values, valid in both modes ---
	// ReasoningLevel / ThinkingMode mirror the same fields on
	// AgentConfig; they are the "currently in effect" values. The
	// per-provider defaults live under Providers[name].
	ReasoningLevel string `yaml:"reasoning_level,omitempty"`
	ThinkingMode   string `yaml:"thinking_mode,omitempty"`

	// Coder / Reviewer are derived from the active provider in
	// LoadConfig and always populated.
	Coder    AgentConfig
	Reviewer AgentConfig
}

const (
	DefaultBaseURL              = "https://opencode.ai/zen/v1"
	DefaultModel                = "mimo-v2.5-free"
	DefaultCommandTimeoutSecs   = 30
	DefaultSessionRetentionDays = 30
	DefaultLogMaxBytes          = 10 * 1024 * 1024
	DefaultLogMaxBackups        = 5
	DefaultBrowserMode          = "headless"
	DefaultChromeCDPPort        = 9222
	DefaultContextWindow        = 1000000
	DefaultActiveProvider       = "opencode_zen"

	// ReasoningLevelNone disables thinking. ReasoningLevelLow /
	// Medium / High enable thinking with successively larger
	// effort budgets (per Xiaomi MiMo docs, "low/medium/high"
	// currently behave identically, but we expose all four for
	// future-proofing).
	ReasoningLevelNone   = "none"
	ReasoningLevelLow    = "low"
	ReasoningLevelMedium = "medium"
	ReasoningLevelHigh   = "high"

	// ThinkingModeEnabled / ThinkingModeDisabled correspond to
	// Xiaomi's `thinking.type` field.
	ThinkingModeEnabled  = "enabled"
	ThinkingModeDisabled = "disabled"
)

// LoadConfig attempts to load configuration from path (e.g., config.yaml).
// If the file does not exist, environment variables are used as fallbacks.
// Both legacy single-provider and new multi-provider shapes are accepted
// and produce the same effective Coder/Reviewer config.
func LoadConfig(path string) (*Config, error) {
	searchKey := os.Getenv("FIRECRAWL_API_KEY")
	if searchKey == "" {
		searchKey = os.Getenv("SEARCH_API_KEY")
	}

	var envInputCost float64
	if v := os.Getenv("OPENCODE_INPUT_COST"); v != "" {
		_, _ = fmt.Sscanf(v, "%f", &envInputCost)
	}
	var envOutputCost float64
	if v := os.Getenv("OPENCODE_OUTPUT_COST"); v != "" {
		_, _ = fmt.Sscanf(v, "%f", &envOutputCost)
	}
	var envCtxWindow int
	if v := os.Getenv("OPENCODE_CONTEXT_WINDOW"); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &envCtxWindow)
	}

	rawCfg := Config{
		BaseURL:            os.Getenv("OPENCODE_BASE_URL"),
		APIKey:             os.Getenv("OPENCODE_API_KEY"),
		Model:              os.Getenv("OPENCODE_MODEL"),
		SearchAPIKey:       searchKey,
		InputCostPerToken:  envInputCost,
		OutputCostPerToken: envOutputCost,
		ContextWindow:      envCtxWindow,
	}

	data, err := os.ReadFile(path)
	if err == nil {
		var yamlCfg Config
		if unmarshalErr := yaml.Unmarshal(data, &yamlCfg); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to parse yaml config at %s: %w", path, unmarshalErr)
		}
		// Legacy single-provider fields.
		if yamlCfg.BaseURL != "" {
			rawCfg.BaseURL = yamlCfg.BaseURL
		}
		if yamlCfg.APIKey != "" {
			rawCfg.APIKey = yamlCfg.APIKey
		}
		if yamlCfg.Model != "" {
			rawCfg.Model = yamlCfg.Model
		}
		if yamlCfg.SearchAPIKey != "" {
			rawCfg.SearchAPIKey = yamlCfg.SearchAPIKey
		}
		if yamlCfg.InputCostPerToken > 0 {
			rawCfg.InputCostPerToken = yamlCfg.InputCostPerToken
		}
		if yamlCfg.OutputCostPerToken > 0 {
			rawCfg.OutputCostPerToken = yamlCfg.OutputCostPerToken
		}
		if yamlCfg.ContextWindow > 0 {
			rawCfg.ContextWindow = yamlCfg.ContextWindow
		}
		if yamlCfg.CommandTimeoutSeconds > 0 {
			rawCfg.CommandTimeoutSeconds = yamlCfg.CommandTimeoutSeconds
		}
		if yamlCfg.SessionRetentionDays > 0 {
			rawCfg.SessionRetentionDays = yamlCfg.SessionRetentionDays
		}
		if yamlCfg.LogMaxBytes > 0 {
			rawCfg.LogMaxBytes = yamlCfg.LogMaxBytes
		}
		if yamlCfg.LogMaxBackups > 0 {
			rawCfg.LogMaxBackups = yamlCfg.LogMaxBackups
		}
		if yamlCfg.BrowserMode != "" {
			rawCfg.BrowserMode = yamlCfg.BrowserMode
		}
		if yamlCfg.ChromeCDPPort > 0 {
			rawCfg.ChromeCDPPort = yamlCfg.ChromeCDPPort
		}
		if yamlCfg.Coder.Model != "" {
			rawCfg.Coder = yamlCfg.Coder
		}
		if yamlCfg.Reviewer.Model != "" {
			rawCfg.Reviewer = yamlCfg.Reviewer
		}

		// New multi-provider fields.
		if len(yamlCfg.Providers) > 0 {
			rawCfg.Providers = yamlCfg.Providers
		}
		if yamlCfg.ActiveProvider != "" {
			rawCfg.ActiveProvider = yamlCfg.ActiveProvider
		}
		if yamlCfg.ReasoningLevel != "" {
			rawCfg.ReasoningLevel = yamlCfg.ReasoningLevel
		}
		if yamlCfg.ThinkingMode != "" {
			rawCfg.ThinkingMode = yamlCfg.ThinkingMode
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("error reading config file at %s: %w", path, err)
	}

	// Apply defaults if empty.
	if rawCfg.BaseURL == "" {
		rawCfg.BaseURL = DefaultBaseURL
	}
	if rawCfg.Model == "" {
		rawCfg.Model = DefaultModel
	}
	if rawCfg.ContextWindow <= 0 {
		rawCfg.ContextWindow = DefaultContextWindow
	}
	if rawCfg.CommandTimeoutSeconds <= 0 {
		rawCfg.CommandTimeoutSeconds = DefaultCommandTimeoutSecs
	}
	if rawCfg.SessionRetentionDays <= 0 {
		rawCfg.SessionRetentionDays = DefaultSessionRetentionDays
	}
	if rawCfg.LogMaxBytes <= 0 {
		rawCfg.LogMaxBytes = DefaultLogMaxBytes
	}
	if rawCfg.LogMaxBackups <= 0 {
		rawCfg.LogMaxBackups = DefaultLogMaxBackups
	}
	if rawCfg.BrowserMode == "" {
		rawCfg.BrowserMode = DefaultBrowserMode
	}
	if rawCfg.ChromeCDPPort <= 0 {
		rawCfg.ChromeCDPPort = DefaultChromeCDPPort
	}

	// Normalize the multi-provider map. If a user only has the
	// legacy top-level fields, fold them into an implicit
	// opencode_zen provider entry so the runtime + /models
	// command can read from a single source of truth.
	if len(rawCfg.Providers) == 0 {
		rawCfg.Providers = map[string]ProviderConfig{
			DefaultActiveProvider: {
				BaseURL:        rawCfg.BaseURL,
				APIKey:         rawCfg.APIKey,
				DefaultModel:   rawCfg.Model,
				ReasoningLevel: rawCfg.ReasoningLevel,
				ThinkingMode:   rawCfg.ThinkingMode,
			},
		}
	} else {
		// Backfill any missing field on each provider from the
		// top-level legacy defaults so an old config.yaml with
		// `providers: {opencode_zen: {}}` still works.
		for name, p := range rawCfg.Providers {
			if p.BaseURL == "" {
				p.BaseURL = rawCfg.BaseURL
			}
			if p.APIKey == "" {
				p.APIKey = rawCfg.APIKey
			}
			if p.DefaultModel == "" {
				p.DefaultModel = rawCfg.Model
			}
			if p.ReasoningLevel == "" {
				p.ReasoningLevel = rawCfg.ReasoningLevel
			}
			if p.ThinkingMode == "" {
				p.ThinkingMode = rawCfg.ThinkingMode
			}
			rawCfg.Providers[name] = p
		}
	}

	if rawCfg.ActiveProvider == "" {
		rawCfg.ActiveProvider = DefaultActiveProvider
	}

	// Validate active provider exists.
	active, ok := rawCfg.Providers[rawCfg.ActiveProvider]
	if !ok {
		// Fall back to the first provider alphabetically, with a
		// clear, actionable error in the system log.
		names := make([]string, 0, len(rawCfg.Providers))
		for n := range rawCfg.Providers {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("active_provider %q not found in providers map (available: %s)",
			rawCfg.ActiveProvider, strings.Join(names, ", "))
	}

	// Normalize reasoning/thinking on the active provider.
	if rawCfg.ReasoningLevel == "" {
		rawCfg.ReasoningLevel = active.ReasoningLevel
	}
	if rawCfg.ThinkingMode == "" {
		rawCfg.ThinkingMode = active.ThinkingMode
	}

	// Build Coder/Reviewer from the active provider.
	coder := rawCfg.Coder
	if coder.Name == "" {
		coder.Name = "Coder"
	}
	coder.BaseURL = active.BaseURL
	coder.APIKey = active.APIKey
	if coder.Model == "" {
		if active.DefaultModel != "" {
			coder.Model = active.DefaultModel
		} else {
			coder.Model = rawCfg.Model
		}
	}
	coder.ReasoningLevel = rawCfg.ReasoningLevel
	coder.ThinkingMode = rawCfg.ThinkingMode
	if coder.InputCostPerToken <= 0 && rawCfg.InputCostPerToken > 0 {
		coder.InputCostPerToken = rawCfg.InputCostPerToken
	}
	if coder.OutputCostPerToken <= 0 && rawCfg.OutputCostPerToken > 0 {
		coder.OutputCostPerToken = rawCfg.OutputCostPerToken
	}
	if coder.ContextWindow <= 0 {
		if rawCfg.ContextWindow > 0 {
			coder.ContextWindow = rawCfg.ContextWindow
		} else {
			coder.ContextWindow = DefaultContextWindow
		}
	}
	coder.HasTools = true
	coder.SystemPrompt = CoderSystemPrompt
	rawCfg.Coder = coder

	reviewer := rawCfg.Reviewer
	if reviewer.Name == "" {
		reviewer.Name = "Reviewer"
	}
	reviewer.BaseURL = active.BaseURL
	reviewer.APIKey = active.APIKey
	if reviewer.Model == "" {
		if active.DefaultModel != "" {
			reviewer.Model = active.DefaultModel
		} else {
			reviewer.Model = rawCfg.Model
		}
	}
	reviewer.ReasoningLevel = rawCfg.ReasoningLevel
	reviewer.ThinkingMode = rawCfg.ThinkingMode
	if reviewer.InputCostPerToken <= 0 && rawCfg.InputCostPerToken > 0 {
		reviewer.InputCostPerToken = rawCfg.InputCostPerToken
	}
	if reviewer.OutputCostPerToken <= 0 && rawCfg.OutputCostPerToken > 0 {
		reviewer.OutputCostPerToken = rawCfg.OutputCostPerToken
	}
	if reviewer.ContextWindow <= 0 {
		if rawCfg.ContextWindow > 0 {
			reviewer.ContextWindow = rawCfg.ContextWindow
		} else {
			reviewer.ContextWindow = DefaultContextWindow
		}
	}
	reviewer.HasTools = false
	reviewer.SystemPrompt = ReviewerSystemPrompt
	rawCfg.Reviewer = reviewer

	return &rawCfg, nil
}

// SaveConfig writes cfg back to path. It uses a yaml.Node round-trip
// when the file already exists so that fields the user has set that
// this struct doesn't model (custom keys, comments, etc.) are
// preserved. New files are emitted with the canonical layout.
//
// Behavior:
//   - Always writes the active provider's base_url / api_key / model
//     to the top level too, so the legacy single-provider fields stay
//     valid for any other tool that reads them.
//   - Always writes the full Providers map. If the user originally
//     only had top-level fields, the first SaveConfig promotes them
//     into an explicit `providers: {opencode_zen: ...}` block.
//   - When path doesn't exist, creates it with mode 0644.
func SaveConfig(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("SaveConfig: cfg is nil")
	}

	// Always reflect the active provider's effective values into
	// the top-level fields too. This is the "design (a)" we agreed
	// on: any other reader that still looks at base_url / api_key /
	// model at the root sees the current active provider.
	if active, ok := cfg.Providers[cfg.ActiveProvider]; ok {
		if active.BaseURL != "" {
			cfg.BaseURL = active.BaseURL
		}
		if active.APIKey != "" {
			cfg.APIKey = active.APIKey
		}
		if active.DefaultModel != "" {
			cfg.Model = active.DefaultModel
		}
		if active.ReasoningLevel != "" {
			cfg.ReasoningLevel = active.ReasoningLevel
		} else {
			cfg.ReasoningLevel = ""
		}
		if active.ThinkingMode != "" {
			cfg.ThinkingMode = active.ThinkingMode
		} else {
			cfg.ThinkingMode = ""
		}
	}

	// Round-trip via yaml.Node so unknown keys are preserved.
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("SaveConfig: read existing file: %w", err)
	}

	if os.IsNotExist(err) {
		// Fresh file: emit canonical layout.
		out, mErr := yaml.Marshal(cfg)
		if mErr != nil {
			return fmt.Errorf("SaveConfig: marshal: %w", mErr)
		}
		if wErr := os.WriteFile(path, out, 0644); wErr != nil {
			return fmt.Errorf("SaveConfig: write %s: %w", path, wErr)
		}
		return nil
	}

	// Existing file: parse, mutate, re-emit preserving order.
	var root yaml.Node
	if uErr := yaml.Unmarshal(data, &root); uErr != nil {
		return fmt.Errorf("SaveConfig: parse existing yaml: %w", uErr)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("SaveConfig: existing yaml has unexpected structure")
	}
	topMap := root.Content[0]
	if topMap.Kind != yaml.MappingNode {
		return fmt.Errorf("SaveConfig: existing yaml root is not a mapping")
	}

	// Overwrite the legacy single-provider top-level keys + the
	// new multi-provider keys we own. Leave any other top-level
	// keys (the user's custom fields) untouched.
	overwrites := map[string]interface{}{
		"base_url":        cfg.BaseURL,
		"api_key":         cfg.APIKey,
		"model":           cfg.Model,
		"search_api_key":  cfg.SearchAPIKey,
		"active_provider": cfg.ActiveProvider,
		"reasoning_level": cfg.ReasoningLevel,
		"thinking_mode":   cfg.ThinkingMode,
		"providers":       cfg.Providers,
	}
	// Preserve cost / context / timeout / browser / port when
	// already present in the file. These come from cfg directly
	// (the loader fills them in).
	if cfg.InputCostPerToken > 0 {
		overwrites["input_cost_per_token"] = cfg.InputCostPerToken
	}
	if cfg.OutputCostPerToken > 0 {
		overwrites["output_cost_per_token"] = cfg.OutputCostPerToken
	}
	if cfg.ContextWindow > 0 {
		overwrites["context_window"] = cfg.ContextWindow
	}
	if cfg.CommandTimeoutSeconds > 0 {
		overwrites["command_timeout_seconds"] = cfg.CommandTimeoutSeconds
	}
	if cfg.SessionRetentionDays > 0 {
		overwrites["session_retention_days"] = cfg.SessionRetentionDays
	}
	if cfg.LogMaxBytes > 0 {
		overwrites["log_max_bytes"] = cfg.LogMaxBytes
	}
	if cfg.LogMaxBackups > 0 {
		overwrites["log_max_backups"] = cfg.LogMaxBackups
	}
	if cfg.BrowserMode != "" {
		overwrites["browser_mode"] = cfg.BrowserMode
	}
	if cfg.ChromeCDPPort > 0 {
		overwrites["chrome_cdp_port"] = cfg.ChromeCDPPort
	}

	mergeIntoMap(topMap, overwrites)

	out, mErr := yaml.Marshal(&root)
	if mErr != nil {
		return fmt.Errorf("SaveConfig: marshal: %w", mErr)
	}
	if wErr := os.WriteFile(path, out, 0644); wErr != nil {
		return fmt.Errorf("SaveConfig: write %s: %w", path, wErr)
	}
	return nil
}

// mergeIntoMap overwrites or appends the given key/value pairs into a
// yaml.MappingNode, preserving the order of untouched keys.
func mergeIntoMap(m *yaml.Node, kv map[string]interface{}) {
	if m.Kind != yaml.MappingNode {
		return
	}
	for k, v := range kv {
		setMapValue(m, k, v)
	}
}

// setMapValue sets mapping[key] = value, either by overwriting the
// existing entry (if the key is present) or by appending a new entry.
// Preserves the relative order of all other keys.
func setMapValue(m *yaml.Node, key string, value interface{}) {
	if m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Value == key {
			// Overwrite value at i+1 with a fresh node.
			m.Content[i+1] = valueToNode(value)
			return
		}
	}
	// Append.
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		valueToNode(value),
	)
}

func valueToNode(v interface{}) *yaml.Node {
	switch x := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: x, Tag: "!!str"}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", x), Tag: "!!int"}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", x), Tag: "!!int"}
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%g", x), Tag: "!!float"}
	case bool:
		v := "false"
		if x {
			v = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!bool"}
	case map[string]ProviderConfig:
		// Emit as a mapping of provider name -> provider fields.
		node := &yaml.Node{Kind: yaml.MappingNode}
		// Stable order: sort keys.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := x[k]
			pnode := &yaml.Node{Kind: yaml.MappingNode}
			if p.BaseURL != "" {
				pnode.Content = append(pnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "base_url", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: p.BaseURL, Tag: "!!str"},
				)
			}
			if p.APIKey != "" {
				pnode.Content = append(pnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "api_key", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: p.APIKey, Tag: "!!str"},
				)
			}
			if p.DefaultModel != "" {
				pnode.Content = append(pnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "default_model", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: p.DefaultModel, Tag: "!!str"},
				)
			}
			if p.ReasoningLevel != "" {
				pnode.Content = append(pnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "reasoning_level", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: p.ReasoningLevel, Tag: "!!str"},
				)
			}
			if p.ThinkingMode != "" {
				pnode.Content = append(pnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "thinking_mode", Tag: "!!str"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: p.ThinkingMode, Tag: "!!str"},
				)
			}
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"},
				pnode,
			)
		}
		return node
	default:
		// Fall back to yaml.Marshal for anything we don't handle
		// explicitly. Cheap, only used for edge cases.
		out, err := yaml.Marshal(v)
		if err != nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v), Tag: "!!str"}
		}
		var n yaml.Node
		if uErr := yaml.Unmarshal(out, &n); uErr != nil || len(n.Content) == 0 {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: strings.TrimSpace(string(out)), Tag: "!!str"}
		}
		return n.Content[0]
	}
}

// ProviderNames returns the configured provider names in stable order.
func (c *Config) ProviderNames() []string {
	if c == nil || len(c.Providers) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.Providers))
	for n := range c.Providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ActiveProviderConfig returns the currently active provider's config.
// Returns zero-value and false if there's no active provider.
func (c *Config) ActiveProviderConfig() (ProviderConfig, bool) {
	if c == nil || c.ActiveProvider == "" {
		return ProviderConfig{}, false
	}
	p, ok := c.Providers[c.ActiveProvider]
	return p, ok
}

// ModelSupportsReasoning reports whether the given model id is known
// to support reasoning controls. The list is small and conservative:
// we err on the side of "show the picker" so users can still set
// reasoning on new models we don't know about yet.
func ModelSupportsReasoning(modelID string) bool {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "mimo-v2.5-pro",
		"mimo-v2.5",
		"mimo-v2-pro",
		"mimo-v2-omni":
		return true
	case "mimo-v2-flash":
		// mimo-v2-flash is documented as not supporting reasoning.
		return false
	}
	// Unknown model — assume yes, let the API decide.
	return true
}
