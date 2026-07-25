package tui

import (
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
	TitleBar       lipgloss.Style
	StatusBar      lipgloss.Style
	YouHeader      lipgloss.Style
	CoderHeader    lipgloss.Style
	ReviewerHeader lipgloss.Style
	SystemHeader   lipgloss.Style
	EntryContent   lipgloss.Style
	ProposedAction lipgloss.Style
	ActionResult   lipgloss.Style
	ErrorContent   lipgloss.Style
}

// DefaultStyles returns a curated color palette for modern terminals.
func DefaultStyles() Styles {
	return Styles{
		TitleBar: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F8F8F2")).
			Background(lipgloss.Color("#6272A4")).
			Padding(0, 1),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1FA8C")).
			Italic(true),

		YouHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#50FA7B")), // Mint Green

		CoderHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#BD93F9")), // Soft Purple

		ReviewerHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFB86C")), // Bright Amber

		SystemHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#8BE9FD")), // Cyan

		EntryContent: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F8F8F2")),

		ProposedAction: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF79C6")).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1),

		ActionResult: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F8F8F2")),

		ErrorContent: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF5555")),
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
	ti.Prompt = "[You]: "
	ti.Focus()

	return Model{
		transcript:    tr,
		coder:         coder,
		reviewer:      reviewer,
		client:        client,
		workDir:       workDir,
		MaxRetries:    loop.DefaultMaxRetries,
		sessionState:  loop.StateIdle,
		statusMessage: "Idle — Enter your task below.",
		input:         ti,
		styles:        DefaultStyles(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}
