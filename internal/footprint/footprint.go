// Package footprint records package-owned filesystem entries.
package footprint

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Entry struct {
	Path       string      `json:"path"`
	Type       string      `json:"type"`
	Mode       os.FileMode `json:"mode"`
	SHA256     string      `json:"sha256,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
}

func Generate(root string) ([]Entry, error) {
	var entries []Entry
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("inspect %q: %w", path, err)
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("make relative path for %q: %w", path, err)
		}
		rel = "/" + filepath.ToSlash(rel)
		entry := Entry{Path: rel, Mode: info.Mode().Perm()}
		switch {
		case info.Mode().IsDir():
			entry.Type = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			entry.Type = "symlink"
			// Normalize symlink mode to 0777 (matching CRUX pkgutils lrwxrwxrwx)
			// because symlink permission bits vary across Linux filesystems.
			entry.Mode = 0o777
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", path, err)
			}
			entry.LinkTarget = target
		default:
			entry.Type = "file"
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Read reads CRUX-style lines: type mode path [-> target], as well as legacy
// single-column /path lines.
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open footprint %q: %w", path, err)
	}
	defer f.Close()
	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry := Entry{}
		if strings.HasPrefix(line, "/") {
			entry.Path = line
			entry.Type = "file"
			entries = append(entries, entry)
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 {
			continue
		}
		entry.Type = strings.TrimSpace(parts[0])
		modeStr := strings.TrimSpace(parts[1])
		rest := strings.TrimSpace(parts[2])
		if parsedMode, err := strconv.ParseUint(modeStr, 8, 32); err == nil {
			entry.Mode = os.FileMode(parsedMode).Perm()
		}
		if entry.Type == "symlink" {
			entry.Mode = 0o777
			if linkPath, target, ok := strings.Cut(rest, " -> "); ok {
				entry.Path = strings.TrimSpace(linkPath)
				entry.LinkTarget = strings.TrimSpace(target)
			} else {
				entry.Path = rest
			}
		} else {
			entry.Path = rest
		}
		if entry.Path != "" {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read footprint %q: %w", path, err)
	}
	return entries, nil
}

// Format renders entries into CRUX-inspired footprint text.
func Format(entries []Entry) string {
	var b strings.Builder
	for _, entry := range entries {
		switch entry.Type {
		case "dir":
			fmt.Fprintf(&b, "dir %04o %s\n", entry.Mode.Perm(), entry.Path)
		case "symlink":
			fmt.Fprintf(&b, "symlink 0777 %s -> %s\n", entry.Path, entry.LinkTarget)
		default:
			fmt.Fprintf(&b, "file %04o %s\n", entry.Mode.Perm(), entry.Path)
		}
	}
	return b.String()
}

func Write(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create footprint directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create footprint %q: %w", path, err)
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	if _, err := f.WriteString(Format(entries)); err != nil {
		f.Close()
		return fmt.Errorf("write footprint %q: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync footprint %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close footprint %q: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod footprint %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit footprint %q: %w", path, err)
	}
	return nil
}
