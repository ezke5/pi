// Package state manages installed package records and ownership checks.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/rootfs"
)

type Record struct {
	Pkgname     string   `json:"pkgname"`
	Pkgver      string   `json:"pkgver"`
	Pkgrel      string   `json:"pkgrel"`
	Arch        string   `json:"arch,omitempty"`
	USEFlags    []string `json:"use_flags,omitempty"`
	USEDisabled []string `json:"use_disabled,omitempty"`
	USEHash     string   `json:"use_hash,omitempty"`
	Depends     []string `json:"depends,omitempty"`
	Backup      []string `json:"backup,omitempty"`
}

func RecordDir(root string) (string, error) {
	return rootfs.TargetPath(root, "/var/lib/pi/packages")
}

func Load(root, name string) (Record, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".meta"))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{Pkgname: name}, nil
		}
		return Record{}, fmt.Errorf("read package metadata %q: %w", name, err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("parse package metadata %q: %w", name, err)
	}
	return record, nil
}

func Installed(root string) ([]Record, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []Record
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".meta") {
			continue
		}
		record, err := Load(root, strings.TrimSuffix(file.Name(), ".meta"))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Pkgname < records[j].Pkgname })
	return records, nil
}

func Footprint(root, name string) ([]footprint.Entry, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return nil, err
	}
	return footprint.Read(filepath.Join(dir, name+".footprint"))
}

func Write(root string, record Record, entries []footprint.Entry) error {
	dir, err := RecordDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create package metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	meta := filepath.Join(dir, record.Pkgname+".meta")
	tmp, err := os.CreateTemp(dir, "."+record.Pkgname+".meta-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, meta); err != nil {
		return fmt.Errorf("commit package metadata: %w", err)
	}
	return footprint.Write(filepath.Join(dir, record.Pkgname+".footprint"), entries)
}

func OwnedByOthers(root, name string, path string) (bool, error) {
	records, err := Installed(root)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Pkgname == name {
			continue
		}
		entries, err := Footprint(root, record.Pkgname)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if entry.Path == path {
				return true, nil
			}
		}
	}
	return false, nil
}

// VerifyInstalled checks whether name is already installed in root at the exact
// expected version-release and USE hash, and whether all recorded footprint
// entries exist on disk without permission or integrity errors.
func VerifyInstalled(root, name, expectedVer, expectedRel, expectedUSEHash string) (bool, string) {
	if root == "" || name == "" || expectedVer == "" {
		return false, "incomplete package identity"
	}
	record, err := Load(root, name)
	if err != nil {
		return false, fmt.Sprintf("read metadata: %v", err)
	}
	if record.Pkgver == "" {
		return false, "not installed"
	}
	if record.Pkgver != expectedVer || record.Pkgrel != expectedRel {
		return false, fmt.Sprintf("installed version %s-%s differs from %s-%s", record.Pkgver, record.Pkgrel, expectedVer, expectedRel)
	}
	if expectedUSEHash != "" && record.USEHash != expectedUSEHash {
		return false, fmt.Sprintf("USE hash changed (%q -> %q)", record.USEHash, expectedUSEHash)
	}
	dir, err := RecordDir(root)
	if err != nil {
		return false, fmt.Sprintf("resolve metadata dir: %v", err)
	}
	footprintPath := filepath.Join(dir, name+".footprint")
	if _, err := os.Stat(footprintPath); err != nil {
		return false, fmt.Sprintf("missing footprint: %v", err)
	}
	entries, err := Footprint(root, name)
	if err != nil {
		return false, fmt.Sprintf("read footprint: %v", err)
	}
	for _, entry := range entries {
		targetPath, err := rootfs.TargetPath(root, entry.Path)
		if err != nil {
			return false, fmt.Sprintf("invalid footprint path %q: %v", entry.Path, err)
		}
		info, err := os.Lstat(targetPath)
		if err != nil {
			return false, fmt.Sprintf("inspect installed path %q: %v", entry.Path, err)
		}
		switch entry.Type {
		case "dir":
			if !info.IsDir() {
				return false, fmt.Sprintf("installed path %q is not a directory", entry.Path)
			}
		case "symlink":
			if info.Mode()&os.ModeSymlink == 0 {
				return false, fmt.Sprintf("installed path %q is not a symlink", entry.Path)
			}
			linkTarget, err := os.Readlink(targetPath)
			if err != nil {
				return false, fmt.Sprintf("read installed symlink %q: %v", entry.Path, err)
			}
			if entry.LinkTarget != "" && linkTarget != entry.LinkTarget {
				return false, fmt.Sprintf("installed symlink %q target %q differs from %q", entry.Path, linkTarget, entry.LinkTarget)
			}
		default:
			if !info.Mode().IsRegular() {
				return false, fmt.Sprintf("installed path %q is not a regular file", entry.Path)
			}
			if info.Mode().Perm() == 0 {
				return false, fmt.Sprintf("installed file %q has zero permissions", entry.Path)
			}
			f, err := os.Open(targetPath)
			if err != nil {
				return false, fmt.Sprintf("open installed file %q: %v", entry.Path, err)
			}
			_ = f.Close()
		}
	}
	return true, ""
}
