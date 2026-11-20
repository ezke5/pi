// Package state manages installed package records and ownership checks.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/rootfs"
)

// DBLock holds an advisory flock on the target root's package database
// directory (mirroring CRUX pkgutils db_lock).
type DBLock struct {
	dir *os.File
}

// Lock acquires a non-blocking shared or exclusive flock on var/lib/pi/packages.
func Lock(root string, exclusive bool) (*DBLock, error) {
	dirPath, err := RecordDir(root)
	if err != nil {
		return nil, err
	}
	if exclusive {
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			return nil, fmt.Errorf("create package metadata directory: %w", err)
		}
	} else if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return &DBLock{}, nil
	}
	dir, err := os.Open(dirPath)
	if err != nil {
		return nil, fmt.Errorf("open package database directory %q: %w", dirPath, err)
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(dir.Fd()), mode|syscall.LOCK_NB); err != nil {
		_ = dir.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("package database %q is currently locked by another process", dirPath)
		}
		return nil, fmt.Errorf("lock package database %q: %w", dirPath, err)
	}
	return &DBLock{dir: dir}, nil
}

// Unlock releases the flock and closes the directory descriptor.
func (l *DBLock) Unlock() error {
	if l == nil || l.dir == nil {
		return nil
	}
	err1 := syscall.Flock(int(l.dir.Fd()), syscall.LOCK_UN)
	err2 := l.dir.Close()
	l.dir = nil
	if err1 != nil {
		return err1
	}
	return err2
}

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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
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

// VerifyResult captures structured verification results for an installed package.
type VerifyResult struct {
	OK     bool
	Issues []string
}

// Reason returns a compact human-readable summary of verification failures.
func (r VerifyResult) Reason() string {
	if r.OK || len(r.Issues) == 0 {
		return ""
	}
	return strings.Join(r.Issues, "; ")
}

// VerifyInstalled checks whether name is already installed in root at the exact
// expected version-release and USE hash, and whether all recorded footprint
// entries exist on disk without permission, symlink, or mode/ownership errors.
func VerifyInstalled(root, name, expectedVer, expectedRel, expectedUSEHash string) (bool, string) {
	res := VerifyPackage(root, name, expectedVer, expectedRel, expectedUSEHash)
	return res.OK, res.Reason()
}

// VerifyPackage performs a full footprint and metadata integrity check for an
// installed package. When expectedVer is empty, the package's recorded metadata
// version is checked only for on-disk footprint integrity.
func VerifyPackage(root, name, expectedVer, expectedRel, expectedUSEHash string) VerifyResult {
	if root == "" || name == "" {
		return VerifyResult{Issues: []string{"incomplete package identity"}}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return VerifyResult{Issues: []string{fmt.Sprintf("resolve root: %v", err)}}
	}
	record, err := Load(rootAbs, name)
	if err != nil {
		return VerifyResult{Issues: []string{fmt.Sprintf("read metadata: %v", err)}}
	}
	if record.Pkgver == "" {
		return VerifyResult{Issues: []string{"not installed"}}
	}
	if expectedVer != "" && (record.Pkgver != expectedVer || record.Pkgrel != expectedRel) {
		return VerifyResult{Issues: []string{fmt.Sprintf("installed version %s-%s differs from %s-%s", record.Pkgver, record.Pkgrel, expectedVer, expectedRel)}}
	}
	if expectedUSEHash != "" && record.USEHash != expectedUSEHash {
		return VerifyResult{Issues: []string{fmt.Sprintf("USE hash changed (%q -> %q)", record.USEHash, expectedUSEHash)}}
	}
	dir, err := RecordDir(rootAbs)
	if err != nil {
		return VerifyResult{Issues: []string{fmt.Sprintf("resolve metadata dir: %v", err)}}
	}
	footprintPath := filepath.Join(dir, name+".footprint")
	if _, err := os.Stat(footprintPath); err != nil {
		return VerifyResult{Issues: []string{fmt.Sprintf("missing footprint: %v", err)}}
	}
	entries, err := Footprint(rootAbs, name)
	if err != nil {
		return VerifyResult{Issues: []string{fmt.Sprintf("read footprint: %v", err)}}
	}

	backupSet := map[string]bool{}
	for _, b := range record.Backup {
		backupSet["/"+strings.TrimPrefix(filepath.ToSlash(b), "/")] = true
	}
	checkOwnership := os.Geteuid() == 0

	var issues []string
	for _, entry := range entries {
		targetPath, err := rootfs.TargetPath(rootAbs, entry.Path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("invalid footprint path %q: %v", entry.Path, err))
			continue
		}
		info, err := os.Lstat(targetPath)
		if err != nil {
			issues = append(issues, fmt.Sprintf("missing or inaccessible %s: %v", entry.Path, err))
			continue
		}
		isConfig := backupSet[entry.Path] || strings.HasPrefix(entry.Path, "/etc/")
		switch entry.Type {
		case "dir":
			if !info.IsDir() {
				issues = append(issues, fmt.Sprintf("installed path %s is not a directory", entry.Path))
				continue
			}
			if entry.Mode != 0 && !isConfig && info.Mode().Perm() != entry.Mode.Perm() {
				issues = append(issues, fmt.Sprintf("mode drift on %s: got %04o, want %04o", entry.Path, info.Mode().Perm(), entry.Mode.Perm()))
			}
			if checkOwnership && !isConfig {
				if issue := checkRootOwnership(entry.Path, info); issue != "" {
					issues = append(issues, issue)
				}
			}
		case "symlink":
			if info.Mode()&os.ModeSymlink == 0 {
				issues = append(issues, fmt.Sprintf("installed path %s is not a symlink", entry.Path))
				continue
			}
			linkTarget, err := os.Readlink(targetPath)
			if err != nil {
				issues = append(issues, fmt.Sprintf("read installed symlink %s: %v", entry.Path, err))
				continue
			}
			if entry.LinkTarget != "" && linkTarget != entry.LinkTarget {
				issues = append(issues, fmt.Sprintf("symlink %s target %q differs from recorded %q", entry.Path, linkTarget, entry.LinkTarget))
				continue
			}
			if err := resolveSymlinkInRoot(rootAbs, targetPath, entry.Path, linkTarget); err != nil {
				issues = append(issues, err.Error())
			}
		default:
			if !info.Mode().IsRegular() {
				issues = append(issues, fmt.Sprintf("installed path %s is not a regular file", entry.Path))
				continue
			}
			if info.Mode().Perm() == 0 {
				issues = append(issues, fmt.Sprintf("installed file %s has zero permissions", entry.Path))
				continue
			}
			f, err := os.Open(targetPath)
			if err != nil {
				issues = append(issues, fmt.Sprintf("unreadable installed file %s: %v", entry.Path, err))
				continue
			}
			_ = f.Close()
			if entry.Mode != 0 && !isConfig && info.Mode().Perm() != entry.Mode.Perm() {
				issues = append(issues, fmt.Sprintf("mode drift on %s: got %04o, want %04o", entry.Path, info.Mode().Perm(), entry.Mode.Perm()))
			}
			if checkOwnership && !isConfig {
				if issue := checkRootOwnership(entry.Path, info); issue != "" {
					issues = append(issues, issue)
				}
			}
		}
	}
	return VerifyResult{OK: len(issues) == 0, Issues: issues}
}

func checkRootOwnership(entryPath string, info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	if st.Uid != 0 || st.Gid != 0 {
		return fmt.Sprintf("ownership drift on %s: got %d:%d, want 0:0", entryPath, st.Uid, st.Gid)
	}
	return ""
}

const maxSymlinkDepth = 40

func resolveSymlinkInRoot(rootAbs, linkAbs, displayPath, target string) error {
	curLink := linkAbs
	curTarget := target
	for depth := 0; depth < maxSymlinkDepth; depth++ {
		if filepath.IsAbs(curTarget) {
			return fmt.Errorf("unsafe symlink %s -> %s: absolute symlink targets are not allowed", displayPath, curTarget)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(curLink), curTarget))
		rel, err := filepath.Rel(rootAbs, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe symlink %s -> %s: target escapes root", displayPath, curTarget)
		}
		relSlash := filepath.ToSlash(rel)
		if isVirtualRootfsPath(relSlash) {
			return nil
		}
		info, err := os.Lstat(resolved)
		if err != nil {
			return fmt.Errorf("broken symlink %s -> %s (missing /%s)", displayPath, target, relSlash)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		nextTarget, err := os.Readlink(resolved)
		if err != nil {
			return fmt.Errorf("read symlink chain %s -> /%s: %v", displayPath, relSlash, err)
		}
		curLink = resolved
		curTarget = nextTarget
	}
	return fmt.Errorf("broken symlink %s -> %s: symlink loop (depth > %d)", displayPath, target, maxSymlinkDepth)
}

func isVirtualRootfsPath(relSlash string) bool {
	for _, prefix := range []string{"proc", "sys", "dev", "run"} {
		if relSlash == prefix || strings.HasPrefix(relSlash, prefix+"/") {
			return true
		}
	}
	return false
}
