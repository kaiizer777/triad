package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/browser"
	"github.com/kaiizer777/triad/internal/clarify"
	"github.com/kaiizer777/triad/internal/commands"
	"github.com/kaiizer777/triad/internal/gitcommit"
	"github.com/kaiizer777/triad/internal/journey"
	"github.com/kaiizer777/triad/internal/learn"
	"github.com/kaiizer777/triad/internal/loop"
	"github.com/kaiizer777/triad/internal/memory"
	"github.com/kaiizer777/triad/internal/tracelog"
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
	SidebarRule        lipgloss.Style
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
	CoderCalloutBox    lipgloss.Style
	ReviewerCalloutBox lipgloss.Style
	YouMessageBar      lipgloss.Style
	CoderMessageBar    lipgloss.Style
	ReviewerMessageBar lipgloss.Style
	ApprovedBadge      lipgloss.Style
	ObjectionBadge     lipgloss.Style
	EntryDivider       lipgloss.Style

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
	InputContainer  lipgloss.Style
	InputPill       lipgloss.Style
	InputPrompt     lipgloss.Style
	InputHint       lipgloss.Style
	InputSeparator  lipgloss.Style

	ViewportContainer  lipgloss.Style
	RightCardContainer lipgloss.Style
	EntryContent       lipgloss.Style
	ActionResult       lipgloss.Style
	ActionResultBar    lipgloss.Style
	ErrorContent       lipgloss.Style
	StatusBar          lipgloss.Style
	SpinnerStyle       lipgloss.Style

	// Welcome screen
	WelcomeTitle lipgloss.Style
	WelcomeSub   lipgloss.Style
	WelcomeTip   lipgloss.Style

	// Autocomplete Overlay
	AutocompleteBox     lipgloss.Style
	AutocompleteHeader  lipgloss.Style
	AutocompleteItemSel lipgloss.Style
	AutocompleteItem    lipgloss.Style
	AutocompleteDesc    lipgloss.Style
	AutocompleteBadge   lipgloss.Style
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
		amberBg = lipgloss.Color("#261A08")

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

		SidebarRule: lipgloss.NewStyle().
			Foreground(border),

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
			Padding(0, 1),

		CoderCalloutBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(violetMd).
			Background(violetBg).
			Foreground(textPrimary).
			Padding(0, 1),

		ReviewerCalloutBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(amberLt).
			Background(amberBg).
			Foreground(textPrimary).
			Padding(0, 1),

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

		EntryDivider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1A2535")),

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
			Foreground(mutedSoft).
			Italic(true),

		InputSeparator: lipgloss.NewStyle().
			Foreground(borderSoft),

		// ── Right Panel & Viewport ────────────────────────────────────
		RightCardContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Background(obsidian),

		ViewportContainer: lipgloss.NewStyle().
			Background(obsidian),

		EntryContent: lipgloss.NewStyle().
			Foreground(textPrimary),

		ActionResult: lipgloss.NewStyle().
			Foreground(textDim),

		ActionResultBar: lipgloss.NewStyle().
			Foreground(muted),

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

		// ── Autocomplete Overlay ──────────────────────────────────────
		AutocompleteBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(violetMd).
			Background(obsidianAlt).
			Padding(0, 1),

		AutocompleteHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt),

		AutocompleteItemSel: lipgloss.NewStyle().
			Bold(true).
			Foreground(cyanLt).
			Background(surfaceHigh),

		AutocompleteItem: lipgloss.NewStyle().
			Foreground(textDim),

		AutocompleteDesc: lipgloss.NewStyle().
			Foreground(mutedSoft).
			Italic(true),

		AutocompleteBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(violetLt),
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

	// Autocomplete state for live slash-command dropdown
	autocompleteActive bool
	autocompleteIndex  int
	autocompleteCmds   []commands.Command
	dismissedInput     string

	// lastCoderMessage is the most recent plain-text message Coder sent
	// before proposing an action. Captured so the auto-commit message
	// for an executed action can include a real intent excerpt (the
	// Coder's stated reasoning) rather than just "write_file" + path.
	// Cleared when a new proposed_action lands.
	lastCoderMessage string

	// lastProposedEntryID is the transcript ID of the most recent
	// proposed_action entry, captured for use in the auto-commit
	// message body (so the commit references the same entry the user
	// can see in the transcript).
	lastProposedEntryID int

	// gitDisabled is set to true when EnsureRepo or CheckUserConfigured
	// reports the working directory can't host commits. Once set, the
	// TUI skips the auto-commit step entirely so it doesn't spam the
	// transcript with the same "not configured" message on every
	// executed action.
	gitDisabled bool

	showJourney     bool
	journeyViewport viewport.Model
	journeyEntries  []journey.JourneyEntry

	spinner     spinner.Model
	viewport    viewport.Model
	sysViewport viewport.Model
	input       textinput.Model
	styles      Styles

	// currentMode holds the top-level orchestration mode (orchestrator | general | triad).
	currentMode loop.Mode

	// pendingClarify holds a non-nil Batch when the most recent
	// user submission triggered a clarification round (Phase 3,
	// docs/x.md §Phase 3). While non-nil, the next user message
	// is treated as a clarification REPLY (or a /proceed signal)
	// rather than a fresh task — and the loop does NOT fire a
	// Coder turn until either a proceed signal or a real answer
	// is received. Mirrors internal/loop.Loop.pendingClarify for
	// the TUI path so both share the same shared clarify step.
	pendingClarify *clarify.Batch

	// commands holds the slash command registry loaded at startup.
	// May be empty (no commands/ dir) but should never be nil.
	commands *commands.Registry

	// browser is the long-lived Playwright manager for browser_*
	// tool calls (docs/work2.md §4.2). nil means browser tools
	// are unavailable; approved browser_* calls will surface a
	// "browser not configured" error rather than crashing. Set
	// via SetBrowser after NewModel — the manager is owned by
	// the caller and not closed by the TUI.
	browser *browser.Manager
	searchAPIKey string

	memory   *memory.Manager
	learnSvc *learn.Service

	width  int
	height int
	ready  bool
}

// SetMemory attaches a memory.Manager and initializes learn.Service for the TUI.
func (m *Model) SetMemory(mem *memory.Manager) {
	m.memory = mem
	if mem != nil {
		s, _ := learn.NewService(mem)
		m.learnSvc = s
	}
}

// NewModel initializes a new Model for the Bubbletea program.
func NewModel(
	tr *transcript.Transcript,
	coder agent.AgentConfig,
	reviewer agent.AgentConfig,
	client loop.AgentClient,
	workDir string,
	commandTimeout time.Duration,
	cmdReg *commands.Registry,
) Model {
	styles := DefaultStyles()

	ti := textinput.New()
	ti.Placeholder = "Ask Triad to build a feature, edit code, or analyze tasks..."
	ti.Prompt = ""
	ti.SetWidth(40)
	ti.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(styles.SpinnerStyle),
	)

	if cmdReg == nil {
		cmdReg = &commands.Registry{}
	}

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
		commands:       cmdReg,
		currentMode:    loop.ModeOrchestrator,
	}

	m.RestoreSessionState()
	return m
}

// SetSearchAPIKey sets the Firecrawl API key used by web_search tool calls.
func (m *Model) SetSearchAPIKey(key string) {
	m.searchAPIKey = key
}

// SetBrowser attaches a browser.Manager to the TUI so that approved
// browser_* tool calls can be executed. Pass nil to detach (and
// disable browser tools for subsequent tool calls; the schema still
// appears in the model, but calls will surface a "browser not
// configured" error). The manager is owned by the caller — the TUI
// does not Close it on shutdown.
//
// The same manager should be passed to the loop via loop.SetBrowser
// so that the headless path (Phase 4 tests, future --headless flag)
// and the TUI path share the same browser state when running in
// the same process.
func (m *Model) SetBrowser(bm *browser.Manager) {
	m.browser = bm
}

// RestoreSessionState evaluates existing transcript entries and sets initial state, status, and Cmd.
func (m *Model) RestoreSessionState() {
	entries := m.transcript.Entries()

	for _, entry := range entries {
		if entry.Speaker == transcript.SpeakerSystem {
			if strings.HasPrefix(entry.Content, "Mode set to: General") {
				m.currentMode = loop.ModeGeneral
			} else if strings.HasPrefix(entry.Content, "Mode set to: Triad") {
				m.currentMode = loop.ModeTriad
			} else if strings.HasPrefix(entry.Content, "Mode set to: Orchestrator") {
				m.currentMode = loop.ModeOrchestrator
			}
		}
	}

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

// ---------------------------------------------------------------------------
// Slash command handling (docs/work2.md §1)
// ---------------------------------------------------------------------------

// expandSlashCommand inspects the human's input and, if it begins with "/",
// looks it up in the command registry.
//
// Return values:
//   - expanded:       the rendered command body (with {{args}} replaced),
//                     suitable to inject as a You message.
//   - cmdHandled:     true if the input was a recognised command — caller
//                     should use `expanded` instead of the raw input.
//   - systemHandled:  true if the command was a system-target command (e.g.
//                     /status) that the helper has already fully handled
//                     internally (wrote a System entry to the transcript).
//                     Caller should NOT inject anything else or trigger
//                     a Coder turn.
//   - errMsg:         non-empty if the input looked like a command but was
//                     not recognised; caller should surface it as a System
//                     error and not inject the raw input.
//
// If the input does not begin with "/", all four return values are zero
// values and the caller treats the input as a plain You message.
func (m *Model) expandSlashCommand(input string) (expanded string, cmdHandled bool, systemHandled bool, errMsg string) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false, false, ""
	}

	// Strip leading "/" and split into command name + rest as args.
	rest := strings.TrimSpace(trimmed[1:])
	if rest == "" {
		// Just "/", nothing to dispatch.
		return "", false, false, "Empty command. Type /<name> or just send a plain message."
	}

	// Split on the first run of whitespace so names with hyphens (e.g. /my-cmd)
	// stay intact, but "/plan add X" parses as name=plan, args="add X".
	var name, args string
	for i, r := range rest {
		if r == ' ' || r == '\t' {
			name = rest[:i]
			args = strings.TrimSpace(rest[i+1:])
			break
		}
	}
	if name == "" {
		name = rest
	}

	cmd, ok := m.commands.Get(name)
	if !ok {
		available := m.commands.Names()
		if len(available) == 0 {
			return "", false, false, fmt.Sprintf("Unknown command /%s. (No slash commands are registered.)", name)
		}
		return "", false, false, fmt.Sprintf("Unknown command /%s. Available: /%s", name, strings.Join(available, ", /"))
	}

	// System-target commands: the command body is addressed to the session
	// itself, not to either agent. Dispatch by name to a small handler
	// table — each handler writes its own System entry and returns.
	// /status is the canonical example; /undo reverts the last auto-commit.
	if cmd.Target == commands.TargetSystem {
		body, handlerErr := m.handleSystemCommand(name, args)
		if handlerErr != "" {
			return "", false, true, handlerErr
		}
		entry := transcript.Entry{
			Speaker:   transcript.SpeakerSystem,
			Type:      transcript.TypeMessage,
			Content:   body,
			Timestamp: time.Now(),
		}
		_ = m.transcript.Append(entry)
		m.statusMessage = "Command /" + name + " handled by session."
		return "", false, true, ""
	}

	// Coder / Reviewer target: render the template and inject as a You
	// message so the relevant agent sees it on the next turn.
	return cmd.Expand(args), true, false, ""
}

// handleSystemCommand dispatches a system-target slash command to its
// handler. Returns the body text that should be written to the transcript
// as a System entry, and a non-empty errMsg if the command name isn't
// recognised. The errMsg path is used by the caller to surface "unknown
// system command" as a separate error entry rather than a System note.
func (m *Model) handleSystemCommand(name string, args string) (body string, errMsg string) {
	switch name {
	case "status":
		return m.describeSession(), ""
	case "summary":
		return m.handleSummary(), ""
	case "undo":
		return m.handleUndo(), ""
	case "help":
		return m.handleHelp(), ""
	case "mode":
		return m.handleMode(args)
	case "trace":
		return m.handleTrace(), ""
	case "learn":
		return m.handleLearn(args)
	case "journey":
		return m.handleJourney(args)
	default:
		return "", fmt.Sprintf("System command /%s is not implemented (known: /status, /summary, /undo, /help, /mode, /trace, /learn, /journey).", name)
	}
}

// handleLearn surfaces extracted learnings and handles human promotion/dismissal commands.
func (m *Model) handleLearn(args string) (body string, errMsg string) {
	if m.memory == nil {
		mgr, err := memory.NewManager(m.workDir)
		if err != nil {
			return "", fmt.Sprintf("Failed to initialize memory manager: %v", err)
		}
		m.memory = mgr
	}
	if m.learnSvc == nil {
		svc, err := learn.NewService(m.memory)
		if err != nil {
			return "", fmt.Sprintf("Failed to initialize learn service: %v", err)
		}
		m.learnSvc = svc
	}

	// Always run auto extraction pass over transcript first
	_, _ = m.learnSvc.AutoExtractAndLog(m.transcript.Entries(), time.Now())

	args = strings.TrimSpace(args)
	if args == "" || args == "digest" || args == "list" {
		unreviewed := m.learnSvc.GetUnreviewedItems()
		return learn.FormatDigest(unreviewed), ""
	}

	if strings.HasPrefix(args, "promote-all") {
		topic := strings.TrimSpace(strings.TrimPrefix(args, "promote-all"))
		if topic == "" {
			return "", "Usage: /learn promote-all <topic>"
		}
		count, err := m.learnSvc.PromoteAll(topic)
		if err != nil {
			return "", fmt.Sprintf("Failed to promote items to %q: %v", topic, err)
		}
		return fmt.Sprintf("[Self-Learning] Promoted %d item(s) to topic %q.", count, topic), ""
	}

	if args == "dismiss-all" {
		count, err := m.learnSvc.DismissAll()
		if err != nil {
			return "", fmt.Sprintf("Failed to dismiss items: %v", err)
		}
		return fmt.Sprintf("[Self-Learning] Dismissed %d item(s). (Items remain intact in raw daily log.)", count), ""
	}

	if strings.HasPrefix(args, "promote ") {
		rest := strings.TrimSpace(strings.TrimPrefix(args, "promote"))
		parts := strings.Fields(rest)
		if len(parts) != 2 {
			return "", "Usage: /learn promote <id> <topic>"
		}
		id, topic := parts[0], parts[1]
		if err := m.learnSvc.Promote(id, topic); err != nil {
			return "", fmt.Sprintf("Failed to promote item %s: %v", id, err)
		}
		return fmt.Sprintf("[Self-Learning] Promoted item %s to memory/topics/%s.md.", id, topic), ""
	}

	if strings.HasPrefix(args, "dismiss ") {
		id := strings.TrimSpace(strings.TrimPrefix(args, "dismiss"))
		if id == "" {
			return "", "Usage: /learn dismiss <id>"
		}
		if err := m.learnSvc.Dismiss(id); err != nil {
			return "", fmt.Sprintf("Failed to dismiss item %s: %v", id, err)
		}
		return fmt.Sprintf("[Self-Learning] Dismissed item %s. (Item remains intact in raw daily log.)", id), ""
	}

	return "", fmt.Sprintf("Unknown subcommand %q for /learn. Available: /learn, /learn promote <id> <topic>, /learn dismiss <id>, /learn promote-all <topic>, /learn dismiss-all.", args)
}

// handleTrace renders the session trace log using the tracelog package.
func (m *Model) handleTrace() string {
	tracePath := tracelog.TracePathForSession(m.transcript.FilePath())
	entries, err := tracelog.LoadTrace(tracePath)
	if err != nil {
		return fmt.Sprintf("/trace failed: %v", err)
	}
	return tracelog.FormatTraceOutput(entries)
}

// handleMode views or sets the current top-level orchestration mode.
func (m *Model) handleMode(args string) (body string, errMsg string) {
	args = strings.TrimSpace(args)
	if args == "" {
		switch m.currentMode {
		case loop.ModeGeneral:
			return "Current mode: General Chat (single agent, no Reviewer loop).", ""
		case loop.ModeTriad:
			return "Current mode: Triad (full propose → review → execute loop).", ""
		default:
			return "Current mode: Orchestrator (default routing mode).", ""
		}
	}

	mode, err := loop.ParseMode(args)
	if err != nil {
		return "", fmt.Sprintf("Unknown mode %q. Valid modes: orchestrator, general, triad.", args)
	}

	m.currentMode = mode
	switch mode {
	case loop.ModeGeneral:
		return "Mode set to: General — Orchestrator will not route until you change this.", ""
	case loop.ModeTriad:
		return "Mode set to: Triad — Orchestrator will not route until you change this.", ""
	case loop.ModeOrchestrator:
		return "Mode set to: Orchestrator — Default routing enabled.", ""
	default:
		return fmt.Sprintf("Mode set to: %s", mode), ""
	}
}

// handleHelp formats a list of all registered slash commands and their descriptions.
func (m *Model) handleHelp() string {
	if m.commands == nil || m.commands.Count() == 0 {
		return "No slash commands registered."
	}
	var sb strings.Builder
	sb.WriteString("Available Slash Commands:\n")
	for _, cmd := range m.commands.List() {
		desc := cmd.Description
		if desc == "" {
			desc = "No description provided."
		}
		fmt.Fprintf(&sb, "  /%s — %s\n", cmd.Name, desc)
	}
	return sb.String()
}


// handleSummary renders a local, git-based report of changes made during the
// current session. It queries git log and git show --stat scoped to commits
// created by Triad during the current session, with no LLM calls required.
func (m *Model) handleSummary() string {
	entries := m.transcript.Entries()
	entryIDs := make(map[int]bool, len(entries))
	var currentTask string
	for _, e := range entries {
		entryIDs[e.ID] = true
		if e.Speaker == transcript.SpeakerYou && e.Type == transcript.TypeMessage && currentTask == "" {
			currentTask = e.Content
		}
	}

	summary, err := gitcommit.GetSessionSummary(m.workDir, entryIDs)
	if err != nil {
		return fmt.Sprintf("/summary failed: %v", err)
	}

	if summary.CommitCount == 0 {
		return "Nothing committed yet this session."
	}

	var sb strings.Builder
	sb.WriteString("Session Summary\n")
	task := strings.TrimSpace(currentTask)
	if task != "" {
		const maxTaskLen = 120
		if len(task) > maxTaskLen {
			task = task[:maxTaskLen] + "..."
		}
		fmt.Fprintf(&sb, "  Task: %s\n", task)
	}
	fmt.Fprintf(&sb, "  Commits made: %d\n", summary.CommitCount)
	if len(summary.FilesTouched) > 0 {
		fmt.Fprintf(&sb, "  Files touched (%d):\n", len(summary.FilesTouched))
		for _, f := range summary.FilesTouched {
			fmt.Fprintf(&sb, "    - %s\n", f)
		}
	} else {
		sb.WriteString("  Files touched: none\n")
	}
	fmt.Fprintf(&sb, "  Lines changed: +%d / -%d\n", summary.LinesAdded, summary.LinesRemoved)
	return strings.TrimRight(sb.String(), "\n")
}

// reloadJourneyEntries queries git log once and caches entries in memory.
func (m *Model) reloadJourneyEntries() {
	entries, _ := journey.GetJourneyEntries(m.workDir, nil)
	m.journeyEntries = entries
	if m.ready {
		vw := max(10, m.journeyViewport.Width())
		m.journeyViewport.SetContent(journey.RenderSidebarTimeline(m.journeyEntries, vw))
	}
}

// handleJourney renders or exports the session's commit journey timeline.
func (m *Model) handleJourney(args string) (body string, errMsg string) {
	m.reloadJourneyEntries()
	journeyEntries := m.journeyEntries

	args = strings.TrimSpace(args)
	if args == "--export" || args == "export" || strings.HasPrefix(args, "--export ") {
		filename := "journey_report.html"
		if strings.HasPrefix(args, "--export ") {
			filename = strings.TrimSpace(strings.TrimPrefix(args, "--export "))
		}
		outPath, err := journey.ExportHTML(m.workDir, filename, journeyEntries)
		if err != nil {
			return "", fmt.Sprintf("/journey export failed: %v", err)
		}
		m.showJourney = true
		return fmt.Sprintf("[Commit Journey] Exported visual HTML report with %d commit(s) to %s. Displaying timeline in left panel.", len(journeyEntries), outPath), ""
	}

	if args == "off" || args == "hide" || args == "close" {
		m.showJourney = false
		return "Commit Journey view closed. Showing Session Overview in left panel.", ""
	}

	if args == "on" || args == "show" {
		m.showJourney = true
	} else {
		m.showJourney = !m.showJourney
	}

	if !m.showJourney {
		return "Session Overview displayed in left panel.", ""
	}

	if len(journeyEntries) == 0 {
		return "Commit Journey view activated in left panel. No Triad commit history recorded yet for this session.", ""
	}

	return fmt.Sprintf("Commit Journey view activated in left panel (%d commit(s)). Enter /journey again to toggle overview.", len(journeyEntries)), ""
}


// handleUndo reverts the most recent [triad] auto-commit and returns a
// human-readable summary suitable for a System transcript entry. On
// failure (no commit to revert, merge conflict, etc.) the returned
// string is the error description — the caller still writes it as a
// System entry so the human sees it inline.
func (m *Model) handleUndo() string {
	res, err := gitcommit.RevertLast(m.workDir)
	if err != nil {
		// Nothing to undo is the common "ok" error path — surface it
		// as a friendly message rather than a raw git error. Covers
		// both "no [triad] commits in history" and "not a git repo".
		if strings.Contains(err.Error(), "nothing to undo") {
			return "Nothing to undo: no [triad] auto-commits in the working tree's history."
		}
		if res.Conflict {
			return fmt.Sprintf("/undo aborted: reverting %s produced a merge conflict. Resolve with `git add` + `git revert --continue`, or abort with `git revert --abort`. Details: %v", res.OriginalHash, err)
		}
		return fmt.Sprintf("/undo failed: %v", err)
	}
	if res.RevertCommitMsg != "" {
		return fmt.Sprintf("/undo: reverted %s. New commit: %s", res.OriginalHash, res.RevertCommitMsg)
	}
	return fmt.Sprintf("/undo: reverted %s.", res.OriginalHash)
}

// describeSession produces a short, human-readable summary of the current
// session state for the /status command. This intentionally computes
// everything from the transcript — no extra bookkeeping state — so it
// stays correct across resumes and crash recovery.
func (m *Model) describeSession() string {
	entries := m.transcript.Entries()

	var (
		proposedCount  int
		approvedCount  int
		objectCount    int
		humanMsgs      int
		currentTask    string
		lastActivityTS time.Time
	)
	for _, e := range entries {
		switch e.Type {
		case transcript.TypeProposedAction:
			proposedCount++
			lastActivityTS = e.Timestamp
		case transcript.TypeActionResult:
			approvedCount++
			lastActivityTS = e.Timestamp
		}
		switch e.Speaker {
		case transcript.SpeakerYou:
			humanMsgs++
			if e.Type == transcript.TypeMessage && currentTask == "" {
				currentTask = e.Content
			}
		case transcript.SpeakerReviewer:
			if loop.ParseReviewerDecision(e.Content) == loop.DecisionObject {
				objectCount++
			}
		}
	}

	stateLabel := "idle"
	if m.sessionState == loop.StateActive {
		stateLabel = "active"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Session status — %s\n", strings.ToUpper(stateLabel))
	fmt.Fprintf(&sb, "  Entries in transcript: %d\n", len(entries))
	fmt.Fprintf(&sb, "  Human messages: %d\n", humanMsgs)
	fmt.Fprintf(&sb, "  Proposed actions: %d (approved: %d, objected: %d)\n", proposedCount, approvedCount, objectCount)
	if currentTask != "" {
		// Truncate long task descriptions to keep /status compact.
		const maxTaskLen = 120
		task := currentTask
		if len(task) > maxTaskLen {
			task = task[:maxTaskLen] + "..."
		}
		fmt.Fprintf(&sb, "  Current task: %s\n", task)
	}
	if !lastActivityTS.IsZero() {
		fmt.Fprintf(&sb, "  Last activity: %s\n", lastActivityTS.Format("2006-01-02 15:04:05"))
	}
	return sb.String()
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
