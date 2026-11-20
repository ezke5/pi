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
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute symlink targets are not allowed")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(symlinkPath), target))
	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil {
		return "", fmt.Errorf("compare symlink target to destination root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target escapes destination root")
	}
	return target, nil
}

func suggestRelativeSymlinkTarget(rootAbs, symlinkAbs, target string) (string, bool) {
	if !filepath.IsAbs(target) {
		return "", false
	}
	clean := filepath.Clean(target)
	inRoot := filepath.Join(rootAbs, strings.TrimPrefix(clean, string(filepath.Separator)))
	rel, err := filepath.Rel(filepath.Dir(symlinkAbs), inRoot)
	if err != nil {
		return "", false
	}
	suggestion := filepath.ToSlash(rel)
	if _, err := safeSymlinkTarget(rootAbs, symlinkAbs, suggestion); err != nil {
		return "", false
	}
	return suggestion, true
}

// ValidateSymlinks validates every symlink in a package staging directory using
// the exact same relative-symlink safety rules enforced during extraction.
func ValidateSymlinks(pkgDir string) error {
	rootAbs, err := filepath.Abs(pkgDir)
	if err != nil {
		return fmt.Errorf("resolve package staging root: %w", err)
	}
	return filepath.Walk(rootAbs, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect package staging path %q: %w", path, walkErr)
		}
		if path == rootAbs || info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return fmt.Errorf("make package-relative path: %w", err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(rel), "./")
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read symlink %q: %w", path, err)
		}
		if _, err := safeSymlinkTarget(rootAbs, path, target); err != nil {
			if suggestion, ok := suggestRelativeSymlinkTarget(rootAbs, path, target); ok {
				return fmt.Errorf("unsafe symlink target %q for %q: %w; use a relative target such as %q", target, name, err, suggestion)
			}
			return fmt.Errorf("unsafe symlink target %q for %q: %w; use a relative target within the package root", target, name, err)
		}
		return nil
	})
}

func Create(pkgDir, output, pkgname, pkgver, pkgrel string, entries []footprint.Entry, backupLists ...[]string) error {
	return CreateWithUSE(pkgDir, output, pkgname, pkgver, pkgrel, "", nil, nil, "", entries, backupLists...)
}

// CreateWithUSE creates an archive and records architecture and effective USE
// metadata in .PKGINFO. The legacy Create function remains source-compatible.
func CreateWithUSE(pkgDir, output, pkgname, pkgver, pkgrel, arch string, useFlags, useDisabled []string, useHash string, entries []footprint.Entry, backupLists ...[]string) error {
	if err := ValidateSymlinks(pkgDir); err != nil {
		return err
	}
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
	if arch != "" {
		metadata += fmt.Sprintf("arch = %s\n", arch)
	}
	if len(useFlags) > 0 {
		metadata += fmt.Sprintf("use = %s\n", strings.Join(useFlags, " "))
	}
	if len(useDisabled) > 0 {
		metadata += fmt.Sprintf("use_disabled = %s\n", strings.Join(useDisabled, " "))
	}
	if useHash != "" {
		metadata += fmt.Sprintf("use_hash = %s\n", useHash)
	}
	if len(backupLists) > 0 {
		for _, backup := range backupLists[0] {
			metadata += fmt.Sprintf("backup = %s\n", backup)
		}
	}
	hdr := &tar.Header{
		Name:     ".PKGINFO",
		Mode:     0o644,
		Size:     int64(len(metadata)),
		Typeflag: tar.TypeReg,
		Uid:      0,
		Gid:      0,
		Uname:    "root",
		Gname:    "root",
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write .PKGINFO header: %w", err)
	}
	if _, err := io.WriteString(tw, metadata); err != nil {
		return fmt.Errorf("write .PKGINFO: %w", err)
	}
	if len(entries) > 0 {
		fpData := footprint.Format(entries)
		fpHdr := &tar.Header{
			Name:     ".FOOTPRINT",
			Mode:     0o644,
			Size:     int64(len(fpData)),
			Typeflag: tar.TypeReg,
			Uid:      0,
			Gid:      0,
			Uname:    "root",
			Gname:    "root",
		}
		if err := tw.WriteHeader(fpHdr); err != nil {
			return fmt.Errorf("write .FOOTPRINT header: %w", err)
		}
		if _, err := io.WriteString(tw, fpData); err != nil {
			return fmt.Errorf("write .FOOTPRINT: %w", err)
		}
	}

	payloadCount := 0
	err = filepath.Walk(pkgDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect package staging path %q: %w", path, walkErr)
		}
		if path == pkgDir {
			return nil
		}
		payloadCount++
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
		hdr.Uid = 0
		hdr.Gid = 0
		hdr.Uname = "root"
		hdr.Gname = "root"
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
	if payloadCount == 0 {
		tw.Close()
		tarFile.Close()
		return fmt.Errorf("package staging directory %q is empty; check DESTDIR=\"$pkgdir\"", pkgDir)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := tarFile.Close(); err != nil {
		return fmt.Errorf("close temporary tar: %w", err)
	}
	tmpZst, err := os.CreateTemp(filepath.Dir(output), ".pi-package-*.pkg.tar.zst")
	if err != nil {
		return fmt.Errorf("create temporary package archive: %w", err)
	}
	tmpZstName := tmpZst.Name()
	tmpZst.Close()
	defer os.Remove(tmpZstName)
	cmd := exec.Command("zstd", "-q", "-f", "-o", tmpZstName, tarName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compress package with zstd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(tmpZstName, 0o644); err != nil {
		return fmt.Errorf("chmod package archive %q: %w", tmpZstName, err)
	}
	if err := os.Rename(tmpZstName, output); err != nil {
		return fmt.Errorf("commit package archive %q: %w", output, err)
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
	if err := decompress(archivePath, tempName); err != nil {
		return fmt.Errorf("extract package archive %q: %w", archivePath, err)
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
		if filepath.ToSlash(name) == ".PKGINFO" || filepath.ToSlash(name) == ".FOOTPRINT" {
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
			parentDir := filepath.Dir(dest)
			if err := os.MkdirAll(parentDir, 0o755); err != nil {
				return fmt.Errorf("create package file directory %q: %w", parentDir, err)
			}
			if existing, statErr := os.Lstat(dest); statErr == nil {
				if existing.Mode()&os.ModeSymlink != 0 {
					if err := os.Remove(dest); err != nil {
						return fmt.Errorf("replace existing symlink %q: %w", dest, err)
					}
				} else if existing.Mode().Perm()&0o200 == 0 {
					if err := os.Chmod(dest, existing.Mode().Perm()|0o200); err != nil {
						return fmt.Errorf("make existing package file writable %q: %w", dest, err)
					}
				}
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("inspect existing package file %q: %w", dest, statErr)
			}
			finalMode := hdr.FileInfo().Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
			// Write to a sibling temp file and rename atomically (matching CRUX
			// ARCHIVE_EXTRACT_UNLINK semantics): avoids ETXTBSY on running
			// binaries/libraries and leaves no partial file on failure.
			tmpOut, err := os.CreateTemp(parentDir, ".pi-extract-file-*")
			if err != nil {
				return fmt.Errorf("create temporary package file for %q: %w", dest, err)
			}
			tmpOutName := tmpOut.Name()
			_, copyErr := io.Copy(tmpOut, tr)
			closeErr := tmpOut.Close()
			if copyErr != nil {
				_ = os.Remove(tmpOutName)
				return fmt.Errorf("extract package file %q: %w", dest, copyErr)
			}
			if closeErr != nil {
				_ = os.Remove(tmpOutName)
				return fmt.Errorf("close package file %q: %w", dest, closeErr)
			}
			if err := os.Chmod(tmpOutName, finalMode); err != nil {
				_ = os.Remove(tmpOutName)
				return fmt.Errorf("set package file mode %q: %w", dest, err)
			}
			if err := os.Rename(tmpOutName, dest); err != nil {
				_ = os.Remove(tmpOutName)
				return fmt.Errorf("commit package file %q: %w", dest, err)
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
	if err := decompress(archivePath, tempName); err != nil {
		return nil, fmt.Errorf("inspect package archive %q: %w", archivePath, err)
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
		if name == ".PKGINFO" || name == ".FOOTPRINT" || name == "." || name == "" || strings.HasPrefix(name, "var/lib/pi/packages/") {
			continue
		}
		entry := footprint.Entry{Path: "/" + strings.TrimPrefix(name, "/"), Mode: hdr.FileInfo().Mode().Perm()}
		switch hdr.Typeflag {
		case tar.TypeDir:
			entry.Type = "dir"
		case tar.TypeSymlink:
			entry.Type, entry.Mode, entry.LinkTarget = "symlink", 0o777, hdr.Linkname
		default:
			entry.Type = "file"
		}
		entries = append(entries, entry)
	}
}

type Info struct {
	Pkgname     string
	Pkgver      string
	Pkgrel      string
	Backup      []string
	Depends     []string
	Arch        string
	USEFlags    []string
	USEDisabled []string
	USEHash     string
}

// ReadInfo reads package identity fields from a .PKGINFO archive member.
func ReadInfo(archivePath string) (Info, error) {
	temp, err := os.CreateTemp(filepath.Dir(archivePath), ".pi-info-*.tar")
	if err != nil {
		return Info{}, err
	}
	tempName := temp.Name()
	if err := temp.Close(); err != nil {
		return Info{}, err
	}
	defer os.Remove(tempName)
	if err := decompress(archivePath, tempName); err != nil {
		return Info{}, fmt.Errorf("read package metadata from %q: %w", archivePath, err)
	}

	f, err := os.Open(tempName)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Info{}, err
		}
		if hdr.Name != ".PKGINFO" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return Info{}, err
		}
		info := Info{}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "pkgname":
				info.Pkgname = strings.Trim(strings.TrimSpace(value), `"'`)
			case "pkgver":
				info.Pkgver = strings.Trim(strings.TrimSpace(value), `"'`)
			case "pkgrel":
				info.Pkgrel = strings.Trim(strings.TrimSpace(value), `"'`)
			case "backup":
				info.Backup = append(info.Backup, strings.Trim(strings.TrimSpace(value), `"'`))
			case "depends":
				info.Depends = append(info.Depends, strings.Trim(strings.TrimSpace(value), `"'`))
			case "arch":
				info.Arch = strings.Trim(strings.TrimSpace(value), `"'`)
			case "use", "use_flags", "use_tags":
				info.USEFlags = append(info.USEFlags, strings.Fields(strings.Trim(strings.TrimSpace(value), `"'`))...)
			case "use_disabled":
				info.USEDisabled = append(info.USEDisabled, strings.Fields(strings.Trim(strings.TrimSpace(value), `"'`))...)
			case "use_hash":
				info.USEHash = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
		return info, nil
	}
	return Info{}, fmt.Errorf("package archive %q has no .PKGINFO", archivePath)
}

func decompress(archivePath, outputPath string) error {
	out, err := exec.Command("zstd", "-q", "-d", "-f", "-o", outputPath, archivePath).CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("invalid .pkg.tar.zst compression; expected a zstd-compressed package archive (do not use gzip with a .pkg.tar.zst extension): %s", detail)
}
