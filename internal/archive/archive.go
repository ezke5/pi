// Package archive creates and extracts .pkg.tar.zst archives.
package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ezke5/pi/internal/footprint"
)

func safeArchivePath(root, name string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve archive destination root: %w", err)
	}
	dest := filepath.Join(rootAbs, name)
	rel, err := filepath.Rel(rootAbs, dest)
	if err != nil {
		return "", fmt.Errorf("compare archive path to destination root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes destination root")
	}
	return dest, nil
}

func safeSymlinkTarget(root, symlinkPath, target string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve destination root: %w", err)
	}
	var resolved string
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute symlink targets are not allowed")
	} else {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(symlinkPath), target))
	}
	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil {
		return "", fmt.Errorf("compare symlink target to destination root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target escapes destination root")
	}
	if filepath.IsAbs(target) {
		return strings.TrimPrefix(filepath.Clean(target), string(filepath.Separator)), nil
	}
	return target, nil
}

func Create(pkgDir, output, pkgname, pkgver, pkgrel string, entries []footprint.Entry) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create package cache directory: %w", err)
	}
	tarFile, err := os.CreateTemp(filepath.Dir(output), ".pi-package-*.tar")
	if err != nil {
		return fmt.Errorf("create temporary tar: %w", err)
	}
	tarName := tarFile.Name()
	defer os.Remove(tarName)
	tw := tar.NewWriter(tarFile)
	metadata := fmt.Sprintf("pkgname = %s\npkgver = %s\npkgrel = %s\n", pkgname, pkgver, pkgrel)
	hdr := &tar.Header{Name: ".PKGINFO", Mode: 0o644, Size: int64(len(metadata)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write .PKGINFO header: %w", err)
	}
	if _, err := io.WriteString(tw, metadata); err != nil {
		return fmt.Errorf("write .PKGINFO: %w", err)
	}

	err = filepath.Walk(pkgDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect package staging path %q: %w", path, walkErr)
		}
		if path == pkgDir {
			return nil
		}
		rel, err := filepath.Rel(pkgDir, path)
		if err != nil {
			return fmt.Errorf("make package-relative path: %w", err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(rel), "./")
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("create tar header for %q: %w", path, err)
		}
		hdr.Name = name
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read symlink %q: %w", path, err)
			}
			hdr.Linkname = target
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write tar header for %q: %w", path, err)
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open package file %q: %w", path, err)
			}
			_, copyErr := io.Copy(tw, f)
			closeErr := f.Close()
			if copyErr != nil {
				return fmt.Errorf("archive package file %q: %w", path, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close package file %q: %w", path, closeErr)
			}
		}
		return nil
	})
	if err != nil {
		tw.Close()
		tarFile.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := tarFile.Close(); err != nil {
		return fmt.Errorf("close temporary tar: %w", err)
	}
	cmd := exec.Command("zstd", "-q", "-f", "-o", output, tarName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compress package with zstd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Extract(output string, archivePath, destRoot string) error {
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("open package archive %q: %w", archivePath, err)
	}

	temp, err := os.CreateTemp(filepath.Dir(archivePath), ".pi-extract-*.tar")
	if err != nil {
		return fmt.Errorf("create temporary tar for package extraction: %w", err)
	}
	tempName := temp.Name()
	temp.Close()
	defer os.Remove(tempName)
	if out, err := exec.Command("zstd", "-q", "-d", "-f", "-o", tempName, archivePath).CombinedOutput(); err != nil {
		return fmt.Errorf("decompress package archive %q: %w (%s)", archivePath, err, strings.TrimSpace(string(out)))
	}
	f, err := os.Open(tempName)
	if err != nil {
		return fmt.Errorf("open decompressed package archive %q: %w", tempName, err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read package archive %q: %w", archivePath, err)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || name == "" {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q in package archive %q", hdr.Name, archivePath)
		}
		dest, err := safeArchivePath(destRoot, name)
		if err != nil {
			return fmt.Errorf("unsafe path %q in package archive %q: %w", hdr.Name, archivePath, err)
		}
		if strings.HasPrefix(filepath.ToSlash(name), "etc/") {
			if _, err := os.Lstat(dest); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("inspect existing config path %q: %w", dest, err)
			}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("create package directory %q: %w", dest, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("create package symlink directory %q: %w", filepath.Dir(dest), err)
			}
			if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("replace package symlink %q: %w", dest, err)
			}
			safeTarget, err := safeSymlinkTarget(destRoot, dest, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("unsafe symlink target %q for %q in package archive %q: %w", hdr.Linkname, hdr.Name, archivePath, err)
			}
			if err := os.Symlink(safeTarget, dest); err != nil {
				return fmt.Errorf("create package symlink %q: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("create package file directory %q: %w", filepath.Dir(dest), err)
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create package file %q: %w", dest, err)
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("extract package file %q: %w", dest, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close package file %q: %w", dest, closeErr)
			}
		}
	}
	_ = output
	return nil
}

// Entries returns the filesystem entries recorded in an archive without
// writing them to the target root.
func Entries(archivePath string) ([]footprint.Entry, error) {
	temp, err := os.CreateTemp(filepath.Dir(archivePath), ".pi-inspect-*.tar")
	if err != nil {
		return nil, err
	}
	tempName := temp.Name()
	if err := temp.Close(); err != nil {
		return nil, err
	}
	defer os.Remove(tempName)
	if out, err := exec.Command("zstd", "-q", "-d", "-f", "-o", tempName, archivePath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("decompress package archive %q: %w (%s)", archivePath, err, strings.TrimSpace(string(out)))
	}
	f, err := os.Open(tempName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var entries []footprint.Entry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if name == ".PKGINFO" || name == "." || name == "" || strings.HasPrefix(name, "var/lib/pi/packages/") {
			continue
		}
		entry := footprint.Entry{Path: "/" + strings.TrimPrefix(name, "/"), Mode: os.FileMode(hdr.Mode)}
		switch hdr.Typeflag {
		case tar.TypeDir:
			entry.Type = "dir"
		case tar.TypeSymlink:
			entry.Type, entry.LinkTarget = "symlink", hdr.Linkname
		default:
			entry.Type = "file"
		}
		entries = append(entries, entry)
	}
}
