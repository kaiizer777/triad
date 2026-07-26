package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/transcript"
)

// pickerMockClient is a deterministic test client for the picker.
// It always reports a single provider (test_prov) with two
// models, and never errors.
type pickerMockClient struct{}

func (pickerMockClient) Respond(_ context.Context, _ agent.AgentConfig, _ []transcript.Entry) (agent.AgentResponse, error) {
	return agent.AgentResponse{Text: "ok"}, nil
}
func (pickerMockClient) ListModels(_ context.Context, _ agent.AgentConfig) ([]agent.ModelInfo, error) {
	return []agent.ModelInfo{
		{ID: "mimo-v2.5-free", OwnedBy: "test_prov"},
		{ID: "mimo-v2.5-pro", OwnedBy: "test_prov"},
	}, nil
}
func (pickerMockClient) ListAllModels(_ context.Context, _ *agent.Config) ([]agent.AnnotatedModel, []agent.ModelError) {
	return []agent.AnnotatedModel{
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-free", OwnedBy: "test_prov"}},
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-pro", OwnedBy: "test_prov"}},
		},
		nil
}

// makePickerTestModel builds a model wired up with a real
// in-memory config so the picker has something to mutate.
func makePickerTestModel(t *testing.T) (Model, string) {
	t.Helper()
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "session.jsonl")
	tr := transcript.NewTranscript(sessionPath)

	cfg := &agent.Config{
		Providers: map[string]agent.ProviderConfig{
			"test_prov": {
				BaseURL:        "http://test",
				APIKey:         "k",
				DefaultModel:   "mimo-v2.5-free",
				ReasoningLevel: agent.ReasoningLevelMedium,
				ThinkingMode:   agent.ThinkingModeDisabled,
			},
		},
		ActiveProvider: "test_prov",
		Model:          "mimo-v2.5-free",
		ReasoningLevel: agent.ReasoningLevelMedium,
		ThinkingMode:   agent.ThinkingModeDisabled,
	}
	coder := agent.AgentConfig{Name: "Coder", HasTools: true,
		BaseURL: cfg.Providers["test_prov"].BaseURL, APIKey: "k", Model: "mimo-v2.5-free",
		ReasoningLevel: agent.ReasoningLevelMedium, ThinkingMode: agent.ThinkingModeDisabled}
	reviewer := agent.AgentConfig{Name: "Reviewer", HasTools: false,
		BaseURL: cfg.Providers["test_prov"].BaseURL, APIKey: "k", Model: "mimo-v2.5-free"}

	configPath := filepath.Join(tmpDir, "config.yaml")
	m := NewModel(tr, coder, reviewer, pickerMockClient{}, tmpDir, 0,
		&commands.Registry{}, configPath, cfg)
	// Drive the WindowSizeMsg to initialize viewports.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	return m, configPath
}

// TestPicker_StartModelsLoadsModels exercises the /models entry
// point. After startModelPicker returns, the picker should be in
// loading state; after the async message arrives, it should be in
// the model-list state with the right rows.
func TestPicker_StartModelsLoadsModels(t *testing.T) {
	m, _ := makePickerTestModel(t)
	_ = m.startModelPicker(false)
	if m.picker == nil {
		t.Fatal("expected picker to be set after startModelPicker")
	}
	if m.picker.Step != pickerStepLoading {
		t.Errorf("expected loading step, got %v", m.picker.Step)
	}

	// Drive the pickerModelsReadyMsg through Update.
	updated, _ := m.Update(pickerModelsReadyMsg{
		Models: []agent.AnnotatedModel{
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-free"}},
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-pro"}},
		},
	})
	m = updated.(Model)
	if m.picker == nil {
		t.Fatal("expected picker to remain set after models ready")
	}
	if m.picker.Step != pickerStepModel {
		t.Errorf("expected pickerStepModel, got %v", m.picker.Step)
	}
	if len(m.picker.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(m.picker.Models))
	}
}

// TestPicker_EnterAdvancesSteps exercises the keyboard flow:
// 1. enter (model select) → reasoning step
// 2. enter (reasoning select) → thinking step
// 3. enter (thinking select) → apply + write to disk
func TestPicker_EnterAdvancesSteps(t *testing.T) {
	m, configPath := makePickerTestModel(t)
	_ = m.startModelPicker(false)
	updated, _ := m.Update(pickerModelsReadyMsg{
		Models: []agent.AnnotatedModel{
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-pro"}},
		},
	})
	m = updated.(Model)
	if m.picker.Step != pickerStepModel {
		t.Fatalf("expected model step, got %v", m.picker.Step)
	}

	// Step 1: pick the model (index 0).
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if m.picker.Step != pickerStepReasoning {
		t.Fatalf("after first enter, expected reasoning step, got %v", m.picker.Step)
	}

	// Step 2: pick reasoning level (index 3 = high).
	m.picker.Index = 3
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if m.picker.Step != pickerStepThinking {
		t.Fatalf("after second enter, expected thinking step, got %v", m.picker.Step)
	}

	// Step 3: pick thinking mode (index 1 = enabled).
	m.picker.Index = 1
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updated.(Model)
	if m.picker != nil {
		t.Fatalf("expected picker to be nil after apply, got %+v", m.picker)
	}

	// Verify the live config was updated.
	if m.agentCfg.Model != "mimo-v2.5-pro" {
		t.Errorf("expected cfg.Model = mimo-v2.5-pro, got %q", m.agentCfg.Model)
	}
	if m.agentCfg.ReasoningLevel != agent.ReasoningLevelHigh {
		t.Errorf("expected cfg.ReasoningLevel = high, got %q", m.agentCfg.ReasoningLevel)
	}
	if m.agentCfg.ThinkingMode != agent.ThinkingModeEnabled {
		t.Errorf("expected cfg.ThinkingMode = enabled, got %q", m.agentCfg.ThinkingMode)
	}
	// SaveConfig should have written config.yaml to disk.
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected config.yaml to exist after picker apply, got: %v", err)
	}
}

// TestPicker_EscCancels verifies the Escape key dismisses the
// picker without applying any change.
func TestPicker_EscCancels(t *testing.T) {
	m, _ := makePickerTestModel(t)
	_ = m.startModelPicker(false)
	updated, _ := m.Update(pickerModelsReadyMsg{
		Models: []agent.AnnotatedModel{
			{Provider: "test_prov", Info: agent.ModelInfo{ID: "mimo-v2.5-pro"}},
		},
	})
	m = updated.(Model)

	before := m.agentCfg.Model
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	if m.picker != nil {
		t.Errorf("expected picker to be nil after esc, got %+v", m.picker)
	}
	if m.agentCfg.Model != before {
		t.Errorf("expected model to be unchanged after esc, was %q, now %q", before, m.agentCfg.Model)
	}
}

// TestPicker_ProviderListStep verifies /provider (no args) lands
// in the provider-list step with one row per configured provider.
func TestPicker_ProviderListStep(t *testing.T) {
	m, _ := makePickerTestModel(t)
	_ = m.startProviderPicker("") // no name → list step
	if m.picker == nil {
		t.Fatal("expected picker set after startProviderPicker")
	}
	if m.picker.Step != pickerStepProviderList {
		t.Errorf("expected provider-list step, got %v", m.picker.Step)
	}
	if len(m.picker.Models) != 1 {
		t.Errorf("expected 1 provider row, got %d", len(m.picker.Models))
	}
	if m.picker.Models[0].Provider != "test_prov" {
		t.Errorf("expected provider name test_prov, got %q", m.picker.Models[0].Provider)
	}
}
