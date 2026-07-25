package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFile_HappyPath(t *testing.T) {
	raw := []byte(`---
name: plan
target: coder
description: Ask Coder to produce a plan only
---

Propose a step-by-step plan for: {{args}}
`)

	cmd, err := parseFile(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Target != TargetCoder {
		t.Errorf("target: got %q, want %q", cmd.Target, TargetCoder)
	}
	if cmd.Description != "Ask Coder to produce a plan only" {
		t.Errorf("description: got %q", cmd.Description)
	}
	if !strings.Contains(cmd.Template, "{{args}}") {
		t.Errorf("template should contain {{args}}, got %q", cmd.Template)
	}
}

func TestParseFile_AllTargets(t *testing.T) {
	cases := []struct {
		raw  string
		want Target
	}{
		{"---\ntarget: coder\n---\nbody", TargetCoder},
		{"---\ntarget: reviewer\n---\nbody", TargetReviewer},
		{"---\ntarget: system\n---\nbody", TargetSystem},
		{"---\ntarget: CODER\n---\nbody", TargetCoder}, // case-insensitive
	}
	for _, tc := range cases {
		cmd, err := parseFile([]byte(tc.raw))
		if err != nil {
			t.Errorf("case %q: unexpected error: %v", tc.raw, err)
			continue
		}
		if cmd.Target != tc.want {
			t.Errorf("case %q: got %q, want %q", tc.raw, cmd.Target, tc.want)
		}
	}
}

func TestParseFile_DefaultTarget(t *testing.T) {
	raw := []byte("---\ndescription: no target specified\n---\nbody")
	cmd, err := parseFile(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Target != TargetCoder {
		t.Errorf("default target: got %q, want %q", cmd.Target, TargetCoder)
	}
}

func TestParseFile_FailureCases(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing leading delimiter", "name: x\n---\nbody\n"},
		{"unterminated frontmatter", "---\nname: x\nbody with no closing delimiter\n"},
		{"unknown target", "---\ntarget: wizard\n---\nbody\n"},
		{"empty body", "---\ntarget: coder\n---\n\n   \n"},
		{"invalid yaml", "---\ntarget: [unclosed\n---\nbody\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFile([]byte(tc.raw))
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestExpand(t *testing.T) {
	cmd := Command{Template: "Do this: {{args}} and then {{args}} again."}

	got := cmd.Expand("wash the car")
	want := "Do this: wash the car and then wash the car again."
	if got != want {
		t.Errorf("Expand: got %q, want %q", got, want)
	}
}

func TestExpand_EmptyArgs(t *testing.T) {
	cmd := Command{Template: "Do this: {{args}}"}
	got := cmd.Expand("")
	want := "Do this: "
	if got != want {
		t.Errorf("Expand empty: got %q, want %q", got, want)
	}
}

func TestExpand_NoArgsPlaceholder(t *testing.T) {
	cmd := Command{Template: "Just do it."}
	got := cmd.Expand("ignored")
	want := "Just do it."
	if got != want {
		t.Errorf("Expand no-placeholder: got %q, want %q", got, want)
	}
}

func TestLoad_Directory(t *testing.T) {
	dir := t.TempDir()

	// Write a valid command file.
	valid := []byte("---\nname: plan\ntarget: coder\ndescription: plan a thing\n---\nDo: {{args}}\n")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), valid, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Write an invalid file (should be skipped with a warning, not fail Load).
	invalid := []byte("no frontmatter at all\n")
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), invalid, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Write a non-.md file (should be ignored).
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if reg.Count() != 1 {
		t.Errorf("Count: got %d, want 1", reg.Count())
	}

	cmd, ok := reg.Get("plan")
	if !ok {
		t.Fatalf("Get(plan): not found")
	}
	if cmd.Target != TargetCoder {
		t.Errorf("plan target: got %q", cmd.Target)
	}

	// Filename-based name (no frontmatter name mismatch).
	if cmd.Name != "plan" {
		t.Errorf("plan name: got %q, want %q (from filename)", cmd.Name, "plan")
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if reg.Count() != 0 {
		t.Errorf("empty dir Count: got %d, want 0", reg.Count())
	}
}

func TestLoad_DuplicateNames(t *testing.T) {
	dir := t.TempDir()
	body := []byte("---\ntarget: coder\ndescription: dup\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Filenames differ but lowercase names collide — only one wins.
	if reg.Count() != 1 {
		t.Errorf("dup count: got %d, want 1", reg.Count())
	}
}

func TestRegistry_CaseInsensitiveGet(t *testing.T) {
	dir := t.TempDir()
	body := []byte("---\ntarget: coder\ndescription: x\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, name := range []string{"plan", "Plan", "PLAN"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("Get(%q): not found (should be case-insensitive)", name)
		}
	}
}

func TestRegistry_Names(t *testing.T) {
	dir := t.TempDir()
	body := []byte("---\ntarget: coder\n---\nbody\n")
	for _, name := range []string{"zeta", "alpha", "mu"} {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), body, 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"alpha", "mu", "zeta"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names: got %v, want %v", got, want)
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if _, ok := r.Get("anything"); ok {
		t.Error("nil Get: should return false")
	}
	if got := r.Names(); got != nil {
		t.Errorf("nil Names: got %v, want nil", got)
	}
	if got := r.Count(); got != 0 {
		t.Errorf("nil Count: got %d, want 0", got)
	}
	if got := r.List(); got != nil {
		t.Errorf("nil List: got %v, want nil", got)
	}
	if got := r.Filter("p"); got != nil {
		t.Errorf("nil Filter: got %v, want nil", got)
	}
}

func TestRegistry_Filter(t *testing.T) {
	dir := t.TempDir()
	body := []byte("---\ntarget: coder\ndescription: desc\n---\nbody\n")
	for _, name := range []string{"plan", "status", "strict", "undo"} {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), body, 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Empty prefix or "/" returns all sorted
	all := reg.Filter("")
	if len(all) != 4 || all[0].Name != "plan" || all[3].Name != "undo" {
		t.Errorf("Filter(\"\"): unexpected results %+v", all)
	}
	slashAll := reg.Filter("/")
	if len(slashAll) != 4 {
		t.Errorf("Filter(\"/\"): expected 4 results, got %d", len(slashAll))
	}

	// Prefix filtering
	pMatches := reg.Filter("p")
	if len(pMatches) != 1 || pMatches[0].Name != "plan" {
		t.Errorf("Filter(\"p\"): got %v", pMatches)
	}

	sMatches := reg.Filter("s")
	if len(sMatches) != 2 || sMatches[0].Name != "status" || sMatches[1].Name != "strict" {
		t.Errorf("Filter(\"s\"): got %v", sMatches)
	}

	strMatches := reg.Filter("/STR") // case insensitive and leading slash handling
	if len(strMatches) != 1 || strMatches[0].Name != "strict" {
		t.Errorf("Filter(\"/STR\"): got %v", strMatches)
	}

	noMatches := reg.Filter("xyz")
	if len(noMatches) != 0 {
		t.Errorf("Filter(\"xyz\"): expected 0 matches, got %d", len(noMatches))
	}
}

func TestRegistry_FilterModeSubcommands(t *testing.T) {
	dir := t.TempDir()
	body := []byte("---\ntarget: system\ndescription: mode\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, "mode.md"), body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 1. Filtering "mode" returns base command + 3 subcommands
	modeMatches := reg.Filter("mode")
	if len(modeMatches) != 4 {
		t.Fatalf("expected 4 matches for 'mode', got %d: %+v", len(modeMatches), modeMatches)
	}

	// 2. Filtering "mode " (with trailing space) returns only the 3 subcommands
	spaceMatches := reg.Filter("mode ")
	if len(spaceMatches) != 3 {
		t.Fatalf("expected 3 matches for 'mode ', got %d: %+v", len(spaceMatches), spaceMatches)
	}

	// 3. Filtering "mode g" returns "mode general"
	gMatches := reg.Filter("mode g")
	if len(gMatches) != 1 || gMatches[0].Name != "mode general" {
		t.Fatalf("expected 'mode general' for 'mode g', got %+v", gMatches)
	}
}

