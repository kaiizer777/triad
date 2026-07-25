package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/transcript"
)

// Styles holds Lipgloss formatting styles for the TUI components.
type Styles struct {
	// Title Bar
	TitleBrand       lipgloss.Style
	TitleVersion     lipgloss.Style
	TitleCenter      lipgloss.Style
	TitleKeycapKey   lipgloss.Style
	TitleKeycapLabel lipgloss.Style

	// Sidebar
	SidebarContainer   lipgloss.Style
	SidebarHeader      lipgloss.Style
	SidebarSubHeader   lipgloss.Style
	SidebarLabel       lipgloss.Style
	SidebarValue       lipgloss.Style
	SidebarBadgeIdle   lipgloss.Style
	SidebarBadgeActive lipgloss.Style
	SidebarBadgeThink  lipgloss.Style
	SidebarMeterFill   lipgloss.Style
	SidebarMeterEmpty  lipgloss.Style

	// Speaker Pills & Timestamps
	YouPill      lipgloss.Style
	CoderPill    lipgloss.Style
	ReviewerPill lipgloss.Style
	SystemPill   lipgloss.Style
	Timestamp    lipgloss.Style

	// Message Feed Callouts & Accents
	UserCalloutBox     lipgloss.Style
	YouMessageBar      lipgloss.Style
	CoderMessageBar    lipgloss.Style
	ReviewerMessageBar lipgloss.Style
	ApprovedBadge      lipgloss.Style
	ObjectionBadge     lipgloss.Style

	// Markdown Formatting
	MdBold       lipgloss.Style
	MdInlineCode lipgloss.Style
	MdCodeBlock  lipgloss.Style
	MdBullet     lipgloss.Style

	// Tool Call Cards
	ToolCallBox    lipgloss.Style
	ToolCallHeader lipgloss.Style
	ToolCallFunc   lipgloss.Style
	ToolCallKey    lipgloss.Style
	ToolCallVal    lipgloss.Style
	ToolCallNum    lipgloss.Style

	// Input & Status Bar
	InputContainer lipgloss.Style
	InputPill      lipgloss.Style
	InputPrompt    lipgloss.Style
	InputHint      lipgloss.Style

	// Pipeline Dock
	PipelineStepActive  lipgloss.Style
	PipelineStepPending lipgloss.Style
	PipelineStepDone    lipgloss.Style
	PipelineArrow       lipgloss.Style
	PipelineDock        lipgloss.Style

	ViewportContainer lipgloss.Style
	EntryContent      lipgloss.Style
	ActionResult      lipgloss.Style
	ErrorContent      lipgloss.Style
	StatusBar         lipgloss.Style
	SpinnerStyle      lipgloss.Style

	// Welcome screen
	WelcomeTitle lipgloss.Style
	WelcomeSub   lipgloss.Style
	WelcomeTip   lipgloss.Style
}

// DefaultStyles returns an ultra-premium obsidian dark palette with glowing accents,
// inspired by Claude Code / Cursor CLI / Warp Terminal / Aider aesthetics.
func DefaultStyles() Styles {
	var (
		obsidian    = lipgloss.Color("#080C14")
		obsidianAlt = lipgloss.Color("#0D1526")
		surface     = lipgloss.Color("#111827")
		surfaceHigh = lipgloss.Color("#172032")
		border      = lipgloss.Color("#1E2D45")
		borderSoft  = lipgloss.Color("#253348")
		muted       = lipgloss.Color("#4B5E78")
		mutedSoft   = lipgloss.Color("#5A7090")
		textPrimary = lipgloss.Color("#E8EDF5")
		textDim     = lipgloss.Color("#8899B4")

		violetMd = lipgloss.Color("#8B5CF6")
		violetLt = lipgloss.Color("#A78BFA")
		violetBg = lipgloss.Color("#1A0A3A")

		cyan   = lipgloss.Color("#0891B2")
		cyanLt = lipgloss.Color("#67E8F9")

		emerald   = lipgloss.Color("#059669")
		emeraldLt = lipgloss.Color("#6EE7B7")
		emeraldBg = lipgloss.Color("#022C22")

		amber   = lipgloss.Color("#D97706")
		amberLt = lipgloss.Color("#FCD34D")

		blue   = lipgloss.Color("#2563EB")
		blueMd = lipgloss.Color("#3B82F6")
		blueLt = lipgloss.Color("#93C5FD")
		blueBg = lipgloss.Color("#0A1628")

		red   = lipgloss.Color("#BE123C")
		redLt = lipgloss.Color("#FDA4AF")
		redBg = lipgloss.Color("#2D0A14")

		pink = lipgloss.Color("#EC4899")
	)

	return Styles{
		// ── Header Strip ──────────────────────────────────────────────
		TitleBrand: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(violetMd).
			Padding(0, 2),

		TitleVersion: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(cyanLt).
			Padding(0, 1),

		TitleCenter: lipgloss.NewStyle().
			Foreground(textDim).
			Background(obsidianAlt).
			Padding(0, 1),

		TitleKeycapKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt).
			Background(surfaceHigh).
			Padding(0, 1),

		TitleKeycapLabel: lipgloss.NewStyle().
			Foreground(mutedSoft).
			Background(obsidianAlt).
			Padding(0, 1),

		// ── Sidebar Panel ─────────────────────────────────────────────
		SidebarContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Background(obsidian).
			Padding(0, 1),

		SidebarHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt),

		SidebarSubHeader: lipgloss.NewStyle().
			Foreground(muted),

		SidebarLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(textDim),

		SidebarValue: lipgloss.NewStyle().
			Foreground(textPrimary),

		SidebarBadgeIdle: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(emerald).
			Padding(0, 1),

		SidebarBadgeActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(amber).
			Padding(0, 1),

		SidebarBadgeThink: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(violetMd).
			Padding(0, 1),

		SidebarMeterFill: lipgloss.NewStyle().
			Foreground(emeraldLt),

		SidebarMeterEmpty: lipgloss.NewStyle().
			Foreground(border),

		// ── Speaker Name Pills ────────────────────────────────────────
		YouPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(blue).
			Padding(0, 1),

		CoderPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(violetMd).
			Padding(0, 1),

		ReviewerPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(amberLt).
			Padding(0, 1),

		SystemPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(cyan).
			Padding(0, 1),

		Timestamp: lipgloss.NewStyle().
			Foreground(muted).
			Italic(true),

		// ── Message Feed Callouts & Accents ──────────────────────────
		UserCalloutBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(blueMd).
			Background(blueBg).
			Foreground(textPrimary).
			Padding(0, 2),

		YouMessageBar: lipgloss.NewStyle().
			Foreground(blueLt).
			Bold(true),

		CoderMessageBar: lipgloss.NewStyle().
			Foreground(violetLt).
			Bold(true),

		ReviewerMessageBar: lipgloss.NewStyle().
			Foreground(amberLt).
			Bold(true),

		ApprovedBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(emeraldLt).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(emerald).
			Background(emeraldBg).
			Padding(0, 2),

		ObjectionBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(redLt).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(red).
			Background(redBg).
			Padding(0, 2),

		// ── Markdown Formatting ───────────────────────────────────────
		MdBold: lipgloss.NewStyle().
			Bold(true).
			Foreground(textPrimary),

		MdInlineCode: lipgloss.NewStyle().
			Foreground(cyanLt).
			Background(surface).
			Padding(0, 1),

		MdCodeBlock: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderSoft).
			Background(obsidianAlt).
			Padding(0, 1),

		MdBullet: lipgloss.NewStyle().
			Foreground(violetLt),

		// ── Tool Action Card Panel ────────────────────────────────────
		ToolCallBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(violetMd).
			Background(violetBg).
			Padding(0, 1),

		ToolCallHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt),

		ToolCallFunc: lipgloss.NewStyle().
			Bold(true).
			Foreground(cyanLt),

		ToolCallKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#A5B4FC")),

		ToolCallVal: lipgloss.NewStyle().
			Foreground(emeraldLt),

		ToolCallNum: lipgloss.NewStyle().
			Foreground(pink),

		// ── Input Box & Prompts ───────────────────────────────────────
		InputContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(blueMd).
			Background(obsidianAlt).
			Padding(0, 1),

		InputPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(blue).
			Padding(0, 1),

		InputPrompt: lipgloss.NewStyle().
			Bold(true).
			Foreground(cyanLt),

		InputHint: lipgloss.NewStyle().
			Foreground(muted).
			Italic(true),

		// ── Pipeline Dock Steps ───────────────────────────────────────
		PipelineStepActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(violetMd).
			Padding(0, 1),

		PipelineStepPending: lipgloss.NewStyle().
			Foreground(muted).
			Background(surface).
			Padding(0, 1),

		PipelineStepDone: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(emeraldLt).
			Padding(0, 1),

		PipelineArrow: lipgloss.NewStyle().
			Foreground(border),

		PipelineDock: lipgloss.NewStyle().
			Background(obsidian).
			Foreground(textDim).
			Padding(0, 1),

		// ── Transcript Viewport ───────────────────────────────────────
		ViewportContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Background(obsidian),

		EntryContent: lipgloss.NewStyle().
			Foreground(textPrimary),

		ActionResult: lipgloss.NewStyle().
			Foreground(textDim),

		ErrorContent: lipgloss.NewStyle().
			Bold(true).
			Foreground(redLt),

		StatusBar: lipgloss.NewStyle().
			Foreground(textDim).
			Background(obsidianAlt).
			Padding(0, 1),

		SpinnerStyle: lipgloss.NewStyle().
			Foreground(violetLt).
			Bold(true),

		// ── Welcome Screen ────────────────────────────────────────────
		WelcomeTitle: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt),

		WelcomeSub: lipgloss.NewStyle().
			Foreground(textDim),

		WelcomeTip: lipgloss.NewStyle().
			Foreground(mutedSoft).
			Italic(true),
	}
}

// Model represents the top-level Bubbletea application state.
type Model struct {
	transcript *transcript.Transcript
	coder      agent.AgentConfig
	reviewer   agent.AgentConfig
	client     loop.AgentClient
	workDir    string
	// commandTimeout caps run_command execution time (from config.yaml).
	commandTimeout time.Duration

	MaxRetries     int
	sessionState   loop.SessionState
	activeToolCall *agent.ToolCall
	retryCount     int
	plainTextTurns int // consecutive Coder plain-text turns with no tool call
	statusMessage  string
	initialCmd     tea.Cmd

	spinner  spinner.Model
	viewport viewport.Model
	input    textinput.Model
	styles   Styles

	width  int
	height int
	ready  bool
}

// NewModel initializes a new Model for the Bubbletea program.
func NewModel(
	tr *transcript.Transcript,
	coder agent.AgentConfig,
	reviewer agent.AgentConfig,
	client loop.AgentClient,
	workDir string,
	commandTimeout time.Duration,
) Model {
	styles := DefaultStyles()

	ti := textinput.New()
	ti.Placeholder = "Ask Triad to build a feature, edit code, or analyze tasks..."
	ti.Prompt = " ❯ "
	ti.SetWidth(40)
	ti.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(styles.SpinnerStyle),
	)

	m := Model{
		transcript:     tr,
		coder:          coder,
		reviewer:       reviewer,
		client:         client,
		workDir:        workDir,
		commandTimeout: commandTimeout,
		MaxRetries:     loop.DefaultMaxRetries,
		sessionState:   loop.StateIdle,
		statusMessage:  "Ready — Type your prompt below and press Enter.",
		spinner:        sp,
		input:          ti,
		styles:         styles,
	}

	m.RestoreSessionState()
	return m
}

// RestoreSessionState evaluates existing transcript entries and sets initial state, status, and Cmd.
func (m *Model) RestoreSessionState() {
	entries := m.transcript.Entries()
	if len(entries) == 0 {
		m.sessionState = loop.StateIdle
		m.statusMessage = "Ready — Type your prompt below and press Enter."
		m.initialCmd = nil
		return
	}

	last := entries[len(entries)-1]

	// Case 1: Human typed a message last -> Coder turn
	if last.Speaker == transcript.SpeakerYou {
		m.sessionState = loop.StateActive
		m.statusMessage = "Resuming session: Coder is thinking..."
		m.initialCmd = cmdCoderTurn(m.transcript, m.coder, m.client)
		return
	}

	// Case 2: Coder proposed an action last -> Reviewer turn
	if last.Type == transcript.TypeProposedAction {
		tc, err := loop.ParseProposedAction(last.Content)
		if err == nil {
			m.activeToolCall = tc
			m.retryCount = countObjectionsForProposal(entries)
			m.sessionState = loop.StateActive
			m.statusMessage = fmt.Sprintf("Resuming session: Reviewer inspecting proposed action %q...", tc.Function.Name)
			m.initialCmd = cmdReviewerTurn(m.transcript, m.reviewer, m.client)
			return
		}
	}

	// Case 3: Coder sent a plain-text message / plan last -> Coder turn
	if last.Speaker == transcript.SpeakerCoder && last.Type == transcript.TypeMessage {
		m.plainTextTurns = countRecentPlainTextTurns(entries)
		if m.plainTextTurns >= MaxPlainTextTurns {
			m.sessionState = loop.StateIdle
			m.statusMessage = "Resumed session: Coder stalled (no action proposed). Session idle."
			m.initialCmd = nil
			return
		}
		m.sessionState = loop.StateActive
		m.statusMessage = "Resuming session: Coder considering next step..."
		m.initialCmd = cmdCoderTurn(m.transcript, m.coder, m.client)
		return
	}

	// Case 4: Reviewer responded last
	if last.Speaker == transcript.SpeakerReviewer {
		decision := loop.ParseReviewerDecision(last.Content)
		if decision == loop.DecisionApprove {
			propEntry, found := findPrecedingProposedAction(entries)
			if found {
				tc, err := loop.ParseProposedAction(propEntry.Content)
				if err == nil && tc.Function.Name == "task_complete" {
					m.sessionState = loop.StateIdle
					m.statusMessage = "Resumed session: Task complete. Session idle."
					m.initialCmd = nil
					return
				}
				if err == nil {
					// Guard against double-execution: if an action_result already
					// exists after the proposed_action in question, the tool already
					// ran successfully before the crash. Resume at Coder turn instead.
					if actionResultExistsAfter(entries, propEntry) {
						m.sessionState = loop.StateActive
						m.statusMessage = "Resuming session: Action already executed. Coder considering next step..."
						m.initialCmd = cmdCoderTurn(m.transcript, m.coder, m.client)
						return
					}
					m.activeToolCall = tc
					m.sessionState = loop.StateActive
					m.statusMessage = fmt.Sprintf("Resuming session: Executing approved tool %q...", tc.Function.Name)
					m.initialCmd = cmdExecuteTool(m.workDir, *tc, m.commandTimeout)
					return
				}
			}
			m.sessionState = loop.StateIdle
			m.statusMessage = "Resumed session (Idle)."
			m.initialCmd = nil
			return
		}

		if decision == loop.DecisionObject {
			m.retryCount = countObjectionsForProposal(entries)
			m.sessionState = loop.StateActive
			m.statusMessage = fmt.Sprintf("Resuming session: Reviewer objected (%d/%d). Coder revising...", m.retryCount, m.MaxRetries)
			m.initialCmd = cmdCoderTurn(m.transcript, m.coder, m.client)
			return
		}
	}

	// Case 5: Action result returned last -> Coder turn
	if last.Type == transcript.TypeActionResult {
		m.sessionState = loop.StateActive
		m.statusMessage = "Resuming session: Action executed. Coder considering next step..."
		m.initialCmd = cmdCoderTurn(m.transcript, m.coder, m.client)
		return
	}

	// Case 6: Default fallback
	m.sessionState = loop.StateIdle
	m.statusMessage = "Resumed session (Idle)."
	m.initialCmd = nil
}

func countObjectionsForProposal(entries []transcript.Entry) int {
	count := 0
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Speaker == transcript.SpeakerYou || entries[i].Type == transcript.TypeActionResult {
			break
		}
		if entries[i].Speaker == transcript.SpeakerReviewer && loop.ParseReviewerDecision(entries[i].Content) == loop.DecisionObject {
			count++
		}
	}
	return count
}

func countRecentPlainTextTurns(entries []transcript.Entry) int {
	count := 0
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Speaker == transcript.SpeakerCoder && entries[i].Type == transcript.TypeMessage {
			count++
		} else {
			break
		}
	}
	return count
}

func findPrecedingProposedAction(entries []transcript.Entry) (transcript.Entry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == transcript.TypeProposedAction {
			return entries[i], true
		}
		if entries[i].Speaker == transcript.SpeakerYou || entries[i].Type == transcript.TypeActionResult {
			break
		}
	}
	return transcript.Entry{}, false
}

// actionResultExistsAfter reports whether any action_result entry appears after
// the given proposal entry in the transcript. This is used during crash-resume
// to detect if an approved tool was already executed before the process died.
func actionResultExistsAfter(entries []transcript.Entry, propEntry transcript.Entry) bool {
	seenProposal := false
	for i := 0; i < len(entries); i++ {
		if !seenProposal {
			if entries[i].ID == propEntry.ID && entries[i].Type == transcript.TypeProposedAction {
				seenProposal = true
			}
			continue
		}
		if entries[i].Type == transcript.TypeActionResult {
			return true
		}
		// A new proposed_action means we've moved into a new cycle — stop.
		if entries[i].Type == transcript.TypeProposedAction {
			break
		}
	}
	return false
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		m.spinner.Tick,
	}
	if m.initialCmd != nil {
		cmds = append(cmds, m.initialCmd)
	}
	return tea.Batch(cmds...)
}
