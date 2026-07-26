// Package skills — /skill command handlers (Workflow 5 Phase 3).
//
// The /skill command is dispatched as a single slash command with
// subcommands (list / view / add / delete / force), matching the
// /mode orchestrator|general|triad pattern in the TUI. Each
// Handler below is a small, side-effecting function that mutates
// the registry / loaded set / file system and returns a body
// string suitable to be appended as a System transcript entry.
//
// The handlers own NO terminal state and emit NO ANSI — they
// return plain text and let the TUI's view layer style it. This
// keeps the package testable in isolation (every handler is
// exercised via unit tests with a temp dir + scratch registry).
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HandlerResult is the small data structure every /skill
// subcommand returns. Body is the human-readable text the TUI
// writes as a System entry; Reload is true if the registry /
// loaded set state changed in a way the TUI should reflect
// (e.g. /skill add created a new file, /skill delete removed
// one). The TUI uses Reload to decide whether to re-render
// list-style surfaces.
//
// PendingAction is the optional follow-up state the TUI
// should run AFTER writing the System entry. Most subcommands
// leave this nil (everything happens synchronously). Delete
// uses it to defer the actual file removal until the user
// confirms; the TUI surfaces a "Type yes to confirm" prompt
// and re-invokes HandleSubcommand on confirmation.
type HandlerResult struct {
	Body          string
	Reload        bool
	PendingAction *PendingAction
}

// PendingAction is the deferred work a subcommand wants the
// TUI to do after writing the System entry. Currently only
// Delete is supported (forces a confirmation gate).
type PendingAction struct {
	// Kind identifies which follow-up to run.
	Kind PendingActionKind
	// Name is the skill name the action targets.
	Name string
}

// PendingActionKind is the small enum over supported
// follow-up kinds. Mirrors tui.skillActionKind but lives
// in the skills package to avoid an import cycle.
type PendingActionKind int

const (
	// PendingActionNone is the zero value (no follow-up).
	PendingActionNone PendingActionKind = iota
	// PendingActionDelete asks the TUI to confirm the
	// delete before running it.
	PendingActionDelete
)

// HandleSubcommand dispatches "/skill <subcmd> [args]" to the
// matching Handler below. The leading "/skill" is already
// stripped — `subcmd` is the first whitespace-delimited token
// after "/skill", and `args` is the remainder (also stripped).
//
// An empty subcmd returns a usage summary (Body set, Reload
// false). An unknown subcmd returns an error message in Body
// listing known subcommands. The TUI writes Body as a System
// entry either way — there's no separate error-channel path,
// because /skill is a session-level command and the user already
// initiated it.
//
// reg may be nil (no skills/ dir or load failed) — in that case
// subcommands that need a registry (list, view, force) return a
// friendly "no skills configured" message. Subcommands that
// always work regardless of registry state (add) still operate
// on the filesystem via the workDir fallback.
func HandleSubcommand(subcmd, args string, reg *Registry, loaded *LoadedSet, workDir string) HandlerResult {
	subcmd = strings.ToLower(strings.TrimSpace(subcmd))
	args = strings.TrimSpace(args)

	switch subcmd {
	case "":
		return HandlerResult{Body: usage()}
	case "list", "ls":
		return handleList(reg, loaded)
	case "view", "show":
		return handleView(args, reg)
	case "add", "new":
		return handleAdd(args, reg, workDir)
	case "delete", "rm", "remove":
		// /skill delete requires explicit confirmation. The TUI
		// prompt is layered on top: the TUI calls this only
		// after the user types "yes" to a confirmation prompt,
		// so passing `confirm` here is the runtime contract
		// that this isn't an accidental invocation.
		if args == "" {
			return HandlerResult{Body: "Usage: /skill delete <name>  (requires a second confirmation in the TUI)"}
		}
		// Re-parse: handleDelete expects "<name> --confirm" or
		// "<name>" — we accept either, but require the
		// confirmation prompt to have already been shown by
		// the TUI. We treat any args value as the name and
		// trust the TUI's confirmation flow did the gate.
		return handleDelete(args, reg, workDir)
	case "force":
		if args == "" {
			return HandlerResult{Body: "Usage: /skill force <name>  — pins the section's Main body for the rest of this session."}
		}
		return handleForce(args, reg, loaded, workDir)
	default:
		return HandlerResult{Body: fmt.Sprintf("Unknown /skill subcommand %q. %s", subcmd, usage())}
	}
}

// usage returns the short /skill help block. Used as the
// "no subcommand given" and "unknown subcommand" response.
func usage() string {
	return strings.TrimSpace(`Available /skill subcommands:
  /skill list                       — show all skills with name, description, tier sizes, last-modified
  /skill view <name>                — show a skill's full Main and Mini content
  /skill add <name>                 — scaffold a new skill (empty frontmatter + placeholder Main/Mini), then drop into edit mode
  /skill delete <name>              — remove a skill file (with confirmation)
  /skill force <name>               — pin a section's Main body to be injected for the rest of this session
  /skill                            — show this help`)
}

// handleList renders the table-style summary of every loaded
// skill. Output format is intentionally plain (no ANSI) so the
// TUI's lipgloss styling can wrap it in the standard
// System callout box.
//
// Forced sections are annotated with a "(forced)" suffix so the
// human can see at a glance which skills they've pinned for
// the session.
func handleList(reg *Registry, loaded *LoadedSet) HandlerResult {
	if reg == nil || reg.Count() == 0 {
		return HandlerResult{Body: "No skills configured. Drop a `*.md` file in the `skills/` directory and (re)start a session, or use `/skill add <name>` to scaffold one."}
	}

	names := reg.Names()
	var b strings.Builder
	b.WriteString("Loaded skills (")
	fmt.Fprintf(&b, "%d", len(names))
	b.WriteString("):\n")

	for _, name := range names {
		sk, ok := reg.Get(name)
		if !ok {
			continue
		}
		mod := "?"
		if fi, err := os.Stat(sk.SourcePath); err == nil {
			mod = fi.ModTime().Format("2006-01-02 15:04")
		}
		desc := sk.Description
		const maxDesc = 80
		if len(desc) > maxDesc {
			desc = desc[:maxDesc-3] + "..."
		}
		miniTag := ""
		if sk.MiniBody == "" {
			miniTag = " (no mini)"
		} else if sk.MiniRef != "" {
			miniTag = fmt.Sprintf(" (mini: %s)", sk.MiniRef)
		}
		forcedTag := ""
		if loaded != nil && loaded.IsForced(sk.Section) {
			forcedTag = " [forced]"
		}
		// Pad the name column to a fixed width for readability.
		nameCol := fmt.Sprintf("%-20s", sk.Name)
		fmt.Fprintf(&b, "  - %s  %s  | main ≤%d tok%s | modified %s%s\n",
			nameCol, desc, sk.TokenBudgetMain, miniTag, mod, forcedTag)
	}
	return HandlerResult{Body: strings.TrimRight(b.String(), "\n")}
}

// handleView renders a single skill's full Main + Mini bodies
// in a clearly-delimited block. Output is plain text — the TUI
// styles it via the System callout.
//
// Missing skills return an error message in Body, not a panic.
func handleView(args string, reg *Registry) HandlerResult {
	name := strings.TrimSpace(args)
	if name == "" {
		return HandlerResult{Body: "Usage: /skill view <name>"}
	}
	if reg == nil || reg.Count() == 0 {
		return HandlerResult{Body: "No skills configured."}
	}
	sk, ok := reg.Get(name)
	if !ok {
		return HandlerResult{Body: fmt.Sprintf("Unknown skill %q. Use /skill list to see available skills.", name)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill: %s  (section: %s)\n", sk.Name, sk.Section)
	fmt.Fprintf(&b, "  Description: %s\n", sk.Description)
	fmt.Fprintf(&b, "  Source:      %s\n", sk.SourcePath)
	if sk.MiniRef != "" {
		fmt.Fprintf(&b, "  Mini file:   %s\n", sk.MiniRef)
	}
	fmt.Fprintf(&b, "  Token budgets: main ≤%d, mini ≤%d\n", sk.TokenBudgetMain, sk.TokenBudgetMini)
	b.WriteString("\n--- MAIN BODY ---\n")
	b.WriteString(sk.MainBody)
	b.WriteString("\n")
	if sk.MiniBody != "" {
		b.WriteString("\n--- MINI BODY ---\n")
		b.WriteString(sk.MiniBody)
		b.WriteString("\n")
	}
	return HandlerResult{Body: strings.TrimRight(b.String(), "\n")}
}

// skillScaffold is the canonical placeholder body for a freshly
// added skill. Authoring is intentionally low-friction — the
// user (or their coding agent) fills in the placeholder lines
// later. We provide Main + Mini so /skill view works
// immediately after /skill add without further authoring.
//
// The template uses simple, declarative placeholders rather
// than being a complete example skill, so the user has to
// think about what to put in it. (Cf. work.md §9 — Triad does
// not auto-generate skill content.)
const skillScaffold = `---
name: __NAME__
section: __NAME__
description: "Short description of when this skill should fire."
tier: main
mini_ref: __NAME__-mini.md
token_budget_main: 0
token_budget_mini: 0
---

# Main body

> Replace this placeholder with the full domain knowledge
> (conventions, patterns, gotchas) that Coder should see the
> first time this section is selected. Aim for 5–8k tokens.
`

const skillScaffoldMini = `---
name: __NAME__
section: __NAME__
description: "Short description of when this skill should fire."
tier: mini
---

# Mini body

> Replace this with the condensed pointer version (2–4k
> tokens) that Coder sees on every subsequent touch of this
> section within a session.
`

// handleAdd scaffolds a new skill file (Main + Mini) on disk
// and returns success text. The TUI takes the Reload=true
// signal and re-loads the registry so the new skill is
// immediately visible to Stage 1.
//
// Reuses Loader's name-validation rules: name must be a valid
// filename stem and must not already exist. We do NOT
// re-validate here — we just check the filesystem to keep the
// "scaffold" semantics obvious to the user. The TUI's edit
// flow re-loads the registry, which will run Loader's full
// validation on the just-written file and surface any
// authoring mistakes via a follow-up System entry.
//
// If the skills/ directory does not exist yet, we create it
// (mkdir -p) so the user can start from a fresh project.
func handleAdd(args string, reg *Registry, workDir string) HandlerResult {
	name := strings.ToLower(strings.TrimSpace(args))
	if name == "" {
		return HandlerResult{Body: "Usage: /skill add <name>  — <name> becomes both the filename and the section label."}
	}
	if !validSkillName(name) {
		return HandlerResult{Body: fmt.Sprintf("Invalid skill name %q. Use lowercase letters, digits, and hyphens; must not start or end with a hyphen.", name)}
	}
	dir := skillsDir(reg, workDir)
	if dir == "" {
		return HandlerResult{Body: "Could not determine skills directory. Pass --workdir or run from a project root containing a `skills/` folder."}
	}
	mainPath := filepath.Join(dir, name+".md")
	miniPath := filepath.Join(dir, name+"-mini.md")
	if _, err := os.Stat(mainPath); err == nil {
		return HandlerResult{Body: fmt.Sprintf("Skill %q already exists at %s. Use /skill view %s to inspect, or /skill delete %s first.", name, mainPath, name, name)}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return HandlerResult{Body: fmt.Sprintf("Could not create skills directory %q: %v", dir, err)}
	}
	main := strings.ReplaceAll(skillScaffold, "__NAME__", name)
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		return HandlerResult{Body: fmt.Sprintf("Could not write main file %q: %v", mainPath, err)}
	}
	mini := strings.ReplaceAll(skillScaffoldMini, "__NAME__", name)
	if err := os.WriteFile(miniPath, []byte(mini), 0o644); err != nil {
		// Best-effort cleanup so we don't leave a half-scaffolded
		// pair behind.
		_ = os.Remove(mainPath)
		return HandlerResult{Body: fmt.Sprintf("Could not write mini file %q: %v", miniPath, err)}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Scaffolded new skill %q at:\n", name)
	fmt.Fprintf(&b, "  main: %s\n", mainPath)
	fmt.Fprintf(&b, "  mini: %s\n", miniPath)
	b.WriteString("Use /skill edit " + name + " to fill in the body. The new skill will appear in the next Stage 1 section scan after the TUI reloads the registry.")
	return HandlerResult{Body: b.String(), Reload: true}
}

// handleDelete verifies the skill exists and returns a
// PendingAction for the TUI to confirm. The actual file
// removal happens in handleDeleteConfirmed (called only
// after the user types "yes" to the TUI's confirmation
// prompt). This two-step shape is what makes the
// destructive command safe — a single mis-typed "/skill
// delete foo" does NOT remove files.
//
// We still validate the skill exists here so the
// confirmation prompt doesn't bother the user with a
// follow-up "skill not found" error.
func handleDelete(args string, reg *Registry, workDir string) HandlerResult {
	name := strings.ToLower(strings.TrimSpace(args))
	// Strip any trailing flags the TUI might have appended
	// (e.g. a confirmation token). The TUI never sends those
	// today, but this is defensive.
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return HandlerResult{Body: "Usage: /skill delete <name>"}
	}
	if reg == nil || reg.Count() == 0 {
		return HandlerResult{Body: "No skills configured."}
	}
	sk, ok := reg.Get(name)
	if !ok {
		return HandlerResult{Body: fmt.Sprintf("Unknown skill %q. Use /skill list to see available skills.", name)}
	}
	// Existence is confirmed; defer the actual removal to
	// the TUI's confirmation gate.
	return HandlerResult{
		Body: fmt.Sprintf("Pending delete of skill %q (main: %s).", name, sk.SourcePath),
		PendingAction: &PendingAction{
			Kind: PendingActionDelete,
			Name: name,
		},
	}
}

// handleDeleteConfirmed performs the actual file removal
// for a previously-queued delete. Called by the TUI only
// after the user types "yes" to the confirmation prompt.
// The function is exported-style (lowercase but reachable
// via HandleSubcommand's "delete --confirm" branch in
// tests; the TUI itself uses ExecutePending).
func handleDeleteConfirmed(name string, reg *Registry, workDir string) HandlerResult {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return HandlerResult{Body: "Usage: /skill delete <name>"}
	}
	if reg == nil || reg.Count() == 0 {
		return HandlerResult{Body: "No skills configured."}
	}
	sk, ok := reg.Get(name)
	if !ok {
		return HandlerResult{Body: fmt.Sprintf("Unknown skill %q.", name)}
	}
	dir := filepath.Dir(sk.SourcePath)
	miniPath := ""
	if sk.MiniRef != "" {
		miniPath = filepath.Join(dir, sk.MiniRef)
	} else {
		miniPath = filepath.Join(dir, name+"-mini.md")
	}

	removed := []string{}
	if err := os.Remove(sk.SourcePath); err != nil && !os.IsNotExist(err) {
		return HandlerResult{Body: fmt.Sprintf("Could not remove main file %q: %v", sk.SourcePath, err)}
	}
	removed = append(removed, sk.SourcePath)
	if miniPath != "" && miniPath != sk.SourcePath {
		if err := os.Remove(miniPath); err != nil && !os.IsNotExist(err) {
			return HandlerResult{Body: fmt.Sprintf("Removed main file %q but could not remove mini file %q: %v", sk.SourcePath, miniPath, err), Reload: true}
		}
		removed = append(removed, miniPath)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Deleted skill %q. Removed files:\n", name)
	for _, p := range removed {
		fmt.Fprintf(&b, "  - %s\n", p)
	}
	b.WriteString("Recover with `git restore <path>` if this was accidental.")
	return HandlerResult{Body: strings.TrimRight(b.String(), "\n"), Reload: true}
}

// handleForce marks a section as forced for the rest of the
// session. The next time the funnel builds the prompt, the
// section's Main body fires (regardless of whether Coder
// auto-selected it); subsequent turns emit Mini.
//
// Unknown sections return an error message in Body. We
// re-load the registry's section list to make the error
// message specific (no point listing all sections if the
// user has 50 of them — we just say "unknown").
func handleForce(args string, reg *Registry, loaded *LoadedSet, workDir string) HandlerResult {
	name := strings.ToLower(strings.TrimSpace(args))
	if name == "" {
		return HandlerResult{Body: "Usage: /skill force <name>  — pins the section's Main body for the rest of this session."}
	}
	if reg == nil {
		return HandlerResult{Body: "No skills configured."}
	}
	// Look up by both name and section: /skill force frontend
	// should work the same as /skill force frontend (the
	// section label). GetBySection handles the section path;
	// Get handles the name path. Both are case-insensitive.
	sk, ok := reg.GetBySection(name)
	if !ok {
		sk, ok = reg.Get(name)
	}
	if !ok {
		return HandlerResult{Body: fmt.Sprintf("Unknown skill/section %q. Use /skill list to see available skills.", name)}
	}
	if loaded == nil {
		return HandlerResult{Body: "Loaded-skill tracking is not initialized for this session."}
	}
	loaded.Force(sk.Section)
	return HandlerResult{Body: fmt.Sprintf("Forced skill %q (section: %s) for this session. Its Main body will fire on the next Coder turn, then Mini on every turn after. Use /skill force %s again to unforce.", sk.Name, sk.Section, sk.Name)}
}

// ExecutePending runs a previously-queued PendingAction and
// returns the resulting HandlerResult. The TUI calls this
// after the user confirms a destructive command. The
// returned Body is what gets written to the transcript as
// the follow-up System entry.
//
// Currently only PendingActionDelete is supported. Unknown
// kinds return a friendly error Body so a future version
// of the TUI doesn't silently drop a queued action.
func ExecutePending(action *PendingAction, reg *Registry, workDir string) HandlerResult {
	if action == nil {
		return HandlerResult{Body: "No pending action to execute."}
	}
	switch action.Kind {
	case PendingActionDelete:
		return handleDeleteConfirmed(action.Name, reg, workDir)
	default:
		return HandlerResult{Body: fmt.Sprintf("Unknown pending action kind %d.", int(action.Kind))}
	}
}

// skillsDir returns the absolute skills directory the registry
// was loaded from, falling back to workDir/skills if the
// registry has no Dir (e.g. constructed manually in tests).
// Returns "" if neither is usable.
func skillsDir(reg *Registry, workDir string) string {
	if reg != nil && reg.Dir != "" {
		return reg.Dir
	}
	if workDir == "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(workDir, "skills"))
	if err != nil {
		return ""
	}
	return abs
}

// validSkillName enforces the same naming rules Loader does
// (filename stem matches frontmatter name), but in a
// user-facing way. Lowercase letters, digits, hyphens; must
// not start or end with a hyphen; must not contain whitespace
// or path separators.
func validSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	// Reject the "looks like a mini" case so /skill add foo-mini
	// fails fast — otherwise the user would get a Main file
	// whose mini_ref was implicitly a sibling, which is
	// surprising.
	if strings.HasSuffix(name, "-mini") {
		return false
	}
	return true
}

// now is a tiny indirection so tests can stub out the
// timestamp in scaffolded files (currently unused — the
// scaffold is deterministic — but kept here so a future
// "include creation time" tweak doesn't have to reach into
// callers).
var now = func() time.Time { return time.Now() }
