package transcript

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CleanupResult describes session artifacts archived during a retention pass.
type CleanupResult struct{ Archived []string }

// ArchivedCount returns the number of source files successfully archived.
func (r CleanupResult) ArchivedCount() int { return len(r.Archived) }

// CleanupSessions archives expired JSONL files from the main, subagent, twin,
// and trace session locations. Archives are gzip files grouped by the source
// file's modification month beneath root/archive. A source file is removed
// only after its archive has been fully written and closed successfully.
// protectedPaths are always skipped, regardless of age.
func CleanupSessions(root string, retention time.Duration, protectedPaths ...string) (CleanupResult, error) {
	if retention <= 0 {
		return CleanupResult{}, fmt.Errorf("session retention must be positive")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("resolve session root: %w", err)
	}
	protected := make(map[string]struct{}, len(protectedPaths))
	for _, path := range protectedPaths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return CleanupResult{}, fmt.Errorf("resolve protected session path: %w", err)
		}
		protected[filepath.Clean(abs)] = struct{}{}
	}

	cutoff := time.Now().Add(-retention)
	var result CleanupResult
	for _, relDir := range []string{".", "subagents", "twins", "traces"} {
		dir := filepath.Join(rootAbs, relDir)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("read session directory %q: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			if _, ok := protected[filepath.Clean(source)]; ok {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return result, fmt.Errorf("stat session artifact %q: %w", source, err)
			}
			if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			archive, err := archiveSessionFile(rootAbs, source, info.ModTime())
			if err != nil {
				return result, err
			}
			if err := os.Remove(source); err != nil {
				return result, fmt.Errorf("remove archived session artifact %q: %w", source, err)
			}
			result.Archived = append(result.Archived, archive)
		}
	}
	return result, nil
}

func archiveSessionFile(root, source string, modTime time.Time) (string, error) {
	rel, err := filepath.Rel(root, source)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("session artifact %q is outside %q", source, root)
	}
	archive := filepath.Join(root, "archive", modTime.Format("2006-01"), rel+".gz")
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		return "", fmt.Errorf("create session archive directory: %w", err)
	}
	in, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open session artifact %q: %w", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return "", fmt.Errorf("create session archive %q: %w", archive, err)
	}
	gz := gzip.NewWriter(out)
	_, copyErr := io.Copy(gz, in)
	closeGzipErr := gz.Close()
	closeFileErr := out.Close()
	if copyErr != nil || closeGzipErr != nil || closeFileErr != nil {
		_ = os.Remove(archive)
		if copyErr != nil {
			return "", fmt.Errorf("compress session artifact %q: %w", source, copyErr)
		}
		if closeGzipErr != nil {
			return "", fmt.Errorf("finish session archive %q: %w", archive, closeGzipErr)
		}
		return "", fmt.Errorf("close session archive %q: %w", archive, closeFileErr)
	}
	return archive, nil
}
