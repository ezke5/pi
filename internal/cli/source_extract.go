package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func isSourceArchive(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.zst", ".tar.bz2", ".tar", ".zip"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func extractSourceArchive(path, dest string) error {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		return extractZip(path, dest)
	}
	list, err := exec.Command("tar", "-tf", path).Output()
	if err != nil {
		return fmt.Errorf("list source archive %q: %w", path, err)
	}
	for _, name := range strings.Split(strings.TrimSpace(string(list)), "\n") {
		if name == "" {
			continue
		}
		if _, err := safeSourcePath(dest, name); err != nil {
			return fmt.Errorf("unsafe path %q in source archive %q: %w", name, path, err)
		}
	}
	verbose, err := exec.Command("tar", "-tvf", path).Output()
	if err != nil {
		return fmt.Errorf("inspect source archive %q: %w", path, err)
	}
	for _, line := range strings.Split(string(verbose), "\n") {
		if !strings.Contains(line, " -> ") {
			continue
		}
		parts := strings.SplitN(line, " -> ", 2)
		name := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if _, err := safeSourcePath(dest, name); err != nil {
			return fmt.Errorf("unsafe symlink %q in source archive %q: %w", name, path, err)
		}
		linkPath, _ := safeSourcePath(dest, name)
		if _, err := safeSourceSymlink(dest, linkPath, target); err != nil {
			return fmt.Errorf("unsafe symlink target %q in source archive %q: %w", target, path, err)
		}
	}
	if err := exec.Command("tar", "-xf", path, "-C", dest).Run(); err != nil {
		return fmt.Errorf("extract source archive %q: %w", path, err)
	}
	return nil
}

func safeSourcePath(root, name string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	name = filepath.FromSlash(name)
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute path")
	}
	dest := filepath.Join(root, name)
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes destination")
	}
	return dest, nil
}

func safeSourceSymlink(root, path, target string) (string, error) {
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute target")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target escapes destination")
	}
	return target, nil
}

func extractZip(path, dest string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open source archive %q: %w", path, err)
	}
	defer r.Close()
	for _, file := range r.File {
		target, err := safeSourcePath(dest, file.Name)
		if err != nil {
			return fmt.Errorf("unsafe path %q in source archive %q: %w", file.Name, path, err)
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			in, err := file.Open()
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(in)
			in.Close()
			if readErr != nil {
				return readErr
			}
			if _, err := safeSourceSymlink(dest, target, string(data)); err != nil {
				return fmt.Errorf("unsafe symlink target in source archive %q: %w", path, err)
			}
			continue
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, file.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
