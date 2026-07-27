package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/transcript"
)

// writeSkillFile writes a complete Main (+ optional Mini) skill
// file to dir, returning the name of the file written. Used by
// the funnel tests to spin up small fixtures without re-running
// the full registry parse path on every case.
//
// IMPORTANT: when miniBody is non-empty, this writes a -mini.md
// file AND adds the corresponding `mini_ref:` line to the Main
// file's frontmatter — the loader's design (work.md §4, see also
// internal/skills/loader.go) requires the Main file to reference
// the Mini file by name; a standalone -mini.md is silently
// skipped. Forgetting the mini_ref is a real loader bug the
// funnel tests should keep catching.
func writeSkillFile(t *testing.T, dir, name, section, description, mainBody, miniBody string) {
	t.Helper()
	main := "---\nname: " + name + "\nsection: " + section +
		"\ndescription: \"" + description + "\"\ntier: main\n"
	if miniBody != "" {
		main += "mini_ref: " + name + "-mini.md\n"
	}
	main += "\n---\n" + mainBody + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main %s: %v", name, err)
	}
	if miniBody != "" {
		mini := "---\nname: " + name + "\nsection: " + section +
			"\ndescription: \"" + description + "\"\ntier: mini\n" +
			"\n---\n" + miniBody + "\n"
		if err := os.WriteFile(filepath.Join(dir, name+"-mini.md"), []byte(mini), 0o644); err != nil {
			t.Fatalf("write mini %s: %v", name, err)
		}
	}
}

// threeSkillFixture writes frontend/backend/db skill files (with
// Mini variants) into a temp dir and returns a loaded Registry
// pointing at them. Used by the Phase 2 funnel tests.
func threeSkillFixture(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	writeSkillFile(t, dir, "frontend", "frontend", "React/TS UI work.",
		"FRONTEND MAIN BODY: react components go here.",
		"FRONTEND MINI: keep components small.")
	writeSkillFile(t, dir, "backend", "backend", "Go server work.",
		"BACKEND MAIN BODY: handlers and routes.",
		"BACKEND MINI: prefer small handlers.")
	writeSkillFile(t, dir, "db", "db", "Postgres schema and queries.",
		"DB MAIN BODY: schemas, indexes, migrations.",
		"DB MINI: check indexes.")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}

// --- 2.8: single-domain Main → Mini transition ---------------------------

// TestApplySelection_FirstTouchMain verifies that a section's first
// selection this session injects its Main body and marks the
// section as loaded. This is the core "Main fires once per
// session" invariant the spec relies on.
func TestApplySelection_FirstTouchMain(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	decisions := ApplySelection([]string{"frontend"}, false, reg, loaded, tr, "fix the login button")

	if len(decisions) != 1 {
		t.Fatalf("decisions: got %d, want 1", len(decisions))
	}
	if decisions[0].Section != "frontend" {
		t.Errorf("decision section: got %q, want %q", decisions[0].Section, "frontend")
	}
	if decisions[0].Tier != TierMain {
		t.Errorf("decision tier: got %q, want %q (first touch should be Main)", decisions[0].Tier, TierMain)
	}
	if decisions[0].Body != "FRONTEND MAIN BODY: react components go here." {
		t.Errorf("decision body: got %q", decisions[0].Body)
	}
	if !loaded.Has("frontend") {
		t.Errorf("loaded set: frontend should be marked loaded after Main fired")
	}
}

// TestApplySelection_SecondTouchMini verifies that re-selecting a
// section that already had its Main fire injects the Mini body
// instead, without re-marking the section. This is the spec's
// "subsequent touch → Mini" rule.
func TestApplySelection_SecondTouchMini(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	// First touch — Main.
	ApplySelection([]string{"frontend"}, false, reg, loaded, tr, "first task")

	// Second touch — Mini.
	decisions := ApplySelection([]string{"frontend"}, false, reg, loaded, tr, "second task")

	if len(decisions) != 1 {
		t.Fatalf("decisions: got %d, want 1", len(decisions))
	}
	if decisions[0].Tier != TierMini {
		t.Errorf("second-touch tier: got %q, want %q (re-selection should be Mini)", decisions[0].Tier, TierMini)
	}
	if decisions[0].Body != "FRONTEND MINI: keep components small." {
		t.Errorf("second-touch body: got %q", decisions[0].Body)
	}
	// Loaded set should still only contain frontend, and Mark is idempotent.
	if !loaded.Has("frontend") {
		t.Errorf("loaded set: frontend should still be marked loaded")
	}
	got := loaded.Sections()
	if len(got) != 1 || got[0] != "frontend" {
		t.Errorf("loaded.Sections: got %v, want [frontend]", got)
	}
}

// TestApplySelection_NoSelection verifies that calling
// ApplySelection with no sections still records a system entry
// (so the observability layer can see "Coder picked 0 sections
// this turn") but doesn't touch the loaded set or emit any
// decision bodies. The decisions slice is empty, not nil
// (callers can range over it safely).
func TestApplySelection_NoSelection(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	decisions := ApplySelection(nil, false, reg, loaded, tr, "planning turn")
	if len(decisions) != 0 {
		t.Errorf("decisions: got %d, want 0 (no sections → no decisions)", len(decisions))
	}
	if loaded.Has("frontend") || loaded.Has("backend") || loaded.Has("db") {
		t.Errorf("loaded set: should not be touched by a no-selection ApplySelection")
	}

	// And the system entry was written.
	entries := tr.Entries()
	if len(entries) != 1 {
		t.Fatalf("transcript entries: got %d, want 1 (the [Skills] system entry)", len(entries))
	}
	if !strings.Contains(entries[0].Content, "[Skills]") {
		t.Errorf("entry should be a [Skills] system entry, got %q", entries[0].Content)
	}
}

// --- 2.9: multi-domain within cap ----------------------------------------

// TestApplySelection_MultiDomainMains verifies that a 3-domain
// selection (at the cap) loads Main for all three sections
// independently on the first touch. Each section's Main fires
// exactly once; the loaded set ends up with all three.
func TestApplySelection_MultiDomainMains(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	decisions := ApplySelection(
		[]string{"backend", "db", "frontend"}, // unsorted, in declaration order
		false, reg, loaded, tr, "build the booking flow",
	)

	if len(decisions) != 3 {
		t.Fatalf("decisions: got %d, want 3", len(decisions))
	}
	for _, d := range decisions {
		if d.Tier != TierMain {
			t.Errorf("section %s: tier got %q, want %q (all three are first touch)", d.Section, d.Tier, TierMain)
		}
	}

	// Verify each Main is unique (no shared body, no merge).
	bodies := make(map[string]bool)
	for _, d := range decisions {
		if bodies[d.Body] {
			t.Errorf("duplicate body across decisions: %q", d.Body)
		}
		bodies[d.Body] = true
	}

	// All three should now be loaded.
	for _, sec := range []string{"backend", "db", "frontend"} {
		if !loaded.Has(sec) {
			t.Errorf("loaded set: %q should be marked loaded after first touch", sec)
		}
	}
}

// TestApplySelection_MultiDomainMixed verifies the "stacking"
// behavior: one section fresh + one re-selected + one at cap, in
// the same selection. The fresh one gets Main, the re-selected
// one gets Mini, all three are recorded independently.
func TestApplySelection_MultiDomainMixed(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	// First touch: backend only.
	ApplySelection([]string{"backend"}, false, reg, loaded, tr, "first task")

	// Second touch: all three (backend already loaded → Mini; db + frontend fresh → Main).
	decisions := ApplySelection(
		[]string{"backend", "db", "frontend"},
		false, reg, loaded, tr, "second task",
	)

	gotBySection := make(map[string]Tier)
	for _, d := range decisions {
		gotBySection[d.Section] = d.Tier
	}

	if gotBySection["backend"] != TierMini {
		t.Errorf("backend tier: got %q, want %q (already loaded)", gotBySection["backend"], TierMini)
	}
	if gotBySection["db"] != TierMain {
		t.Errorf("db tier: got %q, want %q (first touch)", gotBySection["db"], TierMain)
	}
	if gotBySection["frontend"] != TierMain {
		t.Errorf("frontend tier: got %q, want %q (first touch)", gotBySection["frontend"], TierMain)
	}
}

// --- 2.10: cap enforcement -----------------------------------------------

// TestParseSelection_CapTruncates verifies that a 4+ section
// selection is silently truncated to 3 (the spec's hard cap) and
// that the truncated flag is set so the system entry can record
// it. The cap is enforced inside ParseSelection, so ApplySelection
// never sees the overflow.
func TestParseSelection_CapTruncates(t *testing.T) {
	// Use a 4-section registry so we can attempt a 4-section
	// selection. The Mini variant isn't needed for this test.
	dir := t.TempDir()
	for _, sec := range []string{"alpha", "beta", "gamma", "delta"} {
		writeSkillFile(t, dir, sec, sec, sec+" description", "body of "+sec, "")
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 4 sections declared; the cap is 3.
	text := `SELECTED_SECTIONS: ["alpha", "beta", "gamma", "delta"]`
	got, remaining, truncated := ParseSelection(text, reg)

	if !truncated {
		t.Errorf("truncated: got false, want true (4 declared > 3 cap)")
	}
	if len(got) != MaxSectionsPerTurn {
		t.Errorf("len(got): got %d, want %d (cap)", len(got), MaxSectionsPerTurn)
	}
	// After sort + truncate-to-3, the kept set is
	// alpha/beta/delta (gamma is the 4th in sorted order
	// and gets dropped). This is documented behavior —
	// ParseSelection sorts before truncating so the kept
	// set is deterministic regardless of declaration order.
	want := []string{"alpha", "beta", "delta"}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("got[%d]: got %q, want %q", i, got[i], s)
		}
	}
	if remaining != "" {
		t.Errorf("remaining: got %q, want \"\" (the whole response was the selection line)", remaining)
	}
}

// TestParseSelection_ExactlyAtCap verifies that exactly 3
// sections are NOT truncated (the cap is inclusive, per spec:
// "Coder may select at most 3 sections").
func TestParseSelection_ExactlyAtCap(t *testing.T) {
	reg := threeSkillFixture(t)
	text := `SELECTED_SECTIONS: ["backend", "db", "frontend"]`
	got, _, truncated := ParseSelection(text, reg)
	if truncated {
		t.Errorf("truncated: got true, want false (3 == cap is allowed)")
	}
	if len(got) != 3 {
		t.Errorf("len(got): got %d, want 3", len(got))
	}
}

// TestApplySelection_CapTruncationRecordsNote verifies that the
// cap-truncation case writes a system entry that mentions the
// truncation, so the human can see it happened. The actual
// truncation happens in ParseSelection (which is what ParseAndApply
// calls), so this test goes through ParseAndApply end-to-end.
func TestApplySelection_CapTruncationRecordsNote(t *testing.T) {
	dir := t.TempDir()
	for _, sec := range []string{"alpha", "beta", "gamma", "delta"} {
		writeSkillFile(t, dir, sec, sec, sec+" description", "body of "+sec, "")
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := NewLoadedSet()
	tr := transcript.NewTranscript("")

	cleaned, _ := ParseAndApply(
		`SELECTED_SECTIONS: ["alpha", "beta", "gamma", "delta"]`,
		reg, loaded, tr, "task that tries to pick 4",
	)
	if cleaned != "" {
		t.Errorf("cleaned: got %q, want \"\" (response was just the selection line)", cleaned)
	}

	entries := tr.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Content, "cap-truncated") {
		t.Errorf("system entry should mention cap-truncation, got: %q", entries[0].Content)
	}
}

// --- 2.5/2.6: regression checks (no skill content leaks) ---------------

// TestBuildCoderSystemPromptExtension_NilRegistry verifies that
// with no registry attached, no skill content is added — the
// Coder config is byte-for-byte the same as if the funnel
// weren't being run. This is the regression check for "no skill
// content when not configured".
func TestBuildCoderSystemPromptExtension_NilRegistry(t *testing.T) {
	got := BuildCoderSystemPromptExtension(nil, NewLoadedSet())
	if got != "" {
		t.Errorf("nil registry: got %q, want \"\"", got)
	}

	got = BuildCoderSystemPromptExtension(&Registry{}, NewLoadedSet())
	if got != "" {
		t.Errorf("empty registry: got %q, want \"\"", got)
	}
}

// TestParseSelection_UnknownSectionDropped verifies that a
// section label Coder emits that isn't in the registry is
// silently dropped (not errored). The spec is explicit: "use
// the EXACT section labels above" and unknown labels are
// dropped. A confused Coder is better handled gracefully than
// by surfacing an error to the user.
func TestParseSelection_UnknownSectionDropped(t *testing.T) {
	reg := threeSkillFixture(t)
	text := `SELECTED_SECTIONS: ["frontend", "made-up-section", "backend"]`
	got, _, _ := ParseSelection(text, reg)
	want := []string{"backend", "frontend"} // sorted, made-up dropped
	if len(got) != len(want) {
		t.Fatalf("len(got): got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseSelection_NoPrefix verifies that responses without a
// SELECTED_SECTIONS line pass through unchanged. This is the
// common case (most Coder turns don't re-declare).
func TestParseSelection_NoPrefix(t *testing.T) {
	reg := threeSkillFixture(t)
	text := "I'll start by reading the file."
	got, remaining, truncated := ParseSelection(text, reg)
	if got != nil {
		t.Errorf("sections: got %v, want nil", got)
	}
	if remaining != text {
		t.Errorf("remaining: got %q, want unchanged %q", remaining, text)
	}
	if truncated {
		t.Errorf("truncated: got true, want false")
	}
}

// TestParseSelection_MalformedJSON verifies that a malformed
// SELECTED_SECTIONS line (e.g. unclosed bracket) is treated as
// no selection, not as an error — the response text is
// preserved verbatim so the model can self-correct.
func TestParseSelection_MalformedJSON(t *testing.T) {
	reg := threeSkillFixture(t)
	text := `SELECTED_SECTIONS: ["frontend"` // unclosed
	got, remaining, _ := ParseSelection(text, reg)
	if got != nil {
		t.Errorf("sections: got %v, want nil (malformed JSON → no selection)", got)
	}
	if remaining != text {
		t.Errorf("remaining: got %q, want unchanged %q", remaining, text)
	}
}

// TestBuildCoderSystemPromptExtension_IncludesMini verifies that after the
// mandatory selector has completed, Coder receives only the active Mini body.
func TestBuildCoderSystemPromptExtension_IncludesSections(t *testing.T) {
	reg := threeSkillFixture(t)
	loaded := NewLoadedSet()
	// Pretend frontend has already been loaded (e.g. second
	// turn of a cycle).
	loaded.Mark("frontend")

	got := BuildCoderSystemPromptExtension(reg, loaded)
	if !strings.Contains(got, "FRONTEND MINI: keep components small.") {
		t.Errorf("Stage 2 should inject frontend Mini body, got:\n%s", got)
	}
	// Mini body must not include the Main body.
	if strings.Contains(got, "FRONTEND MAIN BODY") {
		t.Errorf("Stage 2 should NOT inject Main body (Mini is for subsequent touches), got:\n%s", got)
	}
}
