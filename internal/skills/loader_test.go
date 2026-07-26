package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const mainHdr = "---\nname: %s\nsection: %s\ndescription: %q\ntier: main\n"

func writeFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("setup write %s: %v", name, err)
	}
}

// ---- Stage 1: happy path (1.6) -----------------------------------------

func TestLoad_HappyPath_ThreeSkills(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "React/TS UI work."
tier: main
mini_ref: frontend-mini.md
token_budget_main: 6500
token_budget_mini: 3000
---
Main frontend body.
`))
	writeFile(t, dir, "frontend-mini.md", []byte(
		`---
name: frontend
section: frontend
description: "React/TS UI work."
tier: mini
---
Mini frontend body.
`))
	writeFile(t, dir, "backend.md", []byte(
		`---
name: backend
section: backend
description: "Go server work."
tier: main
token_budget_main: 7000
---
Main backend body.
`))
	writeFile(t, dir, "db.md", []byte(
		`---
name: db
section: db
description: "Postgres schema and queries."
tier: main
mini_ref: db-mini.md
token_budget_main: 5500
token_budget_mini: 2500
---
Main db body.
`))
	writeFile(t, dir, "db-mini.md", []byte(
		`---
name: db
section: db
description: "Postgres schema and queries."
tier: mini
---
Mini db body.
`))

	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Count() != 3 {
		t.Fatalf("Count: got %d, want 3", reg.Count())
	}

	// Stage 1: section labels, sorted, no description/body leakage.
	wantSections := []string{"backend", "db", "frontend"}
	if got := reg.Sections(); !reflect.DeepEqual(got, wantSections) {
		t.Errorf("Sections: got %v, want %v", got, wantSections)
	}

	// Stage 2: full lookup by section.
	fe, ok := reg.GetBySection("frontend")
	if !ok {
		t.Fatalf("GetBySection(frontend): not found")
	}
	if fe.Name != "frontend" {
		t.Errorf("frontend.Name: got %q", fe.Name)
	}
	if fe.Description != "React/TS UI work." {
		t.Errorf("frontend.Description: got %q", fe.Description)
	}
	if fe.MainBody != "Main frontend body." {
		t.Errorf("frontend.MainBody: got %q", fe.MainBody)
	}
	if fe.MiniBody != "Mini frontend body." {
		t.Errorf("frontend.MiniBody: got %q", fe.MiniBody)
	}
	if fe.TokenBudgetMain != 6500 || fe.TokenBudgetMini != 3000 {
		t.Errorf("frontend token budgets: got main=%d mini=%d", fe.TokenBudgetMain, fe.TokenBudgetMini)
	}
	if fe.MiniRef != "frontend-mini.md" {
		t.Errorf("frontend.MiniRef: got %q", fe.MiniRef)
	}

	// Section 1:1: db must resolve by both name and section.
	dbByName, ok := reg.Get("db")
	if !ok {
		t.Fatalf("Get(db): not found")
	}
	dbBySec, _ := reg.GetBySection("db")
	if dbByName.Section != dbBySec.Section || dbByName.Name != dbBySec.Name {
		t.Errorf("Get vs GetBySection mismatch for db")
	}

	// Case-insensitive section lookup.
	if _, ok := reg.GetBySection("FRONTEND"); !ok {
		t.Errorf("GetBySection(FRONTEND): case-insensitive lookup failed")
	}
}

func TestLoad_MainWithoutMini(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "backend.md", []byte(
		`---
name: backend
section: backend
description: "Go server work."
tier: main
---
Main backend body.
`))

	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.GetBySection("backend")
	if !ok {
		t.Fatalf("GetBySection(backend): not found")
	}
	if sk.MiniBody != "" {
		t.Errorf("MiniBody: should be empty when mini_ref absent, got %q", sk.MiniBody)
	}
	if sk.MiniRef != "" {
		t.Errorf("MiniRef: should be empty when absent in frontmatter, got %q", sk.MiniRef)
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
	if got := reg.Sections(); len(got) != 0 {
		t.Errorf("empty dir Sections: got %v, want []", got)
	}
}

func TestLoad_NonExistentDir(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Errorf("Load on missing dir: expected error, got nil")
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if got := r.Sections(); got != nil {
		t.Errorf("nil Sections: got %v, want nil", got)
	}
	if _, ok := r.Get("x"); ok {
		t.Errorf("nil Get: should return false")
	}
	if _, ok := r.GetBySection("x"); ok {
		t.Errorf("nil GetBySection: should return false")
	}
	if got := r.Names(); got != nil {
		t.Errorf("nil Names: got %v, want nil", got)
	}
	if got := r.Count(); got != 0 {
		t.Errorf("nil Count: got %d, want 0", got)
	}
}

// ---- Stage 2: malformed input (1.7) ------------------------------------

func TestLoad_Malformed_YAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		"---\nname: [unclosed\ntier: main\n---\nbody\n",
	))
	_, err := Load(dir)
	if err == nil {
		t.Fatalf("malformed YAML: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "YAML") {
		t.Errorf("error should mention YAML, got: %v", err)
	}
}

func TestLoad_MissingLeadingDelimiter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		"name: frontend\nsection: frontend\ntier: main\n---\nbody\n",
	))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "leading") {
		t.Errorf("missing leading delimiter: got %v", err)
	}
}

func TestLoad_UnterminatedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		"---\nname: frontend\nsection: frontend\ntier: main\nbody with no closing\n",
	))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("unterminated frontmatter: got %v", err)
	}
}

func TestLoad_MissingSection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
description: "x"
tier: main
---
body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "section") {
		t.Errorf("missing section: got %v", err)
	}
}

func TestLoad_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
tier: main
---
body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("missing description: got %v", err)
	}
}

func TestLoad_MissingTier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
---
body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "tier") {
		t.Errorf("missing tier: got %v", err)
	}
}

func TestLoad_WrongTierOnMainFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: mini
---
body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "tier") {
		t.Errorf("wrong tier on main file: got %v", err)
	}
}

func TestLoad_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		"---\nname: frontend\nsection: frontend\ndescription: \"x\"\ntier: main\n---\n\n   \n",
	))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty body: got %v", err)
	}
}

func TestLoad_DuplicateSection(t *testing.T) {
	dir := t.TempDir()
	// Two Main files, same section — must be rejected (section:skill 1:1).
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: ui
description: "first"
tier: main
---
body one
`))
	writeFile(t, dir, "frontend-old.md", []byte(
		`---
name: frontend-old
section: ui
description: "second"
tier: main
---
body two
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate section") {
		t.Errorf("duplicate section: got %v", err)
	}
}

func TestLoad_FilenameNameMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: notfrontend
section: frontend
description: "x"
tier: main
---
body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "filename and frontmatter name") {
		t.Errorf("filename/frontmatter name mismatch: got %v", err)
	}
}

func TestLoad_MiniRefMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: main
mini_ref: frontend-mini.md
---
body
`))
	// No frontend-mini.md written.
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "mini") {
		t.Errorf("missing mini_ref target: got %v", err)
	}
}

func TestLoad_MiniRefMismatchedIdentity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: main
mini_ref: frontend-mini.md
---
body
`))
	// Mini file exists but claims a different name.
	writeFile(t, dir, "frontend-mini.md", []byte(
		`---
name: backend
section: frontend
description: "x"
tier: mini
---
mini body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("mini identity mismatch: got %v", err)
	}
}

func TestLoad_MiniWrongTier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: main
mini_ref: frontend-mini.md
---
body
`))
	writeFile(t, dir, "frontend-mini.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: main
---
mini body
`))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "tier") {
		t.Errorf("mini with tier=main: got %v", err)
	}
}

func TestLoad_MiniEmptyBody(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "frontend.md", []byte(
		`---
name: frontend
section: frontend
description: "x"
tier: main
mini_ref: frontend-mini.md
---
body
`))
	writeFile(t, dir, "frontend-mini.md", []byte(
		"---\nname: frontend\nsection: frontend\ndescription: \"x\"\ntier: mini\n---\n\n   \n",
	))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("mini with empty body: got %v", err)
	}
}

func TestLoad_SkipsStandaloneMiniFiles(t *testing.T) {
	dir := t.TempDir()
	// A -mini.md file with no matching main file should be silently
	// skipped (it's an orphan, not a parse error). It only loads when
	// referenced.
	writeFile(t, dir, "orphan-mini.md", []byte(
		`---
name: orphan
section: orphan
description: "x"
tier: mini
---
mini
`))
	reg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: orphan mini should not error, got %v", err)
	}
	if reg.Count() != 0 {
		t.Errorf("Count: got %d, want 0", reg.Count())
	}
}
