package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
)

// ---------------------------------------------------------------------------
// /models + /provider picker
//
// The picker is a small finite-state machine that walks the user
// through one to three steps:
//   1. Pick a model (across all configured providers when invoked
//      via /models, just the current provider's list when invoked
//      via /provider with no model specified).
//   2. If the chosen model supports reasoning, pick a reasoning
//      level (none / low / medium / high).
//   3. Pick the thinking-mode toggle (enabled / disabled).
//
// /provider skips step 1 if the user passes a provider name as
// the arg; otherwise it shows just step 1 but filters the model
// list to that provider's catalog and doesn't allow cross-provider
// switching.
//
// All steps share the same selectList state (List + SelectedIndex)
// and the same row renderer. The wizard lives on Model so the
// keypress handler in update.go can route arrow / enter / esc to
// it directly.
// ---------------------------------------------------------------------------

// pickerStep enumerates the wizard phases.
type pickerStep int

const (
	pickerStepLoading pickerStep = iota
	pickerStepModel
	pickerStepReasoning
	pickerStepThinking
	pickerStepProviderList
	pickerStepDone
)

// pickerResult is the final outcome of a successful /models or
// /provider run. The TUI applies it to the live config and saves
// to disk.
type pickerResult struct {
	// Provider is the provider key (e.g. "opencode_zen") that
	// the user implicitly chose. Equal to the previous
	// active_provider for "stay-on-current-provider" picks.
	Provider string
	// Model is the model id the user picked. Empty if the user
	// aborted.
	Model string
	// ReasoningLevel is "" when the chosen model doesn't support
	// reasoning or when the user didn't change the previous value.
	ReasoningLevel string
	// ThinkingMode is "" when the chosen model doesn't support
	// the toggle or when the user didn't change it.
	ThinkingMode string
	// SwitchedProvider is true when the user picked a model that
	// lives on a different provider than the previous active one.
	SwitchedProvider bool
}

// modelPickerState is the wizard's mutable state. One instance per
// TUI session; nil when no picker is active.
type modelPickerState struct {
	Step         pickerStep
	Models       []agent.AnnotatedModel // all models from all providers (for /models) or just one (for /provider)
	ProviderErrs []agent.ModelError
	Index        int   // cursor in the current list
	ReasoningIdx int   // 0=none, 1=low, 2=medium, 3=high
	ThinkingIdx  int   // 0=disabled, 1=enabled
	PrevReason   string
	PrevThinking string
	// IsProviderOnly is true when the picker was launched via
	// /provider (the model list is just the current provider's
	// catalog, and selecting a model does NOT switch the active
	// provider).
	IsProviderOnly bool
	// Result is set when the wizard finishes successfully.
	Result *pickerResult
}

// pickerRow is one entry in the picker's current list. Generic
// across all three steps.
type pickerRow struct {
	Label       string // primary text shown to the user
	Description string // secondary line / hint
	Badge       string // right-aligned "[provider]" or "[reasoning]" tag, can be empty
	Value       string // the underlying value (model id, "low", "enabled", etc.)
}

// rowsForStep returns the visible rows for the current step.
func (m *Model) rowsForStep(p *modelPickerState) []pickerRow {
	switch p.Step {
	case pickerStepModel, pickerStepProviderList:
		rows := make([]pickerRow, 0, len(p.Models))
		for _, am := range p.Models {
			rows = append(rows, pickerRow{
				Label:       am.Info.ID,
				Description: "owned by " + am.OwnedByLabel(),
				Badge:       am.Provider,
				Value:       am.Info.ID,
			})
		}
		return rows
	case pickerStepReasoning:
		return []pickerRow{
			{Label: "none", Description: "Disable thinking on this model", Badge: "reasoning", Value: agent.ReasoningLevelNone},
			{Label: "low", Description: "Low effort — fast, cheap", Badge: "reasoning", Value: agent.ReasoningLevelLow},
			{Label: "medium", Description: "Medium effort — balanced (default)", Badge: "reasoning", Value: agent.ReasoningLevelMedium},
			{Label: "high", Description: "High effort — slower, more thorough", Badge: "reasoning", Value: agent.ReasoningLevelHigh},
		}
	case pickerStepThinking:
		return []pickerRow{
			{Label: "disabled", Description: "Send a non-thinking request (faster, cheaper)", Badge: "thinking", Value: agent.ThinkingModeDisabled},
			{Label: "enabled", Description: "Send a thinking request (model returns reasoning_content)", Badge: "thinking", Value: agent.ThinkingModeEnabled},
		}
	}
	return nil
}

// maxIdxForStep returns the number of rows in the current step.
func (m *Model) maxIdxForStep(p *modelPickerState) int {
	return len(m.rowsForStep(p))
}

// startModelPicker begins the /models flow. It fires an async
// ListAllModels and switches the wizard into the loading step.
// When the response arrives, the picker's Update handler advances
// to the model-list step.
func (m *Model) startModelPicker(providerOnly bool) tea.Cmd {
	p := &modelPickerState{
		Step:           pickerStepLoading,
		Index:          0,
		ReasoningIdx:   2, // medium
		ThinkingIdx:    0, // disabled
		IsProviderOnly: providerOnly,
	}
	// Seed the wizard's reasoning / thinking cursors with the
	// current values, so the user sees their previous choice as
	// the default highlight.
	if m.agentCfg != nil {
		p.PrevReason = m.agentCfg.ReasoningLevel
		p.PrevThinking = m.agentCfg.ThinkingMode
		switch m.agentCfg.ReasoningLevel {
		case agent.ReasoningLevelNone:
			p.ReasoningIdx = 0
		case agent.ReasoningLevelLow:
			p.ReasoningIdx = 1
		case agent.ReasoningLevelMedium, "":
			p.ReasoningIdx = 2
		case agent.ReasoningLevelHigh:
			p.ReasoningIdx = 3
		}
		switch m.agentCfg.ThinkingMode {
		case agent.ThinkingModeDisabled, "":
			p.ThinkingIdx = 0
		case agent.ThinkingModeEnabled:
			p.ThinkingIdx = 1
		}
	}
	m.picker = p
	return m.fetchPickerModels()
}

// startProviderPicker begins the /provider flow. If providerName
// is non-empty, the wizard filters to that provider's models.
// If providerName is empty, the wizard shows the list of
// configured providers (not models) and the user picks one.
func (m *Model) startProviderPicker(providerName string) tea.Cmd {
	if m.agentCfg == nil {
		return m.systemStatusCmd("/provider unavailable: no live config attached to the TUI.")
	}
	// Sub-case A: just list providers (no model switching).
	if providerName == "" {
		names := m.agentCfg.ProviderNames()
		p := &modelPickerState{
			Step:  pickerStepProviderList,
			Index: indexOf(names, m.agentCfg.ActiveProvider),
		}
		m.picker = p
		m.picker.Models = make([]agent.AnnotatedModel, 0, len(names))
		for _, n := range names {
			m.picker.Models = append(m.picker.Models, agent.AnnotatedModel{
				Provider: n,
				Info:     agent.ModelInfo{ID: n, OwnedBy: m.providerTag(n)},
			})
		}
		return nil
	}
	// Sub-case B: pick a model within the named provider.
	// (This is the "switch model, stay on this provider" path —
	// /provider xiaomi_direct without changing active_provider.)
	p := &modelPickerState{
		Step:           pickerStepLoading,
		Index:          0,
		ReasoningIdx:   2,
		ThinkingIdx:    0,
		IsProviderOnly: true,
	}
	if m.agentCfg != nil {
		p.PrevReason = m.agentCfg.ReasoningLevel
		p.PrevThinking = m.agentCfg.ThinkingMode
	}
	m.picker = p
	return m.fetchPickerModelsFor(providerName)
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return 0
}

// providerTag returns a short human-readable label for a
// provider. Falls back to the key itself if we don't recognize
// it.
func (m *Model) providerTag(name string) string {
	// No hard-coded mapping today; just use the key. Future
	// addition: a friendly label per provider.
	return name
}

// fetchPickerModels fires a ListAllModels call against the live
// config. Wraps the result in a pickerModelsReadyMsg for the
// Update handler to pick up.
func (m *Model) fetchPickerModels() tea.Cmd {
	if m.agentCfg == nil {
		return func() tea.Msg {
			return pickerModelsReadyMsg{
				Errs: []agent.ModelError{{Provider: "(config)", Err: "no live config attached"}},
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		models, errs := m.client.ListAllModels(ctx, m.agentCfg)
		return pickerModelsReadyMsg{Models: models, Errs: errs}
	}
}

// fetchPickerModelsFor fetches the model list for a single
// provider and labels every row with that provider name. Used
// by /provider <name> to keep the wizard scoped to one provider.
func (m *Model) fetchPickerModelsFor(providerName string) tea.Cmd {
	if m.agentCfg == nil {
		return func() tea.Msg {
			return pickerModelsReadyMsg{Errs: []agent.ModelError{{Provider: providerName, Err: "no live config"}}}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		prov, ok := m.agentCfg.Providers[providerName]
		if !ok {
			return pickerModelsReadyMsg{Errs: []agent.ModelError{{Provider: providerName, Err: "provider not configured"}}}
		}
		ms, err := m.client.ListModels(ctx, agent.AgentConfig{BaseURL: prov.BaseURL, APIKey: prov.APIKey})
		if err != nil {
			return pickerModelsReadyMsg{Errs: []agent.ModelError{{Provider: providerName, Err: err.Error()}}}
		}
		ann := make([]agent.AnnotatedModel, 0, len(ms))
		for _, mm := range ms {
			ann = append(ann, agent.AnnotatedModel{Provider: providerName, Info: mm})
		}
		return pickerModelsReadyMsg{Models: ann}
	}
}

// pickerModelsReadyMsg is delivered when the async ListAllModels /
// ListModels call returns.
type pickerModelsReadyMsg struct {
	Models []agent.AnnotatedModel
	Errs   []agent.ModelError
}

// pickerErrorMsg is delivered when a save or other picker-internal
// step fails. Surfaces to the TUI as a System entry.
type pickerErrorMsg struct {
	Err string
}

// pickerApplyMsg is delivered when the wizard finishes and the new
// active provider / model has been persisted to config.yaml. The
// TUI's Coder / Reviewer configs are re-derived from the live
// config in this same Update step.
type pickerApplyMsg struct {
	Result pickerResult
}

// pickerKey handles up/down/enter/esc when the picker is active.
// Returns (consumed, cmd). consumed=true means the message was
// handled and the Update caller should NOT also process it as a
// text-input event.
func (m *Model) pickerKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.picker == nil {
		return false, nil
	}
	// Loading state — only Esc is meaningful.
	if m.picker.Step == pickerStepLoading {
		if msg.String() == "esc" {
			m.picker = nil
			return true, nil
		}
		return true, nil
	}

	maxIdx := m.maxIdxForStep(m.picker)
	switch msg.String() {
	case "up":
		if maxIdx == 0 {
			return true, nil
		}
		m.picker.Index--
		if m.picker.Index < 0 {
			m.picker.Index = maxIdx - 1
		}
		return true, nil
	case "down":
		if maxIdx == 0 {
			return true, nil
		}
		m.picker.Index++
		if m.picker.Index >= maxIdx {
			m.picker.Index = 0
		}
		return true, nil
	case "esc":
		m.picker = nil
		return true, nil
	case "enter":
		return true, m.advancePicker()
	}
	return true, nil // consume all other keys while picker is open
}

// advancePicker applies the current step's selection and either
// moves to the next step or finalizes the wizard.
func (m *Model) advancePicker() tea.Cmd {
	p := m.picker
	switch p.Step {
	case pickerStepModel, pickerStepProviderList:
		// Same handler for the model list and the provider list —
		// the rows are uniformly (Label, Value).
		if p.Index < 0 || p.Index >= len(p.Models) {
			m.picker = nil
			return nil
		}
		chosen := p.Models[p.Index]

		// Provider-list case: switching the active provider
		// without picking a new model.
		if p.Step == pickerStepProviderList {
			if chosen.Provider == m.agentCfg.ActiveProvider {
				m.picker = nil
				return m.systemStatusCmd("/provider: already on " + chosen.Provider + ".")
			}
			p.Result = &pickerResult{
				Provider:         chosen.Provider,
				Model:            m.agentCfg.Model,
				ReasoningLevel:   m.agentCfg.ReasoningLevel,
				ThinkingMode:     m.agentCfg.ThinkingMode,
				SwitchedProvider: true,
			}
			return m.applyPickerResult()
		}

		// Model-list case.
		supportsReasoning := agent.ModelSupportsReasoning(chosen.Info.ID)
		p.Result = &pickerResult{
			Provider:         chosen.Provider,
			Model:            chosen.Info.ID,
			SwitchedProvider: chosen.Provider != m.agentCfg.ActiveProvider,
		}
		if !supportsReasoning {
			// Skip reasoning + thinking steps.
			return m.applyPickerResult()
		}
		p.Step = pickerStepReasoning
		m.picker.Index = p.ReasoningIdx
		return nil

	case pickerStepReasoning:
		rows := m.rowsForStep(p)
		if p.Index < 0 || p.Index >= len(rows) {
			return nil
		}
		p.Result.ReasoningLevel = rows[p.Index].Value
		p.ReasoningIdx = p.Index
		p.Step = pickerStepThinking
		m.picker.Index = p.ThinkingIdx
		return nil

	case pickerStepThinking:
		rows := m.rowsForStep(p)
		if p.Index < 0 || p.Index >= len(rows) {
			return nil
		}
		p.Result.ThinkingMode = rows[p.Index].Value
		p.ThinkingIdx = p.Index
		return m.applyPickerResult()
	}
	return nil
}

// applyPickerResult persists the user's choice to disk and
// updates the live cfg + Coder/Reviewer configs. Returns a
// pickerApplyMsg that the TUI handles by writing a System entry.
func (m *Model) applyPickerResult() tea.Cmd {
	if m.picker == nil || m.picker.Result == nil {
		return nil
	}
	res := m.picker.Result
	cfg := m.agentCfg
	if cfg == nil {
		m.picker = nil
		return m.systemStatusCmd("applyPickerResult: no live config")
	}

	// 1. Update the target provider's defaults so the choice is
	// remembered next time.
	prov, ok := cfg.Providers[res.Provider]
	if !ok {
		m.picker = nil
		return m.systemStatusCmd(fmt.Sprintf("provider %q not configured", res.Provider))
	}
	if !m.picker.IsProviderOnly {
		prov.DefaultModel = res.Model
	}
	if res.ReasoningLevel != "" {
		prov.ReasoningLevel = res.ReasoningLevel
	}
	if res.ThinkingMode != "" {
		prov.ThinkingMode = res.ThinkingMode
	}
	cfg.Providers[res.Provider] = prov

	// 2. If the user picked a model from a non-active provider,
	// flip active_provider too. (Provider-only picker never
	// changes the provider, only the model.)
	if res.SwitchedProvider {
		cfg.ActiveProvider = res.Provider
	}
	// 3. Reflect the chosen model + reasoning/thinking into the
	// top-level fields so any other reader of cfg.Model /
	// cfg.ReasoningLevel sees the new values.
	if res.Model != "" {
		cfg.Model = res.Model
	}
	if res.ReasoningLevel != "" {
		cfg.ReasoningLevel = res.ReasoningLevel
	}
	if res.ThinkingMode != "" {
		cfg.ThinkingMode = res.ThinkingMode
	}

	// 4. Persist to disk.
	if m.configPath != "" {
		if err := agent.SaveConfig(m.configPath, cfg); err != nil {
			m.picker = nil
			return m.systemStatusCmd("SaveConfig failed: " + err.Error())
		}
	}

	// 5. Re-derive Coder / Reviewer from the new active provider
	// so the very next Coder turn goes to the right endpoint.
	active, _ := cfg.ActiveProviderConfig()
	m.coder.BaseURL = active.BaseURL
	m.coder.APIKey = active.APIKey
	m.coder.Model = active.DefaultModel
	m.coder.ReasoningLevel = cfg.ReasoningLevel
	m.coder.ThinkingMode = cfg.ThinkingMode
	m.reviewer.BaseURL = active.BaseURL
	m.reviewer.APIKey = active.APIKey
	m.reviewer.Model = active.DefaultModel
	m.reviewer.ReasoningLevel = cfg.ReasoningLevel
	m.reviewer.ThinkingMode = cfg.ThinkingMode

	// 6. Build a result message for the Update handler so the
	// TUI can append a System entry to the transcript.
	m.picker = nil
	return m.systemStatusCmd(fmt.Sprintf(
		"Switched to provider %q, model %q (reasoning=%s, thinking=%s). Saved to %s.",
		res.Provider, res.Model,
		displayOrNone(res.ReasoningLevel),
		displayOrNone(res.ThinkingMode),
		m.configPath,
	))
}

func displayOrNone(v string) string {
	if v == "" {
		return "(unchanged)"
	}
	return v
}

func (m *Model) systemStatusCmd(msg string) tea.Cmd {
	return func() tea.Msg {
		return systemStatusMsg{Message: msg}
	}
}

// systemStatusMsg is a System-style status string that the TUI
// surfaces in the transcript + status bar.
type systemStatusMsg struct {
	Message string
}

// pickerLaunchKind distinguishes the two picker entry points.
type pickerLaunchKind int

const (
	pickerLaunchModels pickerLaunchKind = iota
	pickerLaunchProvider
)

// pendingPickerLaunch is a one-shot signal the slash-command
// handler sets in handleSystemCommand. The TUI's Update loop
// picks it up after the System entry is written, fires the
// picker, and clears the field. Lives on Model so the TUI
// can route without growing the message union.
type pendingPickerLaunch struct {
	Kind         pickerLaunchKind
	ProviderName string // for /provider <name>; empty otherwise
}

// renderPickerPopup renders the active picker (loading or
// list) in the same vertical strip the autocomplete popup uses.
// Reuses the existing AutocompleteBox / Item / ItemSel styles
// so the picker looks consistent with the rest of the TUI.
func (m *Model) renderPickerPopup(width int) string {
	if m.picker == nil {
		return ""
	}
	frameHoriz := m.styles.AutocompleteBox.GetHorizontalFrameSize()
	innerWidth := max(10, width-frameHoriz)
	var sb strings.Builder

	header := m.pickerHeader()
	sb.WriteString(m.styles.AutocompleteHeader.Render(header))
	sb.WriteString("\n")

	if m.picker.Step == pickerStepLoading {
		sb.WriteString(m.styles.AutocompleteDesc.Render("  Loading models from providers… (press esc to cancel)"))
	} else {
		rows := m.rowsForStep(m.picker)
		maxVisible := 8
		startIdx := 0
		if len(rows) > maxVisible {
			startIdx = m.picker.Index - maxVisible/2
			if startIdx < 0 {
				startIdx = 0
			}
			if startIdx > len(rows)-maxVisible {
				startIdx = len(rows) - maxVisible
			}
		}
		endIdx := startIdx + maxVisible
		if endIdx > len(rows) {
			endIdx = len(rows)
		}

		for i := startIdx; i < endIdx; i++ {
			row := rows[i]
			badgeStr := ""
			if row.Badge != "" {
				badgeStr = "[" + row.Badge + "]"
			}
			badge := m.styles.AutocompleteBadge.Render(badgeStr)
			badgeW := lipgloss.Width(badge)

			var prefix, nameRendered string
			if i == m.picker.Index {
				prefix = "▸ "
				nameRendered = m.styles.AutocompleteItemSel.Render(row.Label)
			} else {
				prefix = "  "
				nameRendered = m.styles.AutocompleteItem.Render(row.Label)
			}
			nameW := lipgloss.Width(row.Label) + 2
			descW := max(0, innerWidth-nameW-badgeW-3)
			desc := m.styles.AutocompleteDesc.Render(truncateLine(row.Description, descW))

			line := lipgloss.JoinHorizontal(lipgloss.Top, prefix, nameRendered, " ", badge, " ", desc)
			sb.WriteString(line)
			if i != endIdx-1 {
				sb.WriteString("\n")
			}
		}
	}

	boxW := max(0, width-frameHoriz)
	return m.styles.AutocompleteBox.Width(boxW).Render(sb.String())
}

func (m *Model) pickerHeader() string {
	if m.picker == nil {
		return ""
	}
	switch m.picker.Step {
	case pickerStepLoading:
		return "▸ /MODELS — LOADING"
	case pickerStepModel:
		return "▸ /MODELS — PICK A MODEL"
	case pickerStepReasoning:
		return "▸ /MODELS — REASONING LEVEL"
	case pickerStepThinking:
		return "▸ /MODELS — THINKING MODE"
	case pickerStepProviderList:
		return "▸ /PROVIDER — PICK A PROVIDER"
	}
	return "▸ PICKER"
}
