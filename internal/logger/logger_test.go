package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingWriterRotatesDuringSessionWithoutLosingWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "triad.log")
	w, err := newRotatingWriter(path, Options{MaxBytes: 10, MaxBackups: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for _, entry := range []string{"first\n", "second\n", "third\n"} {
		if _, err := w.Write([]byte(entry)); err != nil {
			t.Fatalf("Write(%q): %v", entry, err)
		}
	}

	if got := readLogFile(t, path); got != "third\n" {
		t.Errorf("active log = %q, want third entry", got)
	}
	if got := readLogFile(t, path+".1"); got != "second\n" {
		t.Errorf("first backup = %q, want second entry", got)
	}
	if got := readLogFile(t, path+".2"); got != "first\n" {
		t.Errorf("second backup = %q, want first entry", got)
	}
}

func TestRotatingWriterPrunesOldestBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "triad.log")
	w, err := newRotatingWriter(path, Options{MaxBytes: 10, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for _, entry := range []string{"one---\n", "two---\n", "three-\n", "four--\n"} {
		if _, err := w.Write([]byte(entry)); err != nil {
			t.Fatalf("Write(%q): %v", entry, err)
		}
	}

	if got := readLogFile(t, path); got != "four--\n" {
		t.Errorf("active log = %q, want fourth entry", got)
	}
	if got := readLogFile(t, path+".1"); got != "three-\n" {
		t.Errorf("first backup = %q, want third entry", got)
	}
	if got := readLogFile(t, path+".2"); got != "two---\n" {
		t.Errorf("second backup = %q, want second entry", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("backup beyond retention cap exists; stat error = %v", err)
	}
}

func TestRotatingWriterRotatesOversizedLogAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "triad.log")
	if err := os.WriteFile(path, []byte("existing log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	w, err := newRotatingWriter(path, Options{MaxBytes: 10, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if got := readLogFile(t, path+".1"); got != "existing log\n" {
		t.Errorf("startup backup = %q, want existing content", got)
	}
	if got := readLogFile(t, path); got != "" {
		t.Errorf("active log after startup rotation = %q, want empty", got)
	}
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
