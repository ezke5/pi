// Package rootfs provides safe target-root operations.
package rootfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Validate rejects the host root unless explicitly allowed. It also rejects
// relative roots and traversal components.
func Validate(root string, allowHostRoot bool) (string, error) {
	if root == "" {
		return "", fmt.Errorf("target root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve target root %q: %w", root, err)
	}
	abs = filepath.Clean(abs)
	if abs == "/" && !allowHostRoot {
		return "", fmt.Errorf("refusing to use target root %q: this may modify the Fedora/host filesystem; use --root=/path/to/crux-root or --allow-host-root only with an intentional host operation", root)
	}
	volume := filepath.VolumeName(abs)
	if volume != "" {
		return "", fmt.Errorf("target root %q is not a POSIX-style path", root)
	}
	for _, part := range strings.Split(abs, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("target root %q contains path traversal", root)
		}
	}
	return abs, nil
}

func TargetPath(root, name string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("target root is empty")
	}
	if name == "" {
		return "", fmt.Errorf("target path is empty")
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return "", fmt.Errorf("target path %q contains path traversal", name)
		}
	}
	clean := filepath.Clean("/" + strings.TrimPrefix(name, "/"))
	path := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target path %q escapes root %q", name, root)
	}
	return path, nil
}

func IsHostRoot(root string) bool {
	abs, err := filepath.Abs(root)
	return err == nil && filepath.Clean(abs) == string(os.PathSeparator)
}

const (
	defaultPasswd = "root:x:0:0:root:/root:/bin/sh\nnobody:x:65534:65534:nobody:/nonexistent:/usr/bin/nologin\n"
	defaultGroup  = "root:x:0:\ntty:x:5:\nnogroup:x:65534:\n"
)

// Bootstrap creates a minimal FHS directory skeleton and baseline /etc/passwd
// and /etc/group files inside root when missing. Existing files are preserved.
func Bootstrap(root string) error {
	if root == "" {
		return fmt.Errorf("target root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve target root %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("create target root %q: %w", abs, err)
	}
	dirs := []struct {
		rel  string
		mode os.FileMode
	}{
		{"usr/bin", 0o755},
		{"usr/lib", 0o755},
		{"usr/lib64", 0o755},
		{"etc", 0o755},
		{"dev", 0o755},
		{"proc", 0o755},
		{"sys", 0o755},
		{"tmp", os.ModeSticky | 0o777},
		{"var", 0o755},
	}
	for _, d := range dirs {
		p, err := TargetPath(abs, "/"+d.rel)
		if err != nil {
			return err
		}
		_, statErr := os.Stat(p)
		existed := statErr == nil
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("create rootfs directory %q: %w", p, err)
		}
		if !existed && d.mode != 0o755 {
			if err := os.Chmod(p, d.mode); err != nil {
				return fmt.Errorf("set rootfs directory mode %q: %w", p, err)
			}
		}
	}
	for _, file := range []struct {
		rel     string
		content string
	}{
		{"/etc/passwd", defaultPasswd},
		{"/etc/group", defaultGroup},
	} {
		p, err := TargetPath(abs, file.rel)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect %q: %w", p, err)
		}
		if err := os.WriteFile(p, []byte(file.content), 0o644); err != nil {
			return fmt.Errorf("write %q: %w", p, err)
		}
	}
	return nil
}

// EnsureSkeletonIfEmpty bootstraps root only when root does not exist yet or
// is an empty directory.
func EnsureSkeletonIfEmpty(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			if err := Bootstrap(root); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, fmt.Errorf("inspect target root %q: %w", root, err)
	}
	if len(entries) > 0 {
		return false, nil
	}
	if err := Bootstrap(root); err != nil {
		return false, err
	}
	return true, nil
}
