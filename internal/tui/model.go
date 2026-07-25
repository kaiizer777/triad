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
	TitleBarLeft      lipgloss.Style
	TitleBarCenter    lipgloss.Style
	TitleBarRight     lipgloss.Style
	SidebarContainer  lipgloss.Style
	SidebarHeader     lipgloss.Style
	SidebarLabel      lipgloss.Style
	SidebarValue      lipgloss.Style
	SidebarBadgeIdle  lipgloss.Style
	SidebarBadgeActive lipgloss.Style
	YouPill           lipgloss.Style
	CoderPill         lipgloss.Style
	ReviewerPill      lipgloss.Style
	SystemPill        lipgloss.Style
	Timestamp         lipgloss.Style
	ToolCallBox       lipgloss.Style
	ToolCallHeader    lipgloss.Style
	ToolCallKey       lipgloss.Style
	ToolCallVal       lipgloss.Style
	ToolCallFunc      lipgloss.Style
	InputContainer    lipgloss.Style
	InputPill         lipgloss.Style
	ViewportContainer lipgloss.Style
	EntryContent      lipgloss.Style
	ActionResult      lipgloss.Style
	ErrorContent      lipgloss.Style
	StatusBar         lipgloss.Style
}

// DefaultStyles returns a curated color palette for modern terminals.
func DefaultStyles() Styles {
	return Styles{
		TitleBarLeft: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#BD93F9")).
			Padding(0, 1),

		TitleBarCenter: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8BE9FD")).
			Background(lipgloss.Color("#282A36")).
			Padding(0, 1),

		TitleBarRight: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Background(lipgloss.Color("#1E1E2E")).
			Padding(0, 1),

		SidebarContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#44475A")).
			Padding(0, 1),

		SidebarHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#BD93F9")),

		SidebarLabel: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#6272A4")),

		SidebarValue: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F8F8F2")),

		SidebarBadgeIdle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#50FA7B")).
			Padding(0, 1),

		SidebarBadgeActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#FFB86C")).
			Padding(0, 1),

		YouPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#50FA7B")).
			Padding(0, 1),

		CoderPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#BD93F9")).
			Padding(0, 1),

		ReviewerPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#FFB86C")).
			Padding(0, 1),

		SystemPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#8BE9FD")).
			Padding(0, 1),

		Timestamp: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Italic(true),

		ToolCallBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#BD93F9")).
			Background(lipgloss.Color("#21222C")).
			Padding(0, 1),

		ToolCallHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#BD93F9")),

		ToolCallKey: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8BE9FD")),

		ToolCallVal: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1FA8C")),

		ToolCallFunc: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF79C6")),

		InputContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#50FA7B")).
			Padding(0, 1),

		InputPill: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(lipgloss.Color("#50FA7B")).
			Padding(0, 1),

		ViewportContainer: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#44475A")),

		EntryContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F8F8F2")),

		ActionResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F8F8F2")),

		ErrorContent: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5555")),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1FA8C")).
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
	ti := textinput.New()
	ti.Placeholder = "Type a task or interjection and press Enter..."
	ti.Prompt = "> "
	ti.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))),
	)

	return Model{
		transcript:    tr,
		coder:         coder,
		reviewer:      reviewer,
		client:        client,
		workDir:       workDir,
		MaxRetries:    loop.DefaultMaxRetries,
		sessionState:  loop.StateIdle,
		statusMessage: "Idle — Enter your task below.",
		spinner:       sp,
		input:         ti,
		styles:        DefaultStyles(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
	)
}
