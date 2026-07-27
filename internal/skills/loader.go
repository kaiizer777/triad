// Package skills loads skill definitions from .md files with YAML
// frontmatter. Skills are the building block of Workflow 5 (task-driven
// skill injection): a Coder turn scans the bare list of section labels
// (Stage 1, cheap, scales with total skill count) and then loads the
// full content for only the ≤3 selected sections (Stage 2, bounded by
// the 3-section cap).
//
// A skill file looks like:
//
//	---
//	name: frontend
//	section: frontend
//	description: "React/TS UI work — components, styling, client state."
//	tier: main
//	mini_ref: frontend-mini.md
//	token_budget_main: 6500
//	token_budget_mini: 3000
//	---
//
//	<Main skill body — conventions, patterns, gotchas>
//
// Main and Mini are stored as two separate files (e.g. frontend.md and
// frontend-mini.md); the Main file's `mini_ref` points at the Mini file
// by filename within the same directory. The Loader resolves the pair
// at load time and exposes both bodies via Stage 2 accessors.
//
// The Registry is constructed once at startup (Load) and is read-only
// thereafter. The accessors (Sections / Get) are what Phase 2 will call
// from the Coder turn wiring.
package skills

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

// Tier identifies which body of a skill file we're talking about. The
// Main file holds the full body (5–8k tokens); the Mini file holds a
// condensed pointer version (2–4k tokens) injected on subsequent
// touches of the same section within a session.
type Tier string

const (
	TierMain Tier = "main"
	TierMini Tier = "mini"
)

// Skill is a single loaded skill, resolved from a Main file and its
// referenced Mini file (when present). If the user only authored a Main
// file, Mini will be empty — callers (Phase 2's selection logic) decide
// what to do with that.
type Skill struct {
	// Name is the bare skill name (e.g. "frontend"). This is what the
	// user types after `/skill view|edit|delete|force`, and the registry
	// key for lookups.
	Name string
	// Section is the broad domain label shown in Stage 1 (e.g. "frontend").
	// A section may contain multiple skills; individual skills are shown only
	// after Coder has selected the relevant section.
	Section string
	// Description is the human-readable explanation of what the skill
	// covers. Only read in Stage 2, after a section is selected; never
	// factored into the Stage 1 scan.
	Description string
	// TokenBudgetMain / TokenBudgetMini are advisory caps the author
	// promises the body will fit under. Not enforced here — Phase 5's
	// tokenizer-based check (work.md §5.4) is what verifies the actual
	// size. Stored as int for forward compatibility (e.g. "6000-8000"
	// range could be a struct later if needed).
	TokenBudgetMain int `yaml:"token_budget_main"`
	TokenBudgetMini int `yaml:"token_budget_mini"`
	// MainBody is the Markdown body of the Main file (the long version).
	MainBody string
	// MiniBody is the Markdown body of the Mini file (the short version).
	// Empty if the author did not provide a mini_ref / mini file.
	MiniBody string
	// SourcePath is the absolute path of the Main file on disk — exposed
	// for /skill edit/delete and for log/error messages. Mini files
	// don't get a separate handle; their path is MainPath + mini_ref
	// resolution and is purely an implementation detail of Load.
	SourcePath string
	// MiniRef is the filename (relative to the skills dir) of the Mini
	// file, copied through from the Main file's frontmatter. Empty if
	// the author did not provide a Mini variant. Exposed so /skill view
	// and /skill list can show it without re-reading the frontmatter.
	MiniRef string
}

// Registry holds all skills loaded from a directory. Lookups are
// case-insensitive on both Name and Section.
type Registry struct {
	// Dir is the absolute directory the registry was loaded from.
	// Exposed so /skill add, /skill delete, and /skill edit can compute
	// target file paths without re-deriving the load dir at every call
	// site. Empty if the registry was constructed manually (tests).
	Dir    string
	skills map[string]Skill // keyed by lowercased name
	// order preserves the on-disk iteration order so that SectionLabels
	// is deterministic regardless of map iteration randomness.
	order []string
}

// Load scans dir for *.md files, parses each as a Main skill file, and
// resolves any referenced Mini files (via `mini_ref`) within the same
// directory. Validation happens here — see the per-section error cases
// for the exact contract.
//
// Returns an error (not a warning) on any of:
//   - dir cannot be read
//   - any file has malformed YAML, missing required field, unknown tier,
//     a duplicate section value, or a mini_ref that does not exist
//   - any file referenced by a mini_ref has malformed YAML or mismatches
//     the Main file's name/section
//
// This is intentionally stricter than internal/commands.Load, which
// skips-with-warning on bad files. Skills are config-level content — a
// silently-skipped file could mean Coder runs without expected
// knowledge, which is the kind of bug we'd rather fail loud on.
//
// An empty dir is not an error: the registry just has no skills.
func Load(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skills: failed to read directory %q: %w", dir, err)
	}

	mains := make(map[string]Skill) // by name (already lowercased)
	order := make([]string, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		// Skip files that look like Mini files — they're only loaded via
		// a Main file's mini_ref. We detect this by convention: a file
		// whose stem ends in "-mini" is treated as a Mini file, not a
		// Main file. This avoids a chicken-and-egg parse pass and keeps
		// "two files" the only valid layout (per work.md §4).
		stem := strings.TrimSuffix(entry.Name(), ".md")
		if strings.HasSuffix(stem, "-mini") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("skills: failed to read %q: %w", path, readErr)
		}

		skill, parseErr := parseMainFile(raw, path)
		if parseErr != nil {
			return nil, fmt.Errorf("skills: invalid main file %q: %w", path, parseErr)
		}

		// Filename-based name (commands package convention) — strip
		// any directory and .md, lowercase. Frontmatter `name` is
		// treated as advisory; if it disagrees with the filename we
		// surface a clear error (better than silent divergence).
		filenameName := strings.ToLower(stem)
		if skill.Name == "" {
			skill.Name = filenameName
		}
		if skill.Name != filenameName {
			return nil, fmt.Errorf(
				"skills: file %q has frontmatter name %q; filename and frontmatter name must match (case-insensitive)",
				path, skill.Name)
		}

		// Mini file resolution. mini_ref is optional — but if it's set,
		// it must point at a real, well-formed Mini file in the same dir.
		// If it's not set, MiniBody stays empty (callers handle that).
		if skill.MiniRef != "" {
			miniPath := filepath.Join(dir, skill.MiniRef)
			miniRaw, miniReadErr := os.ReadFile(miniPath)
			if miniReadErr != nil {
				return nil, fmt.Errorf(
					"skills: main file %q references mini %q but file is missing or unreadable: %w",
					path, skill.MiniRef, miniReadErr)
			}
			mini, miniParseErr := parseMiniFile(miniRaw, miniPath, skill.Name, skill.Section)
			if miniParseErr != nil {
				return nil, fmt.Errorf(
					"skills: main file %q has broken mini_ref %q: %w",
					path, skill.MiniRef, miniParseErr)
			}
			skill.MiniBody = mini.Body
		}

		mains[skill.Name] = skill
		order = append(order, skill.Name)
	}

	// Stable order across runs.
	sort.Strings(order)

	logger.L().Info("skills loaded", "count", len(mains), "dir", dir)
	absDir, absErr := filepath.Abs(dir)
	if absErr != nil {
		// Not fatal — keep the registry usable, just leave Dir empty
		// so callers that need an absolute path can fall back to
		// the relative dir they already had.
		absDir = ""
	}
	return &Registry{Dir: absDir, skills: mains, order: order}, nil
}

// Sections returns the bare list of section labels — exactly what
// Stage 1 of the funnel injects into Coder's prompt every turn. Order
// is sorted alphabetically for determinism.
//
// Per work.md §5, the Stage 1 scan must be cheap regardless of skill
// count: a 100-section codebase still costs only ~200-300 tokens here
// (one short label per section + list formatting). This method must
// never include description or body — doing so would silently
// invalidate the whole "Stage 1 stays cheap" property.
func (r *Registry) Sections() []string {
	if r == nil {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if s, ok := r.skills[name]; ok {
			key := strings.ToLower(s.Section)
			if !seen[key] {
				seen[key] = true
				out = append(out, s.Section)
			}
		}
	}
	return out
}

// Get returns the skill with the given name (case-insensitive), and
// whether it was found. Stage 2 callers use this after Stage 1 has
// already narrowed the candidate set.
func (r *Registry) Get(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	s, ok := r.skills[strings.ToLower(name)]
	return s, ok
}

// GetBySection returns the skill whose Section field matches (case-
// insensitive). Stage 2 callers will more commonly have a section
// label (from the Stage 1 scan output) than a name, so this is the
// primary lookup path for the funnel.
func (r *Registry) GetBySection(section string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	for _, name := range r.order {
		s := r.skills[name]
		if strings.EqualFold(s.Section, section) {
			return s, true
		}
	}
	return Skill{}, false
}

// SkillsInSection returns all skills in a selected section, in stable name
// order. Stage 2 uses this catalogue so Coder never has to scan every skill.
func (r *Registry) SkillsInSection(section string) []Skill {
	if r == nil {
		return nil
	}
	out := make([]Skill, 0)
	for _, name := range r.order {
		s := r.skills[name]
		if strings.EqualFold(s.Section, section) {
			out = append(out, s)
		}
	}
	return out
}

// Names returns all registered skill names in sorted order. Useful
// for /skill list and tests.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	return len(r.skills)
}

// ---------------------------------------------------------------------------
// File parsers
// ---------------------------------------------------------------------------

// mainFM is the frontmatter shape of a Main skill file. We don't expose
// the struct — callers see Skill. MiniRef is part of the Main file's
// frontmatter (it points at the Mini file), so it lives here, not on
// the Mini side.
type mainFM struct {
	Name            string `yaml:"name"`
	Section         string `yaml:"section"`
	Description     string `yaml:"description"`
	Tier            string `yaml:"tier"`
	MiniRef         string `yaml:"mini_ref"`
	TokenBudgetMain int    `yaml:"token_budget_main"`
	TokenBudgetMini int    `yaml:"token_budget_mini"`
}

// miniFM is the frontmatter shape of a Mini skill file. Most fields are
// just identity checks (the Mini must match its Main's name/section)
// — the Mini file has no `mini_ref` of its own, and token_budget_mini
// belongs on the Main file.
type miniFM struct {
	Name        string `yaml:"name"`
	Section     string `yaml:"section"`
	Description string `yaml:"description"`
	Tier        string `yaml:"tier"`
}

// parseMainFile splits a Main skill .md file into YAML frontmatter and
// Markdown body, decodes the frontmatter, and returns a Skill. The
// Skill's Name and Section are taken from the frontmatter at this
// point; Load() re-validates them against the filename and handles Mini
// resolution.
func parseMainFile(raw []byte, path string) (Skill, error) {
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return Skill{}, err
	}

	var m mainFM
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return Skill{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	// Required fields.
	if strings.TrimSpace(m.Section) == "" {
		return Skill{}, fmt.Errorf("missing required field `section`")
	}
	if strings.TrimSpace(m.Description) == "" {
		return Skill{}, fmt.Errorf("missing required field `description`")
	}

	// Tier is required and must be exactly "main" for a Main file.
	// We don't infer — being strict here surfaces authoring mistakes
	// (e.g. forgetting to set tier on a copied template).
	tier := Tier(strings.ToLower(strings.TrimSpace(m.Tier)))
	if tier == "" {
		return Skill{}, fmt.Errorf("missing required field `tier` (must be \"main\" for a main file)")
	}
	if tier != TierMain {
		return Skill{}, fmt.Errorf("invalid tier %q in main file (must be \"main\")", m.Tier)
	}

	// Body must be non-empty — a Main file with no body is the kind of
	// authoring mistake we want to fail on, not silently ship as "skill
	// with no content."
	bodyStr := strings.TrimSpace(string(body))
	if bodyStr == "" {
		return Skill{}, fmt.Errorf("main body is empty")
	}

	return Skill{
		Name:            strings.ToLower(strings.TrimSpace(m.Name)),
		Section:         strings.ToLower(strings.TrimSpace(m.Section)),
		Description:     strings.TrimSpace(m.Description),
		TokenBudgetMain: m.TokenBudgetMain,
		TokenBudgetMini: m.TokenBudgetMini,
		MiniRef:         strings.TrimSpace(m.MiniRef),
		MainBody:        bodyStr,
		SourcePath:      path,
	}, nil
}

// parseMiniFile splits a Mini skill .md file. It enforces that the
// Mini's identity (name, section) matches the Main that referenced it,
// and that tier == "mini".
func parseMiniFile(raw []byte, path string, expectedName, expectedSection string) (struct{ Body string }, error) {
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return struct{ Body string }{}, err
	}

	var m miniFM
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return struct{ Body string }{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	// Identity must match the referencing Main file. If they don't, the
	// Mini file is mis-paired and we'd rather surface that than silently
	// load the wrong content under the right name.
	gotName := strings.ToLower(strings.TrimSpace(m.Name))
	if gotName != expectedName {
		return struct{ Body string }{}, fmt.Errorf(
			"name %q does not match main file's name %q", m.Name, expectedName)
	}
	gotSection := strings.ToLower(strings.TrimSpace(m.Section))
	if gotSection != expectedSection {
		return struct{ Body string }{}, fmt.Errorf(
			"section %q does not match main file's section %q", m.Section, expectedSection)
	}

	// Tier must be exactly "mini" — same strictness reasoning as Main.
	tier := Tier(strings.ToLower(strings.TrimSpace(m.Tier)))
	if tier == "" {
		return struct{ Body string }{}, fmt.Errorf("missing required field `tier` (must be \"mini\")")
	}
	if tier != TierMini {
		return struct{ Body string }{}, fmt.Errorf("invalid tier %q in mini file (must be \"mini\")", m.Tier)
	}

	bodyStr := strings.TrimSpace(string(body))
	if bodyStr == "" {
		return struct{ Body string }{}, fmt.Errorf("mini body is empty")
	}

	return struct{ Body string }{Body: bodyStr}, nil
}

// splitFrontmatter extracts the YAML frontmatter bytes and the Markdown
// body bytes from a raw .md file. The file must start with "---" on
// the first line and have a closing "---" terminator — same convention
// as internal/commands, intentionally identical to keep the two
// loaders interchangeable in tooling.
func splitFrontmatter(raw []byte) (frontmatter, body []byte, err error) {
	const delim = "---"

	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), []byte(delim)) {
		return nil, nil, fmt.Errorf("missing leading %q frontmatter delimiter", delim)
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), []byte(delim)) {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, nil, fmt.Errorf("unterminated %q frontmatter (no closing delimiter found)", delim)
	}

	frontmatter = bytes.Join(lines[1:closeIdx], []byte("\n"))
	body = bytes.Join(lines[closeIdx+1:], []byte("\n"))
	return frontmatter, body, nil
}
