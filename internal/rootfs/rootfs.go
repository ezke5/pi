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
