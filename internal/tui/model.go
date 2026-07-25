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
	MdBold        lipgloss.Style
	MdInlineCode  lipgloss.Style
	MdCodeBlock   lipgloss.Style
	MdBullet      lipgloss.Style

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

// DefaultStyles returns a curated Catppuccin Mocha / CommandCode palette for production terminals.
func DefaultStyles() Styles {
	return Styles{
		// Header Strip
		TitleBrand: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")). // Deep Purple
			Padding(0, 1),

		TitleVersion: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E293B")).
			Background(lipgloss.Color("#38BDF8")). // Sky Blue
			Padding(0, 1),

		TitleCenter: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#0F172A")). // Slate Dark
			Padding(0, 1),

		TitleKeycapKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F8FAFC")).
			Background(lipgloss.Color("#334155")). // Slate 700
			Padding(0, 1),

		TitleKeycapLabel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8")).
			Background(lipgloss.Color("#1E293B")). // Slate 800
			Padding(0, 1),

		// Sidebar Panel
		SidebarContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#334155")).
			Background(lipgloss.Color("#0F172A")).
			Padding(0, 1),

		SidebarHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#A855F7")), // Violet

		SidebarSubHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")),

		SidebarLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#94A3B8")),

		SidebarValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1F5F9")),

		SidebarBadgeIdle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#22C55E")). // Green
			Padding(0, 1),

		SidebarBadgeActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#F59E0B")). // Amber
			Padding(0, 1),

		SidebarBadgeThink: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#8B5CF6")). // Purple
			Padding(0, 1),

		SidebarMeterFill: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E")),

		SidebarMeterEmpty: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#334155")),

		// Speaker Name Pills
		YouPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#2563EB")). // Royal Blue
			Padding(0, 1),

		CoderPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#9333EA")). // Deep Purple
			Padding(0, 1),

		ReviewerPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#F59E0B")). // Amber
			Padding(0, 1),

		SystemPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#06B6D4")). // Cyan
			Padding(0, 1),

		Timestamp: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")).
			Italic(true),

		// Message Feed Callouts & Accents
		UserCalloutBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3B82F6")).
			Background(lipgloss.Color("#1E293B")).
			Padding(0, 1),

		YouMessageBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3B82F6")).
			Bold(true),

		CoderMessageBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A855F7")).
			Bold(true),

		ReviewerMessageBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true),

		ApprovedBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#22C55E")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#22C55E")).
			Background(lipgloss.Color("#052E16")).
			Padding(0, 1),

		ObjectionBadge: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EF4444")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#EF4444")).
			Background(lipgloss.Color("#450A0A")).
			Padding(0, 1),

		// Markdown Formatting
		MdBold: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F8FAFC")),

		MdInlineCode: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8")).
			Background(lipgloss.Color("#1E293B")).
			Padding(0, 1),

		MdCodeBlock: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#475569")).
			Background(lipgloss.Color("#0F172A")).
			Padding(0, 1),

		MdBullet: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A855F7")),

		// Tool Action Card Panel
		ToolCallBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#8B5CF6")).
			Background(lipgloss.Color("#090D16")).
			Padding(0, 1),

		ToolCallHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#C084FC")),

		ToolCallFunc: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#38BDF8")),

		ToolCallKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#818CF8")),

		ToolCallVal: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ADE80")),

		ToolCallNum: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F472B6")),

		// Input Box & Prompts
		InputContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3B82F6")).
			Background(lipgloss.Color("#0F172A")).
			Padding(0, 1),

		InputPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#2563EB")).
			Padding(0, 1),

		InputPrompt: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#38BDF8")),

		InputHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")).
			Italic(true),

		// Pipeline Dock Steps
		PipelineStepActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1),

		PipelineStepPending: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8")).
			Background(lipgloss.Color("#1E293B")).
			Padding(0, 1),

		PipelineStepDone: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#22C55E")).
			Padding(0, 1),

		PipelineArrow: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")),

		// Transcript Viewport
		ViewportContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#334155")).
			Background(lipgloss.Color("#0B0F19")),

		EntryContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")),

		ActionResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8")),

		ErrorContent: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EF4444")),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FDE047")).
			Background(lipgloss.Color("#0F172A")).
			Padding(0, 1),

		SpinnerStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A855F7")).
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
