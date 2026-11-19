// Package footprint records package-owned filesystem entries.
package footprint

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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

// Read reads CRUX-style lines: type mode path [target].
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
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		entry := Entry{}
		if strings.HasPrefix(fields[0], "/") {
			entry.Path = fields[0]
			entry.Type = "file"
		} else if len(fields) >= 3 {
			entry.Type = fields[0]
			entry.Path = fields[2]
			if entry.Type == "symlink" && len(fields) >= 5 {
				entry.LinkTarget = fields[4]
			}
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read footprint %q: %w", path, err)
	}
	return entries, nil
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
	for _, entry := range entries {
		var line string
		switch entry.Type {
		case "dir":
			line = fmt.Sprintf("dir %04o %s", entry.Mode.Perm(), entry.Path)
		case "symlink":
			line = fmt.Sprintf("symlink %04o %s -> %s", entry.Mode.Perm(), entry.Path, entry.LinkTarget)
		default:
			line = fmt.Sprintf("file %04o %s", entry.Mode.Perm(), entry.Path)
		}
		if _, err := fmt.Fprintln(f, line); err != nil {
			return fmt.Errorf("write footprint %q: %w", path, err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close footprint %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit footprint %q: %w", path, err)
	}
	return nil
}
