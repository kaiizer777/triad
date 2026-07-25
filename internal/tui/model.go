package tui

import (
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

	ViewportContainer lipgloss.Style
	EntryContent      lipgloss.Style
	ActionResult      lipgloss.Style
	ErrorContent      lipgloss.Style
	StatusBar         lipgloss.Style
	SpinnerStyle      lipgloss.Style
}

// DefaultStyles returns a premium obsidian dark palette with glowing accents,
// inspired by Claude Code / Cursor CLI / Warp Terminal aesthetics.
func DefaultStyles() Styles {
	var (
		obsidian    = lipgloss.Color("#0A0E17")
		obsidianAlt = lipgloss.Color("#0F172A")
		surface     = lipgloss.Color("#131A2A")
		border      = lipgloss.Color("#1E293B")
		borderSoft  = lipgloss.Color("#293548")
		muted       = lipgloss.Color("#64748B")
		mutedSoft   = lipgloss.Color("#475569")
		textPrimary = lipgloss.Color("#F1F5F9")
		textDim     = lipgloss.Color("#94A3B8")

		violet    = lipgloss.Color("#8B5CF6")
		violetLt  = lipgloss.Color("#C4B5FD")
		cyan      = lipgloss.Color("#06B6D4")
		cyanLt    = lipgloss.Color("#67E8F9")
		emerald   = lipgloss.Color("#10B981")
		emeraldLt = lipgloss.Color("#6EE7B7")
		amber     = lipgloss.Color("#F59E0B")
		amberLt   = lipgloss.Color("#FCD34D")
		blue      = lipgloss.Color("#3B82F6")
		blueLt    = lipgloss.Color("#93C5FD")
		red       = lipgloss.Color("#F43F5E")
		redLt     = lipgloss.Color("#FDA4AF")
		pink      = lipgloss.Color("#EC4899")
	)

	return Styles{
		// ── Header Strip ──────────────────────────────────────────────
		TitleBrand: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(violet).
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
			Background(surface).
			Padding(0, 1),

		TitleKeycapLabel: lipgloss.NewStyle().
			Foreground(muted).
			Background(obsidianAlt).
			Padding(0, 1),

		// ── Sidebar Panel ─────────────────────────────────────────────
		SidebarContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(violet).
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
			Background(violet).
			Padding(0, 1),

		SidebarMeterFill: lipgloss.NewStyle().
			Foreground(emerald),

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
			Background(violet).
			Padding(0, 1),

		ReviewerPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(amber).
			Padding(0, 1),

		SystemPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(cyan).
			Padding(0, 1),

		Timestamp: lipgloss.NewStyle().
			Foreground(mutedSoft).
			Italic(true),

		// ── Message Feed Callouts & Accents ──────────────────────────
		UserCalloutBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(blue).
			Background(surface).
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
			Foreground(lipgloss.Color("#FFFFFF")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(emerald).
			Background(lipgloss.Color("#022C22")).
			Padding(0, 1),

		ObjectionBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(red).
			Background(lipgloss.Color("#450A0A")).
			Padding(0, 1),

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
			BorderForeground(violet).
			Background(lipgloss.Color("#0B0F1C")).
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
			BorderForeground(blue).
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
			Background(violet).
			Padding(0, 1),

		PipelineStepPending: lipgloss.NewStyle().
			Foreground(muted).
			Background(surface).
			Padding(0, 1),

		PipelineStepDone: lipgloss.NewStyle().
			Bold(true).
			Foreground(obsidian).
			Background(emerald).
			Padding(0, 1),

		PipelineArrow: lipgloss.NewStyle().
			Foreground(mutedSoft),

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
			Foreground(amberLt).
			Background(obsidianAlt).
			Padding(0, 1),

		SpinnerStyle: lipgloss.NewStyle().
			Foreground(violetLt).
			Bold(true),
	}
}

// Model represents the top-level Bubbletea application state.
type Model struct {
	transcript *transcript.Transcript
	coder      agent.AgentConfig
	reviewer   agent.AgentConfig
	client     loop.AgentClient
	workDir    string

	MaxRetries     int
	sessionState   loop.SessionState
	activeToolCall *agent.ToolCall
	retryCount     int
	plainTextTurns int // consecutive Coder plain-text turns with no tool call
	statusMessage  string

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

	return Model{
		transcript:    tr,
		coder:         coder,
		reviewer:      reviewer,
		client:        client,
		workDir:       workDir,
		MaxRetries:    loop.DefaultMaxRetries,
		sessionState:  loop.StateIdle,
		statusMessage: "Ready — Type your prompt below and press Enter.",
		spinner:       sp,
		input:         ti,
		styles:        styles,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
	)
}
