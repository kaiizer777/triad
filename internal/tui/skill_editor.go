// Package tui — /skill inline editor (Workflow 5 Phase 3.3 +
// 3.4). The editor opens in the right panel when the user
// types `/skill edit <name>` or after `/skill add <name>`.
// It uses charm.land/bubbles/v2/textarea so we get sane
// cursor movement, multi-line editing, and undo out of the
// box, with no extra dependency surface.
//
// Keybindings (work.md §3.3 / §6):
//   - Ctrl-S          : save the file and exit
//   - Esc             : cancel (discard changes) and exit
//   - Ctrl-C          : also cancel — matches the rest of
//                       the TUI's "abort current flow" key
//
// The editor intentionally does NOT support external
// commands (no shelling out to vim, etc.) — per the spec,
// editing happens in the in-TUI pane, not via $EDITOR. This
// keeps the experience identical whether the human is in a
// local terminal, SSH, or a piped CI session.
package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// skillTextarea is a thin wrapper around the bubbles
// textarea. We define a type alias so the editor state
// (skillEditorState.textarea) has a stable name across the
// codebase; if we ever swap the underlying component (e.g.
// to a different editor lib), only this file changes.
//
// We don't extend textarea.Model beyond what bubbles gives
// us — keybindings are handled at the parent update loop
// level (Model.Update), not inside the textarea itself, so
// "save" / "cancel" can read the editor's current value and
// act on the model.
type skillTextarea = textarea.Model

// newSkillTextarea constructs a fresh editor with sensible
// defaults for editing YAML-frontmatter skill files:
//   - No character limit (skill bodies are small).
//   - Word-wrap on so long lines don't run off the panel.
//   - Prompt blank — the user is editing the file itself,
//     not a chat input.
func newSkillTextarea() skillTextarea {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.ShowLineNumbers = true
	ta.SetWidth(80)
	ta.SetHeight(20)
	// Disable the standard "ctrl+s" / "ctrl+c" on the
	// textarea's own keymap — we handle those at the parent
	// Model.Update level so we can also write the file
	// and re-load the registry in the same step.
	ta.KeyMap.InsertNewline.SetEnabled(true)
	return ta
}

// skillEditorStyle is the lipgloss style used to draw the
// editor's frame. Defined at package scope so the same
// style is used in both Init/Update and View, instead of
// re-creating it on every frame.
var skillEditorStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("#8B5CF6")).
	Background(lipgloss.Color("#0D1526")).
	Padding(0, 1)

// skillEditorHeaderStyle is the title-bar above the editor
// frame. Shows the file path + keybinding hints.
var skillEditorHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#A78BFA")).
	Background(lipgloss.Color("#1A0A3A")).
	Padding(0, 1)

// renderSkillEditor produces the lipgloss string for the
// inline editor pane. Called by View when m.skillEditor is
// non-nil. Width is the panel width the editor should fit
// into (the right-panel inner width).
func renderSkillEditor(edit *skillEditorState, width int) string {
	if edit == nil {
		return ""
	}
	if width < 8 {
		width = 8
	}
	// Resize the textarea to fit the panel before drawing.
	// The -4 padding accounts for the rounded border (2
	// chars each side) plus a 2-char inner margin for
	// readability.
	taWidth := width - 4
	if taWidth < 4 {
		taWidth = 4
	}
	edit.textarea.SetWidth(taWidth)
	header := skillEditorHeaderStyle.Render(
		fmt.Sprintf(" Editing %s — Ctrl-S save · Esc cancel ", edit.path),
	)
	body := skillEditorStyle.Width(width - 2).Render(edit.textarea.View())
	return header + "\n" + body
}

// saveSkillEditor writes the textarea's current value to
// disk at the recorded path and returns the saved-bytes
// count. On error, returns the error and leaves the editor
// open so the user can retry or cancel. The TUI's Update
// loop is responsible for clearing m.skillEditor after a
// successful save.
func saveSkillEditor(edit *skillEditorState) (int, error) {
	if edit == nil {
		return 0, fmt.Errorf("nil editor")
	}
	body := edit.textarea.Value()
	// Trim trailing whitespace on the very last line so we
	// don't introduce spurious blank lines on every save.
	// Internal whitespace is preserved verbatim — we don't
	// reformat the user's content.
	body = strings.TrimRight(body, " \t\n") + "\n"
	return len(body), os.WriteFile(edit.path, []byte(body), 0o644)
}

// keyMatchesCtrlS is a small helper used by the editor's
// key handling. In Bubbletea v2, KeyMsg.String() returns
// "ctrl+s" for the Ctrl-S chord. We use the String form
// rather than a rune/Code compare because v2's KeyMsg
// is an interface that wraps an internal struct.
func keyMatchesCtrlS(msg tea.KeyMsg) bool {
	return msg.String() == "ctrl+s"
}

// keyMatchesCancel is "Esc" OR "Ctrl-C". We accept both
// because Esc is the natural cancel gesture but Ctrl-C is
// what muscle memory reaches for in long-running TUIs.
func keyMatchesCancel(msg tea.KeyMsg) bool {
	s := msg.String()
	return s == "esc" || s == "ctrl+c"
}
