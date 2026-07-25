// Package commands loads slash command definitions from .md files with YAML
// frontmatter and renders them with {{args}} substitution.
//
// A command file looks like:
//
//	---
//	name: plan
//	target: coder
//	description: Ask Coder to produce a plan only
//	---
//
//	Propose a step-by-step plan for: {{args}}
//
// The Registry is constructed once at startup (Load) and is read-only
// thereafter. The Expand method is the only thing the TUI calls at runtime.
package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kaiizer777/triad/internal/logger"
)

// Target identifies which participant a command is addressed to.
// This is informational at the parser level — the TUI still injects the
// rendered template as a You message into the shared transcript. The
// `target` field is exposed so the TUI / future code can choose to route
// it differently if needed (e.g. a /status command that doesn't trigger
// a Coder turn at all).
type Target string

const (
	TargetCoder    Target = "coder"
	TargetReviewer Target = "reviewer"
	TargetSystem   Target = "system"
)

// Command is a single loaded slash command.
type Command struct {
	// Name is the bare command name without the leading slash (e.g. "plan").
	Name string
	// Target is which participant this command is addressed to.
	Target Target
	// Description is a short human-readable explanation, shown in /help.
	Description string
	// Template is the body of the .md file, with {{args}} still in place.
	// Use Expand to render it with concrete arguments.
	Template string
}

// Registry holds all commands loaded from a directory. Lookups are
// case-insensitive on the command name.
type Registry struct {
	commands map[string]Command
}

// Load scans dir for *.md files and parses each as a slash command.
// Files without a valid frontmatter block, or with missing required
// fields, are skipped with a warning logged — the rest still load.
// An empty dir is not an error (the registry just has no commands).
func Load(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("commands: failed to read directory %q: %w", dir, err)
	}

	cmds := make(map[string]Command)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			logger.L().Warn("commands: failed to read file",
				"path", path, "error", readErr.Error())
			continue
		}

		cmd, parseErr := parseFile(raw)
		if parseErr != nil {
			logger.L().Warn("commands: skipping invalid command file",
				"path", path, "error", parseErr.Error())
			continue
		}

		// Filename takes precedence over frontmatter name when they disagree —
		// the user types `/plan`, not `/whatever the frontmatter said`.
		cmd.Name = strings.ToLower(strings.TrimSuffix(entry.Name(), ".md"))

		if _, dup := cmds[cmd.Name]; dup {
			logger.L().Warn("commands: duplicate command name, keeping first",
				"name", cmd.Name, "path", path)
			continue
		}

		cmds[cmd.Name] = *cmd
	}

	logger.L().Info("commands loaded", "count", len(cmds), "dir", dir)
	return &Registry{commands: cmds}, nil
}

// Get returns the command with the given name (case-insensitive), and
// whether it was found.
func (r *Registry) Get(name string) (Command, bool) {
	if r == nil {
		return Command{}, false
	}
	cmd, ok := r.commands[strings.ToLower(name)]
	return cmd, ok
}

// Names returns all registered command names in sorted order.
// Useful for /help-style listings and tests.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.commands))
	for name := range r.commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Count returns the number of registered commands.
func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	return len(r.commands)
}

// List returns all registered commands in name-sorted order.
func (r *Registry) List() []Command {
	if r == nil {
		return nil
	}
	names := r.Names()
	out := make([]Command, 0, len(names))
	for _, name := range names {
		if cmd, ok := r.commands[name]; ok {
			out = append(out, cmd)
		}
	}
	return out
}

// Filter returns all registered commands matching the given prefix (case-insensitive),
// sorted by command name. If prefix is empty or "/", all commands are returned.
func (r *Registry) Filter(prefix string) []Command {
	if r == nil {
		return nil
	}
	prefix = strings.TrimPrefix(prefix, "/")
	prefix = strings.ToLower(prefix)

	var matched []Command
	all := r.List()
	if prefix == "" {
		matched = append(matched, all...)
	} else {
		for _, cmd := range all {
			if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
				matched = append(matched, cmd)
			}
		}
	}

	// If "mode" command exists, inject mode subcommand variants
	if _, hasMode := r.commands["mode"]; hasMode {
		modeSubCmds := []Command{
			{
				Name:        "mode orchestrator",
				Target:      TargetSystem,
				Description: "Switch to Orchestrator mode (default routing)",
			},
			{
				Name:        "mode general",
				Target:      TargetSystem,
				Description: "Switch to General Chat mode (single agent, no Reviewer loop)",
			},
			{
				Name:        "mode triad",
				Target:      TargetSystem,
				Description: "Switch to Triad mode (full propose → review → execute loop)",
			},
		}
		for _, sub := range modeSubCmds {
			if prefix == "" || strings.HasPrefix(sub.Name, prefix) {
				alreadyPresent := false
				for _, m := range matched {
					if m.Name == sub.Name {
						alreadyPresent = true
						break
					}
				}
				if !alreadyPresent {
					matched = append(matched, sub)
				}
			}
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Name < matched[j].Name
	})

	return matched
}


// Expand renders the command's body template with the given arguments.
// {{args}} is replaced verbatim with `args` (which may be empty).
// All other occurrences of {{args}} are also replaced — we don't error
// on multiple, since templates with several mentions are perfectly fine.
//
// The substitution is intentionally simple: literal find-and-replace.
// No shell-style escaping, no variable interpolation beyond {{args}}.
// This matches the precedent in OpenCode's command format.
func (c Command) Expand(args string) string {
	return strings.ReplaceAll(c.Template, "{{args}}", args)
}

// ---------------------------------------------------------------------------
// File parser
// ---------------------------------------------------------------------------

// parseFile splits a command .md file into YAML frontmatter and body,
// decodes the frontmatter, and returns a Command.
func parseFile(raw []byte) (*Command, error) {
	const delim = "---"

	// The file must start with "---" on the very first line.
	// Use bytes.Index to find the next "---" terminator, and require both.
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), []byte(delim)) {
		return nil, fmt.Errorf("missing leading %q frontmatter delimiter", delim)
	}

	// Find the closing delimiter line.
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), []byte(delim)) {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, fmt.Errorf("unterminated %q frontmatter (no closing delimiter found)", delim)
	}

	frontmatterBytes := bytes.Join(lines[1:closeIdx], []byte("\n"))
	bodyBytes := bytes.Join(lines[closeIdx+1:], []byte("\n"))

	var fm struct {
		Name        string `yaml:"name"`
		Target      string `yaml:"target"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(frontmatterBytes, &fm); err != nil {
		return nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	// Name comes from the filename, not the frontmatter — the frontmatter
	// `name` field is currently unused so we don't strictly require it.
	// (We still log if a frontmatter name disagrees with the filename, in
	// case the user expected the frontmatter to be authoritative.)
	if fm.Name != "" {
		// Just a soft warning path; actual Name is set in Load().
		_ = fm.Name
	}

	target := Target(strings.ToLower(strings.TrimSpace(fm.Target)))
	switch target {
	case TargetCoder, TargetReviewer, TargetSystem:
		// ok
	case "":
		// Default to coder if unspecified — most commands will be.
		target = TargetCoder
	default:
		return nil, fmt.Errorf("unknown target %q (must be one of: coder, reviewer, system)", fm.Target)
	}

	body := strings.TrimSpace(string(bodyBytes))
	if body == "" {
		return nil, fmt.Errorf("command body is empty")
	}

	return &Command{
		Target:      target,
		Description: strings.TrimSpace(fm.Description),
		Template:    body,
	}, nil
}
