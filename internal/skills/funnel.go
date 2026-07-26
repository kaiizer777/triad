package skills

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kaiizer777/triad/internal/tracelog"
	"github.com/kaiizer777/triad/internal/transcript"
)

// MaxSectionsPerTurn is the hard cap on sections Coder may select for a
// single coding turn (work.md §5, "Hard cap: Coder may select at most 3
// sections per task, no exceptions"). This is a ceiling, not a soft
// default — any selection beyond 3 is truncated by the funnel before
// the loaded content gets built.
const MaxSectionsPerTurn = 3

// SelectionPrefix is the line marker Coder emits at the start of a
// turn to declare which sections it has chosen. Format: a single line
// exactly matching
//
//	SELECTED_SECTIONS: ["frontend", "backend"]
//
// (JSON array of strings). The funnel scans Coder's plain-text
// response for this prefix; the rest of the text after the prefix is
// the actual reply the funnel hands back to the loop. The prefix is
// case-sensitive (all-caps) to keep detection unambiguous against
// natural prose that might contain words like "selected sections".
//
// Coder is told in its system prompt to emit this line at the start
// of its first turn of a coding task. Subsequent turns within the
// same active cycle can re-emit it to re-declare (e.g. when the task
// shifts into a new domain) or omit it to use the previous selection.
const SelectionPrefix = "SELECTED_SECTIONS:"

// LoadedSet tracks which sections have already had their Main Skill
// injected this session. The first time a section is selected, its
// Main body fires and the section is marked loaded; subsequent
// touches in the same session inject the Mini body instead. The set
// is a per-session structure — pass it into the funnel on every
// Coder turn so the Main/Mini decision is consistent across turns.
//
// The set is keyed by lowercased section name (matching the
// Registry's case-insensitive lookup).
//
// Forced tracks sections manually pinned via /skill force <name>
// (work.md §8 / Phase 3.6). Forced sections are always injected
// regardless of whether they were auto-selected; this is the
// escape hatch for "Coder keeps missing this domain" cases. Forced
// state is per-session and never written to disk.
type LoadedSet struct {
	loaded map[string]bool
	forced map[string]bool
}

// NewLoadedSet returns an empty LoadedSet.
func NewLoadedSet() *LoadedSet {
	return &LoadedSet{loaded: make(map[string]bool), forced: make(map[string]bool)}
}

// Has reports whether the given section has already had its Main
// Skill fire this session. Lookup is case-insensitive.
func (s *LoadedSet) Has(section string) bool {
	if s == nil {
		return false
	}
	return s.loaded[strings.ToLower(strings.TrimSpace(section))]
}

// Mark records that a section's Main Skill has fired this session.
// Mark is a no-op for empty/whitespace section names so callers can
// pass already-trimmed values without a separate guard.
func (s *LoadedSet) Mark(section string) {
	if s == nil {
		return
	}
	section = strings.ToLower(strings.TrimSpace(section))
	if section == "" {
		return
	}
	s.loaded[section] = true
}

// Sections returns a sorted copy of the loaded set's keys. Useful
// for /status, tests, and observability dumps.
func (s *LoadedSet) Sections() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.loaded))
	for k := range s.loaded {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Force marks a section as forced for the rest of the session
// (work.md §8 / Phase 3.6). The section's Main body fires the next
// time the funnel builds the prompt, regardless of whether the user
// (or Coder) selected it. Force is keyed the same way as Mark
// (lowercased, trimmed) and is a no-op on empty section names.
func (s *LoadedSet) Force(section string) {
	if s == nil {
		return
	}
	section = strings.ToLower(strings.TrimSpace(section))
	if section == "" {
		return
	}
	s.forced[section] = true
	// Forced implies loaded too — the next prompt build should
	// include the section in BuildLoadedBodies output. We don't
	// call Mark here because Mark tracks "Main already fired" (i.e.
// "use Mini next time"), whereas a force means "this section is
	// active in this session, period." Marking it loaded would
	// incorrectly cause the next Main injection to be skipped. We
	// instead rely on the prompt builder to consult Forced() in
	// addition to the loaded set.
}

// Unforce removes a section from the forced set. Subsequent funnel
// turns will only inject it if Coder auto-selects it. No-op if the
// section was not forced.
func (s *LoadedSet) Unforce(section string) {
	if s == nil {
		return
	}
	section = strings.ToLower(strings.TrimSpace(section))
	delete(s.forced, section)
}

// IsForced reports whether the given section is currently forced.
func (s *LoadedSet) IsForced(section string) bool {
	if s == nil {
		return false
	}
	return s.forced[strings.ToLower(strings.TrimSpace(section))]
}

// Forced returns a sorted copy of the forced set's keys. Useful
// for /skill list and observability dumps.
func (s *LoadedSet) Forced() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.forced))
	for k := range s.forced {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LoadDecision records which tier was actually injected for one
// section during one Coder turn. The funnel builds a slice of these
// per turn and hands it to ApplySelection so the caller knows what
// the system delivered (vs. what Coder asked for). The Tier field is
// the canonical tier constant; for sections where no body was
// available (e.g. the registry is empty), Tier is the empty string
// and the Body is empty — callers can check the empty case and skip.
type LoadDecision struct {
	Section string // section label (e.g. "frontend")
	Tier    Tier   // TierMain or TierMini; "" if no body injected
	Body    string // the body actually injected (Main or Mini)
	Name    string // the skill's Name field (mirrors Section but kept for clarity)
}

// ParseSelection scans Coder's plain-text response for the
// SELECTED_SECTIONS: prefix and returns the selected section labels.
// The prefix and the array on the same line are stripped from the
// returned reply text, so the rest of Coder's response can be
// appended to the transcript as a normal [Coder] message.
//
// Returns (sections, remainingText, ok):
//   - sections: the parsed, lowercased, deduped, sorted section
//     labels (up to MaxSectionsPerTurn — anything beyond is dropped).
//     Note: cap truncation happens here, not in the caller, so the
//     caller can log a system note about it.
//   - remainingText: the response text with the SELECTED_SECTIONS
//     line stripped. If no prefix was found, this is the original
//     text unchanged.
//   - ok: true if a selection line was found and parsed cleanly. If
//     ok is false (no prefix, malformed JSON, etc.), sections is nil
//     and the caller should treat this turn as "no selection" — no
//     skill content is injected for this turn, and Coder gets the
//     Stage 1 label list (cheap) but no Main/Mini body.
//
// Selection rules implemented here:
//   - Prefix must be at the start of the text (after leading
//     whitespace) — anywhere else is treated as a stray mention and
//     ignored. This prevents Coder from accidentally injecting a
//     selection by quoting the system-prompt instructions back.
//   - The JSON array must be a single line; multi-line arrays are
//     rejected (no need to handle them — the system prompt tells
//     Coder to put it on one line).
//   - Sections are lowercased and deduped; unknown sections (not in
//     the registry) are dropped silently — Coder's prompt instructs
//     it to pick from the provided labels, so an unknown section
//     means Coder was confused; dropping is friendlier than erroring.
//   - Up to MaxSectionsPerTurn are returned; any extras are dropped
//     and a truncation flag is set on the returned selection.
func ParseSelection(text string, reg *Registry) (sections []string, remaining string, truncated bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, SelectionPrefix) {
		return nil, text, false
	}

	// The prefix is followed by whitespace + JSON array on the same line.
	// Split on the first newline so we can hand back the rest of the
	// response (any Coder prose / planning message) as `remaining`.
	firstLine, rest, hasNewline := strings.Cut(trimmed, "\n")
	prefixAndArray := strings.TrimSpace(strings.TrimPrefix(firstLine, SelectionPrefix))
	if prefixAndArray == "" {
		// No array after the prefix — treat as malformed.
		return nil, text, false
	}

	var raw []string
	if err := json.Unmarshal([]byte(prefixAndArray), &raw); err != nil {
		// Malformed JSON — Coder is confused; treat the whole turn
		// as no-selection. The rest of the text is preserved.
		return nil, text, false
	}

	// Lowercase, trim, dedupe, and filter against the registry if
	// one is provided (nil registry → keep everything; tests use
	// this to drive the funnel without standing up a real registry).
	seen := make(map[string]bool)
	var clean []string
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		if reg != nil {
			if _, ok := reg.GetBySection(s); !ok {
				continue
			}
		}
		seen[s] = true
		clean = append(clean, s)
	}

	// Stable order: alphabetical. Coder might emit them in any order;
	// we apply the cap after sorting so the kept set is deterministic
	// for tests and the transcript log.
	sort.Strings(clean)

	truncated = len(clean) > MaxSectionsPerTurn
	if truncated {
		clean = clean[:MaxSectionsPerTurn]
	}

	// Rebuild remaining text: strip the first line (the
	// SELECTED_SECTIONS: ... line) and preserve everything else.
	rem := rest
	if !hasNewline {
		// Coder's whole response was the SELECTED_SECTIONS line with
		// nothing after — rem is empty. That's a legitimate case
		// (Coder just wanted to declare its selection and then call
		// a tool). Pass through the empty string as remaining.
		rem = ""
	}

	// Trim leading whitespace on `rem` so the transcript's [Coder]
	// entry doesn't start with a blank line — but only if there's
	// actually content after.
	if strings.TrimSpace(rem) != "" {
		rem = strings.TrimLeft(rem, " \t\r\n")
	}

	return clean, rem, truncated
}

// BuildSystemPromptStage1 returns the Stage 1 extension to Coder's
// system prompt: a small, cheap section-label list that Coder sees
// every turn. The format is fixed — Coder is told exactly how to
// declare its selection back to the funnel via SELECTED_SECTIONS:.
//
// `reg` may be nil or empty — in that case the returned string is an
// empty string and Coder sees no skills scaffolding (the spec's
// "no skills configured" case). The caller should not append the
// returned string in that case; this function is a pure builder.
//
// IMPORTANT: this extension must stay cheap. work.md §5 requires
// the Stage 1 scan to scale to 100+ sections at < 500 tokens. Do
// not add description or body text here — that would silently break
// the "Stage 1 stays cheap" invariant.
func BuildSystemPromptStage1(reg *Registry) string {
	if reg == nil || reg.Count() == 0 {
		return ""
	}
	sections := reg.Sections() // already sorted by the Registry
	var b strings.Builder
	b.WriteString("\n\nSKILLS (task-driven injection — Workflow 5):\n")
	b.WriteString("On the first line of your reply (before any prose or tool call), declare which skill sections this task touches using EXACTLY this format on its own line:\n")
	b.WriteString("  SELECTED_SECTIONS: [\"<section1>\", \"<section2>\", ...]\n")
	b.WriteString("Pick 0 to 3 sections from the list below. The list is a single token per label — you don't see descriptions here, so if a section is unfamiliar, skip it. Picking 0 means \"no skill content is needed for this turn\" (e.g. a pure chat / planning turn).\n")
	b.WriteString("\nAvailable sections:\n")
	for _, s := range sections {
		b.WriteString("  - ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	b.WriteString("\nRules:\n")
	b.WriteString("  - The SELECTED_SECTIONS line must be the FIRST line of your reply (after any leading whitespace).\n")
	b.WriteString("  - It must be a single-line JSON array of strings. Examples: SELECTED_SECTIONS: [] or SELECTED_SECTIONS: [\"frontend\"].\n")
	b.WriteString("  - At most 3 sections. Extra sections in your list are dropped — pick the most relevant ones.\n")
	b.WriteString("  - Use the EXACT section labels above. Unknown labels are dropped silently.\n")
	b.WriteString("  - If your reply is purely a tool call (no prose), the SELECTED_SECTIONS line is still required as the first line — this is mandatory, not optional.\n")
	return b.String()
}

// BuildLoadedBodies returns the Stage 2 body block: the Mini body
// of every section in `loaded`, wrapped in the
// `--- skill:NAME (tier: mini) ---` delimiters. Empty-string Mini
// bodies are silently skipped.
//
// This is the same shape ApplySelection uses for the first
// injection, so the prompt is consistent across first-touch and
// subsequent-turn cases. Returns an empty string when nothing is
// loaded, when the registry is nil, or when all loaded sections
// have empty Mini bodies — callers can append unconditionally.
//
// Forced sections (from /skill force) are emitted at Main tier the
// first time they appear, and Mini on every turn after. Tracking
// which forced sections have already had their Main is done by
// the loaded set: we treat a forced section the same as a
// Coder-selected one — if it's not yet in `loaded`, emit Main and
// mark it; if it is, emit Mini. This keeps Main-tier cardinality
// invariant intact even via the manual override path.
func BuildLoadedBodies(reg *Registry, loaded *LoadedSet) string {
	if reg == nil || loaded == nil {
		return ""
	}
	// Union of loaded + forced sections. A forced section that
	// hasn't been "loaded" yet still needs its Main to fire on
	// the first turn the forced state is observed; from then on
	// Mini. Mirroring the auto-selection funnel: the only
	// difference is that "the user pinned this" rather than
	// "Coder picked it."
	seen := make(map[string]bool)
	for _, s := range loaded.Sections() {
		seen[s] = true
	}
	for _, s := range loaded.Forced() {
		seen[s] = true
	}
	if len(seen) == 0 {
		return ""
	}
	// Stable output order: alphabetical by section label.
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, sec := range keys {
		sk, ok := reg.GetBySection(sec)
		if !ok {
			continue
		}
		// Tier decision: first time this section appears in the
		// prompt (i.e. not yet in `loaded`), emit Main and mark
		// loaded. Subsequent turns → Mini.
		tier := TierMini
		var body string
		if !loaded.Has(sec) {
			tier = TierMain
			body = sk.MainBody
			loaded.Mark(sec)
		} else {
			body = sk.MiniBody
		}
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "\n--- skill:%s (tier: %s) ---\n%s\n", sk.Name, tier, body)
	}
	return b.String()
}

// BuildCoderSystemPromptExtension is the full system-prompt
// extension for a Coder turn: Stage 1 (cheap section labels) +
// Stage 2 (Mini bodies for already-loaded sections). Returns "" if
// `reg` is nil or empty — callers should append unconditionally,
// the no-op case is handled here.
//
// Used by both the headless loop and the TUI to keep the funnel's
// prompt shape consistent across paths. Adding the extension to
// the persistent AgentConfig.SystemPrompt is a bug; always build
// a per-turn value-copy of the config first.
func BuildCoderSystemPromptExtension(reg *Registry, loaded *LoadedSet) string {
	if reg == nil || reg.Count() == 0 {
		return ""
	}
	return BuildSystemPromptStage1(reg) + BuildLoadedBodies(reg, loaded)
}

// ParseAndApply is the post-call half of the funnel: scan a Coder
// response for a SELECTED_SECTIONS line, parse the selection,
// apply it (mark loaded, log system entry), and return the
// cleaned response text with the control line stripped.
//
// Returns:
//   - cleaned: the response text with the SELECTED_SECTIONS line
//     removed. If no prefix was found OR the prefix was malformed,
//     this is the original text unchanged.
//   - decisions: the LoadDecisions ApplySelection produced. Empty
//     when no selection was parsed. Useful for tests and the
//     TUI/loop call sites that want to react to what was loaded.
//
// `task` is the most recent human task — included in the [Skills]
// system entry ApplySelection writes. Pass "" to omit.
//
// This is a thin convenience wrapper that the loop and TUI both
// call after `client.Respond`. It does NOT make the API call —
// callers still own that step (so they can plug in their own
// mocks, retry logic, and async-wrapping). The funnel only owns
// the pre-call prompt building and the post-call response
// processing.
func ParseAndApply(
	responseText string,
	reg *Registry,
	loaded *LoadedSet,
	tr *transcript.Transcript,
	task string,
) (cleaned string, decisions []LoadDecision) {
	selected, remaining, truncated := ParseSelection(responseText, reg)
	if selected == nil {
		return responseText, nil
	}
	decisions = ApplySelection(selected, truncated, reg, loaded, tr, task)
	return remaining, decisions
}

// ApplySelection is the Stage 2 load step. Given the parsed section
// selection, the registry, the per-session loaded set, and a
// transcript to record the decision to, it:
//   1. For each selected section, looks up the skill, decides
//      Main (first touch) vs Mini (subsequent) based on the loaded
//      set, and builds a LoadDecision.
//   2. Marks each freshly-loaded Main in the loaded set so
//      subsequent turns pick Mini and the loop's prompt builder
//      can find the section via LoadedSet.Sections().
//   3. Writes a single System entry to the transcript recording
//      which sections were selected, which tier was actually
//      injected per section, and whether the selection was
//      truncated by the cap. This entry is what Phase 4's
//      observability work (work.md §7) will read from.
//
// Returns the LoadDecisions so the caller (and tests) can inspect
// what was loaded for this turn.
//
// IMPORTANT: ApplySelection does NOT return a prompt addition
// that should be injected into the current turn. The Main body
// was never "in the prompt" the turn Coder emitted the selection
// (it was the response that triggered the load); on the next
// turn, the prompt builder consults the loaded set directly and
// injects Mini bodies for every loaded section. This two-step
// shape (Stage 1 declaration turn → Stage 2 loaded turn) is what
// lets the spec's "Main fires once per session" stay true without
// us having to thread a per-turn prompt delta through the call
// site.
//
// `truncated` is the value ParseSelection returned — if true, the
// system note records the truncation so the human can see it
// happened.
//
// `task` is the most recent human task message — included in the
// system note so the observability work can correlate skill
// choices with the task that triggered them.
func ApplySelection(
	sections []string,
	truncated bool,
	reg *Registry,
	loaded *LoadedSet,
	tr *transcript.Transcript,
	task string,
) []LoadDecision {
	decisions := make([]LoadDecision, 0, len(sections))
	if reg == nil || len(sections) == 0 {
		// Still record the (empty) selection for observability —
		// the Phase 4 trace view will read these entries to answer
		// "which sections did Coder pick this turn?"
		appendSkillSelectionEntry(tr, loaded, task, sections, nil, truncated, false)
		return decisions
	}

	hadUnknown := false

	for _, sec := range sections {
		sk, ok := reg.GetBySection(sec)
		if !ok {
			// Should be filtered by ParseSelection, but be defensive.
			hadUnknown = true
			continue
		}

		var (
			tier Tier
			body string
		)
		if loaded.Has(sec) {
			tier = TierMini
			body = sk.MiniBody
		} else {
			tier = TierMain
			body = sk.MainBody
			loaded.Mark(sec)
		}

		if body == "" {
			// Section selected but the chosen tier's body is empty.
			// Don't inject an empty block — record the decision
			// with an empty tier so the observability layer can
			// still see "Coder picked this section but the body
			// was empty."
			decisions = append(decisions, LoadDecision{
				Section: sec,
				Name:    sk.Name,
				Tier:    "",
				Body:    "",
			})
			continue
		}

		decisions = append(decisions, LoadDecision{
			Section: sec,
			Name:    sk.Name,
			Tier:    tier,
			Body:    body,
		})
	}

	appendSkillSelectionEntry(tr, loaded, task, sections, decisions, truncated, hadUnknown)

	return decisions
}

// appendSkillSelectionEntry writes the per-turn selection record to
// the transcript as a System entry AND to the session's trace log as
// a structured EventSkillSelection entry. The transcript keeps the
// human-readable block (for the existing reviewer / transcript
// reader); the trace log carries the parseable per-section payload
// (sections, tier, token cost, task excerpt, cap-truncation flag)
// that /trace renders for Phase 4 observability.
//
// Both writes are append-only — never rewriting history. The
// transcript append was the original behavior; the trace append is
// new for Phase 4. Both go through the same write path so the
// relative ordering between transcript and trace is preserved
// (writes from the same turn land in the same wall-clock
// millisecond on most filesystems, so /trace and the transcript
// stay in sync).
//
// Why both surfaces:
//
//   - Transcript: in-band message log the model and Reviewer see.
//     The block-format string is for humans reading the transcript
//     directly.
//   - Trace: append-only, structured, multi-event log. /trace reads
//     from it. Rich fields (tier per section, token cost, forced
//     flag) live here so the renderer can build a proper table
//     instead of re-parsing prose.
//
// The two are not redundant: the transcript is what the model sees
// when it reviews prior turns; the trace is what the human sees
// when they ask "what just happened." Different audiences, same
// event.
//
// Parameters:
//   - tr: the in-memory transcript. May be nil (callers like
//     parser-only tests don't have one) — the function no-ops in
//     that case.
//   - loaded: the per-session loaded set. Used here to detect
//     which of the selected sections are /skill force-pinned, so
//     the trace can render a [forced] tag. May be nil.
//   - task, selected, decisions, truncated, hadUnknown: same shape
//     as before.
func appendSkillSelectionEntry(
	tr *transcript.Transcript,
	loaded *LoadedSet,
	task string,
	selected []string,
	decisions []LoadDecision,
	truncated, hadUnknown bool,
) {
	// Trace log first — the human-facing observability surface
	// for Phase 4. Computed from the same decisions, so the two
	// writes are guaranteed to agree on what was selected.
	appendSkillSelectionTrace(tr, loaded, task, selected, decisions, truncated)

	if tr == nil {
		return
	}
	var b strings.Builder
	b.WriteString("[Skills] Turn skill selection")
	if truncated {
		b.WriteString(" (cap-truncated to 3 sections)")
	}
	if hadUnknown {
		b.WriteString(" (some unknown sections dropped)")
	}
	b.WriteString(":\n")
	if len(decisions) == 0 {
		if len(selected) == 0 {
			b.WriteString("  (none — Coder picked 0 sections for this turn)\n")
		} else {
			b.WriteString("  (selected sections had no bodies to inject)\n")
		}
	} else {
		for _, d := range decisions {
			annotation := "first touch this session"
			if d.Tier == TierMini {
				annotation = "already loaded this session"
			} else if d.Tier == "" {
				annotation = "empty body, nothing injected"
			}
			fmt.Fprintf(&b, "  - section: %s, tier: %s (%s), name: %s\n", d.Section, d.Tier, annotation, d.Name)
		}
	}
	if task != "" {
		// Keep task short — full task text could be very long; the
		// observability layer is for "why did Coder pick this skill",
		// not "what was the whole task". 200 chars is plenty for
		// correlation and keeps the entry compact.
		taskExcerpt := task
		const maxTaskExcerpt = 200
		if len(taskExcerpt) > maxTaskExcerpt {
			taskExcerpt = taskExcerpt[:maxTaskExcerpt] + "..."
		}
		fmt.Fprintf(&b, "  task: %s\n", taskExcerpt)
	}

	_ = tr.Append(transcript.Entry{
		Speaker:   transcript.SpeakerSystem,
		Type:      transcript.TypeMessage,
		Content:   b.String(),
		Timestamp: time.Now(),
	})
}

// appendSkillSelectionTrace writes a single EventSkillSelection entry
// to the current session's trace log. Phase 4 observability: this is
// the data /trace reads to render the multi-line skill-selection
// block. The entry's Data field carries the structured payload
// (sections, tier per section, token cost, task excerpt, cap flag)
// that the renderer (FormatSkillSelectionLine) consumes.
//
// Trace path is derived from the transcript's bound file path. If
// the transcript has no bound path (some tests construct a
// transcript without one), this function is a no-op — without a
// path we can't derive a session-specific trace file, and writing
// to the default trace would cause cross-test pollution against
// the on-disk default.jsonl. This is the same contract as the rest
// of the trace-writing call sites (the orchestrator/clarify paths
// always have a bound transcript, so the no-path branch is only
// hit by unit tests that don't bind a file).
//
// Errors are swallowed (the same way the rest of the trace-writing
// call sites do). The trace log is an observability surface, not a
// correctness-critical path — failing to write a trace entry should
// never fail a Coder turn. The human can always read the transcript
// as a fallback.
func appendSkillSelectionTrace(
	tr *transcript.Transcript,
	loaded *LoadedSet,
	task string,
	selected []string,
	decisions []LoadDecision,
	truncated bool,
) {
	if tr == nil {
		return
	}
	sessionPath := tr.FilePath()
	if sessionPath == "" {
		// No bound session — skip. Documented in the comment above.
		return
	}
	tracePath := tracelog.TracePathForSession(sessionPath)
	// Build the structured payload. Each decision carries the
	// tier + token cost the trace renderer needs. The forced
	// flag is read from the loaded set (the registry doesn't
	// know which sections are forced — the funnel's manual
	// override path is what marks them).
	traceData := tracelog.SkillSelectionData{
		Task:      truncateTaskExcerpt(task),
		Selected:  append([]string(nil), selected...),
		Truncated: truncated,
		Decisions: make([]tracelog.SkillSelectionDecision, 0, len(decisions)),
	}
	for _, d := range decisions {
		cost := EstimateInjectedTokens(d.Name, d.Tier, d.Body)
		traceData.Decisions = append(traceData.Decisions, tracelog.SkillSelectionDecision{
			Section:   d.Section,
			Tier:      string(d.Tier),
			TokenCost: cost,
			Forced:    loaded != nil && loaded.IsForced(d.Section),
		})
		traceData.TotalTokens += cost
	}

	// One-line Description: the legacy trace consumers that read
	// only Description (e.g. a `grep` over the JSONL, or a
	// future tool that hasn't been updated for the new
	// multi-line format) still get something useful. Format:
	// "sections: a(main), b(mini)" — same data as Data, in
	// prose.
	desc := buildSkillSelectionDescription(selected, decisions, truncated)

	_ = tracelog.Append(tracePath, tracelog.Entry{
		Entity:      "skills",
		EventType:   tracelog.EventSkillSelection,
		Description: desc,
		Data: map[string]any{
			"task":         traceData.Task,
			"selected":     traceData.Selected,
			"decisions":    traceData.Decisions,
			"truncated":    traceData.Truncated,
			"total_tokens": traceData.TotalTokens,
		},
	})
}

// truncateTaskExcerpt applies the same 200-char truncation the
// transcript-side writer uses, so the trace and the transcript show
// the same task excerpt for the same turn. Centralizing the limit
// here keeps the two surfaces consistent.
func truncateTaskExcerpt(task string) string {
	if task == "" {
		return ""
	}
	const maxTaskExcerpt = 200
	if len(task) > maxTaskExcerpt {
		return task[:maxTaskExcerpt] + "..."
	}
	return task
}

// buildSkillSelectionDescription builds the one-line Description
// for the trace entry. Kept as a small helper so the test for
// ApplySelection (which reads the trace JSONL) can pin the exact
// string without depending on the multi-line renderer.
func buildSkillSelectionDescription(selected []string, decisions []LoadDecision, truncated bool) string {
	parts := make([]string, 0, len(decisions))
	for _, d := range decisions {
		parts = append(parts, fmt.Sprintf("%s(%s)", d.Section, d.Tier))
	}
	desc := fmt.Sprintf("sections: [%s]", strings.Join(parts, ", "))
	if truncated {
		desc += " [cap-truncated]"
	}
	if len(parts) == 0 {
		if len(selected) == 0 {
			desc = "sections: [none]"
		} else {
			desc = "sections: [no bodies injected]"
		}
	}
	return desc
}
