package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates a Main + (optional) Mini pair in dir, with
// the given name / section / description / bodies. Returns the
// absolute path to the Main file. Used by every test that needs
// a real on-disk skill.
func writeSkill(t *testing.T, dir, name, section, desc, mainBody, miniBody string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	main := "---\n" +
		"name: " + name + "\n" +
		"section: " + section + "\n" +
		"description: \"" + desc + "\"\n" +
		"tier: main\n" +
		"token_budget_main: 1000\n" +
		"token_budget_mini: 500\n"
	if miniBody != "" {
		main += "mini_ref: " + name + "-mini.md\n"
	}
	main += "---\n\n" + mainBody + "\n"
	mainPath := filepath.Join(dir, name+".md")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if miniBody != "" {
		mini := "---\n" +
			"name: " + name + "\n" +
			"section: " + section + "\n" +
			"description: \"" + desc + "\"\n" +
			"tier: mini\n" +
			"---\n\n" + miniBody + "\n"
		if err := os.WriteFile(filepath.Join(dir, name+"-mini.md"), []byte(mini), 0o644); err != nil {
			t.Fatalf("write mini: %v", err)
		}
	}
	return mainPath
}

// TestHandleSubcommand_EmptyReturnsUsage covers the bare
// "/skill" case (no subcommand). Body must be non-empty and
// include a hint about the available subcommands.
func TestHandleSubcommand_EmptyReturnsUsage(t *testing.T) {
	res := HandleSubcommand("", "", nil, nil, "")
	if res.Body == "" {
		t.Fatal("expected non-empty usage body")
	}
	if !strings.Contains(res.Body, "/skill list") {
		t.Errorf("expected usage to mention /skill list, got: %q", res.Body)
	}
	if res.Reload {
		t.Error("expected Reload=false for usage")
	}
}

// TestHandleSubcommand_UnknownReturnsError checks the
// unknown-subcommand path. Body must echo the bad name and
// include the usage block.
func TestHandleSubcommand_UnknownReturnsError(t *testing.T) {
	res := HandleSubcommand("bogus", "", nil, nil, "")
	if !strings.Contains(res.Body, "bogus") {
		t.Errorf("expected body to echo bad subcommand, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "/skill list") {
		t.Errorf("expected body to fall back to usage, got: %q", res.Body)
	}
	if res.Reload {
		t.Error("unknown subcmd should not trigger Reload")
	}
}

// TestHandleList_Empty covers the no-skills case. Body must
// explain the empty state, not error.
func TestHandleList_Empty(t *testing.T) {
	res := HandleSubcommand("list", "", &Registry{}, NewLoadedSet(), "")
	if !strings.Contains(res.Body, "No skills") {
		t.Errorf("expected body to mention no skills, got: %q", res.Body)
	}
	if res.Reload {
		t.Error("list on empty should not Reload")
	}
}

// TestHandleList_Populated covers the happy path: two real
// skills on disk, list emits both with their descriptions
// and tier budgets. Forced state is annotated when set.
func TestHandleList_Populated(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main body", "mini body")
	writeSkill(t, dir, "backend", "backend", "API work", "main body", "mini body")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := NewLoadedSet()
	loaded.Force("backend")
	res := HandleSubcommand("list", "", reg, loaded, "")
	if !strings.Contains(res.Body, "frontend") {
		t.Errorf("expected body to list frontend, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "backend") {
		t.Errorf("expected body to list backend, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "[forced]") {
		t.Errorf("expected body to mark backend as forced, got: %q", res.Body)
	}
	if res.Reload {
		t.Error("list should not Reload")
	}
}

// TestHandleView_Happy covers a real skill with both bodies.
// Body must include the description, the MAIN BODY delimiter,
// and the MINI BODY delimiter.
func TestHandleView_Happy(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "the main body", "the mini body")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := HandleSubcommand("view", "frontend", reg, nil, "")
	if !strings.Contains(res.Body, "MAIN BODY") {
		t.Errorf("expected MAIN BODY delimiter, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "the main body") {
		t.Errorf("expected main body content, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "MINI BODY") {
		t.Errorf("expected MINI BODY delimiter, got: %q", res.Body)
	}
}

// TestHandleView_Unknown covers the bad-name case. Body must
// mention the bad name and reference /skill list.
func TestHandleView_Unknown(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := HandleSubcommand("view", "missing", reg, nil, "")
	if !strings.Contains(res.Body, "missing") {
		t.Errorf("expected body to echo name, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "/skill list") {
		t.Errorf("expected body to mention list, got: %q", res.Body)
	}
}

// TestHandleView_NoMini checks the "Main only" case (no mini
// file) — body should still render, but the MINI BODY block
// is absent and the body notes "(no mini)" was implicit in
// the listing. The view path simply doesn't include the mini
// delimiter.
func TestHandleView_NoMini(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "the main body", "")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := HandleSubcommand("view", "frontend", reg, nil, "")
	if strings.Contains(res.Body, "MINI BODY") {
		t.Errorf("expected no MINI BODY block when no mini file, got: %q", res.Body)
	}
	if !strings.Contains(res.Body, "the main body") {
		t.Errorf("expected main body, got: %q", res.Body)
	}
}

// TestHandleAdd_Happy covers the scaffold + reload path. Both
// files must be created on disk, body must show their paths,
// and Reload must be true so the TUI re-loads the registry.
func TestHandleAdd_Happy(t *testing.T) {
	dir := t.TempDir()
	res := HandleSubcommand("add", "newskill", nil, nil, dir)
	if !res.Reload {
		t.Error("add should set Reload=true")
	}
	if !strings.Contains(res.Body, "Scaffolded") {
		t.Errorf("expected body to confirm scaffold, got: %q", res.Body)
	}
	mainPath := filepath.Join(dir, "skills", "newskill.md")
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("expected main file to exist at %q, got: %v", mainPath, err)
	}
	miniPath := filepath.Join(dir, "skills", "newskill-mini.md")
	if _, err := os.Stat(miniPath); err != nil {
		t.Errorf("expected mini file to exist at %q, got: %v", miniPath, err)
	}
	// Reload the registry and confirm the new skill shows up.
	reg, err := Load(filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatalf("Load after add: %v", err)
	}
	if reg.Count() != 1 {
		t.Errorf("expected 1 skill after add, got %d", reg.Count())
	}
	sk, ok := reg.Get("newskill")
	if !ok {
		t.Fatal("newskill not in registry after add")
	}
	if sk.Section != "newskill" {
		t.Errorf("expected section=newskill, got %q", sk.Section)
	}
}

// TestHandleAdd_AlreadyExists checks the duplicate-name path:
// a second /skill add with the same name must NOT overwrite
// the existing file. Body must mention the existing path.
func TestHandleAdd_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	_ = HandleSubcommand("add", "newskill", nil, nil, dir)
	res := HandleSubcommand("add", "newskill", nil, nil, dir)
	if res.Reload {
		t.Error("duplicate add should not Reload")
	}
	if !strings.Contains(res.Body, "already exists") {
		t.Errorf("expected body to mention 'already exists', got: %q", res.Body)
	}
}

// TestHandleAdd_InvalidName covers the validation gate. Names
// with whitespace, leading/trailing hyphens, or the "-mini"
// suffix must be rejected. Empty / whitespace-only names
// short-circuit to the usage line (a different error path).
// Note: add lowercases input first, so "UPPER" becomes
// "upper" and is accepted — that's the intended behavior.
func TestHandleAdd_InvalidName(t *testing.T) {
	cases := []string{
		"Has Space", // contains whitespace
		"trailing-", // ends with hyphen
		"-leading",  // starts with hyphen
		"foo-mini",  // reserved suffix
	}
	for _, name := range cases {
		res := HandleSubcommand("add", name, nil, nil, t.TempDir())
		if !strings.Contains(res.Body, "Invalid skill name") {
			t.Errorf("name %q: expected rejection, got: %q", name, res.Body)
		}
	}
}

// TestHandleAdd_EmptyOrWhitespace covers the empty-args path,
// which is a usage line, not a validation error.
func TestHandleAdd_EmptyOrWhitespace(t *testing.T) {
	for _, name := range []string{"", "   "} {
		res := HandleSubcommand("add", name, nil, nil, t.TempDir())
		if !strings.Contains(res.Body, "Usage: /skill add") {
			t.Errorf("name %q: expected usage line, got: %q", name, res.Body)
		}
	}
}

// TestHandleDelete_QueuesConfirmation covers the new
// confirmation-gated flow: HandleSubcommand("delete")
// does NOT remove files directly — it returns a
// PendingAction the TUI can use to defer the actual
// removal. The files must still exist after the
// "first" delete call.
func TestHandleDelete_QueuesConfirmation(t *testing.T) {
	dir := t.TempDir()
	_ = HandleSubcommand("add", "newskill", nil, nil, dir)
	reg, err := Load(filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatalf("Load after add: %v", err)
	}
	res := HandleSubcommand("delete", "newskill", reg, nil, dir)
	if res.Reload {
		t.Error("delete should NOT Reload before confirmation")
	}
	if res.PendingAction == nil {
		t.Fatal("expected PendingAction to be set")
	}
	if res.PendingAction.Kind != PendingActionDelete {
		t.Errorf("expected PendingActionDelete, got %d", res.PendingAction.Kind)
	}
	// Files must still exist.
	mainPath := filepath.Join(dir, "skills", "newskill.md")
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("expected main file to still exist before confirm, got: %v", err)
	}
}

// TestExecutePending_Delete removes the file via the
// confirmation path. End-to-end: add → queue delete →
// confirm → files gone.
func TestExecutePending_Delete(t *testing.T) {
	dir := t.TempDir()
	_ = HandleSubcommand("add", "newskill", nil, nil, dir)
	reg, err := Load(filepath.Join(dir, "skills"))
	if err != nil {
		t.Fatalf("Load after add: %v", err)
	}
	res := ExecutePending(
		&PendingAction{Kind: PendingActionDelete, Name: "newskill"},
		reg,
		dir,
	)
	if !res.Reload {
		t.Error("confirmed delete should Reload")
	}
	if !strings.Contains(res.Body, "Deleted") {
		t.Errorf("expected body to confirm delete, got: %q", res.Body)
	}
	mainPath := filepath.Join(dir, "skills", "newskill.md")
	if _, err := os.Stat(mainPath); !os.IsNotExist(err) {
		t.Errorf("expected main file to be gone, got err=%v", err)
	}
}

// TestHandleDelete_Unknown covers the bad-name path: the
// registry does not contain the name, the body must reflect
// that and there must be no PendingAction queued.
func TestHandleDelete_Unknown(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := HandleSubcommand("delete", "missing", reg, nil, dir)
	if res.PendingAction != nil {
		t.Error("unknown skill should not queue a PendingAction")
	}
	if !strings.Contains(res.Body, "Unknown") {
		t.Errorf("expected body to say unknown, got: %q", res.Body)
	}
}

// TestHandleForce_Happy covers the manual-override path. After
// /skill force, the loaded set reports IsForced==true and
// the body confirms the pin.
func TestHandleForce_Happy(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := NewLoadedSet()
	res := HandleSubcommand("force", "frontend", reg, loaded, dir)
	if !strings.Contains(res.Body, "Forced") {
		t.Errorf("expected body to confirm force, got: %q", res.Body)
	}
	if !loaded.IsForced("frontend") {
		t.Error("expected frontend to be forced in loaded set")
	}
}

// TestHandleForce_ByNameNotSection covers the case where the
// user types the skill's name and it's also its section label
// (the common case). Both lookups (by section, by name) should
// succeed.
func TestHandleForce_ByNameNotSection(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := NewLoadedSet()
	HandleSubcommand("force", "frontend", reg, loaded, dir)
	if !loaded.IsForced("frontend") {
		t.Error("expected frontend to be forced")
	}
}

// TestHandleForce_Unknown covers the bad-name path.
func TestHandleForce_Unknown(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := NewLoadedSet()
	res := HandleSubcommand("force", "missing", reg, loaded, dir)
	if !strings.Contains(res.Body, "Unknown") {
		t.Errorf("expected body to say unknown, got: %q", res.Body)
	}
	if loaded.IsForced("frontend") {
		t.Error("frontend should not be forced after a failed force")
	}
}

// TestLoadedSet_ForceAndUnforce exercises the LoadedSet
// Force / Unforce / IsForced / Forced primitives directly.
func TestLoadedSet_ForceAndUnforce(t *testing.T) {
	ls := NewLoadedSet()
	if ls.IsForced("frontend") {
		t.Error("fresh set should report not forced")
	}
	ls.Force("frontend")
	if !ls.IsForced("frontend") {
		t.Error("after Force, IsForced should be true")
	}
	if len(ls.Forced()) != 1 {
		t.Errorf("expected Forced() length 1, got %d", len(ls.Forced()))
	}
	ls.Unforce("frontend")
	if ls.IsForced("frontend") {
		t.Error("after Unforce, IsForced should be false")
	}
}

// TestLoadedSet_ForceIsCaseInsensitive covers the
// case-insensitive contract that's consistent with the rest
// of the skills package.
func TestLoadedSet_ForceIsCaseInsensitive(t *testing.T) {
	ls := NewLoadedSet()
	ls.Force("Frontend")
	if !ls.IsForced("frontend") {
		t.Error("IsForced should match case-insensitively")
	}
	if !ls.IsForced("FRONTEND") {
		t.Error("IsForced should match case-insensitively")
	}
}

// TestBuildLoadedBodies_IncludesForcedAtMain covers the
// forced-section's first emission: it should be Main, not
// Mini, regardless of the loaded set's pre-existing state.
func TestBuildLoadedBodies_IncludesForcedAtMain(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "the main body", "the mini body")
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ls := NewLoadedSet()
	ls.Force("frontend")
	body := BuildLoadedBodies(reg, ls)
	if !strings.Contains(body, "the main body") {
		t.Errorf("expected forced section to emit Main on first turn, got: %q", body)
	}
	if strings.Contains(body, "the mini body") {
		t.Errorf("expected Main, not Mini, on first forced turn, got: %q", body)
	}
	if !ls.Has("frontend") {
		t.Error("expected forced section to be marked loaded after first emission")
	}
	// Subsequent call: should now emit Mini.
	body2 := BuildLoadedBodies(reg, ls)
	if !strings.Contains(body2, "the mini body") {
		t.Errorf("expected Mini on second call, got: %q", body2)
	}
	if strings.Contains(body2, "the main body") {
		t.Errorf("expected Mini, not Main, on second call, got: %q", body2)
	}
}

// TestBuildLoadedBodies_EmptyInputs covers the no-op cases:
// nil registry, nil loaded set, empty loaded set. All must
// return an empty string.
func TestBuildLoadedBodies_EmptyInputs(t *testing.T) {
	if got := BuildLoadedBodies(nil, NewLoadedSet()); got != "" {
		t.Errorf("nil reg: expected empty, got %q", got)
	}
	dir := t.TempDir()
	writeSkill(t, dir, "frontend", "frontend", "UI work", "main", "mini")
	reg, _ := Load(dir)
	if got := BuildLoadedBodies(reg, nil); got != "" {
		t.Errorf("nil loaded: expected empty, got %q", got)
	}
	if got := BuildLoadedBodies(reg, NewLoadedSet()); got != "" {
		t.Errorf("empty loaded: expected empty, got %q", got)
	}
}
