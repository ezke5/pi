// Package repoindex manages the JSON index published with binary packages.
package repoindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Index struct {
	SchemaVersion int                `json:"schema_version"`
	Packages      map[string]Package `json:"packages"`
}

type Package struct {
	Latest   string  `json:"latest"`
	Versions []Entry `json:"versions"`
}

type Entry struct {
	Pkgname  string `json:"pkgname"`
	Pkgver   string `json:"pkgver"`
	Pkgrel   string `json:"pkgrel"`
	Arch     string `json:"arch"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func New() Index { return Index{SchemaVersion: 1, Packages: map[string]Package{}} }

func Load(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("read repository index %q: %w", path, err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, fmt.Errorf("parse repository index %q: %w", path, err)
	}
	if index.SchemaVersion == 0 {
		index.SchemaVersion = 1
	}
	if index.Packages == nil {
		index.Packages = map[string]Package{}
	}
	return index, nil
}

func Save(path string, index Index) error {
	if index.SchemaVersion == 0 {
		index.SchemaVersion = 1
	}
	if index.Packages == nil {
		index.Packages = map[string]Package{}
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode repository index: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create repository index directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pi-index-*")
	if err != nil {
		return fmt.Errorf("create temporary repository index: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary repository index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary repository index: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace repository index %q: %w", path, err)
	}
	return nil
}

func Merge(index *Index, entries ...Entry) {
	if index.SchemaVersion == 0 {
		index.SchemaVersion = 1
	}
	if index.Packages == nil {
		index.Packages = map[string]Package{}
	}
	for _, entry := range entries {
		if entry.Pkgname == "" || entry.Filename == "" {
			continue
		}
		group := index.Packages[entry.Pkgname]
		replaced := false
		for i, old := range group.Versions {
			sameArch := old.Arch == entry.Arch || old.Arch == "" || entry.Arch == ""
			if old.Pkgver == entry.Pkgver && old.Pkgrel == entry.Pkgrel && sameArch {
				if entry.Arch == "" {
					entry.Arch = old.Arch
				}
				group.Versions[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			group.Versions = append(group.Versions, entry)
		}
		sort.Slice(group.Versions, func(i, j int) bool {
			return CompareVersion(group.Versions[i].Pkgver, group.Versions[i].Pkgrel, group.Versions[j].Pkgver, group.Versions[j].Pkgrel) < 0
		})
		if len(group.Versions) > 0 {
			latest := group.Versions[len(group.Versions)-1]
			group.Latest = latest.Pkgver + "-" + latest.Pkgrel
		}
		index.Packages[entry.Pkgname] = group
	}
}

func Find(index Index, name, arch string) (Entry, bool) {
	group, ok := index.Packages[name]
	if !ok {
		return Entry{}, false
	}
	for i := len(group.Versions) - 1; i >= 0; i-- {
		if arch == "" || group.Versions[i].Arch == "" || group.Versions[i].Arch == arch {
			return group.Versions[i], true
		}
	}
	return Entry{}, false
}

// ParseFilename parses both supported package archive naming conventions.
// Supported forms are <pkgname>-<pkgver>-<pkgrel>.pkg.tar.zst and
// <pkgname>-<pkgver>-<pkgrel>-<arch>.pkg.tar.zst.
func ParseFilename(filename, archHint string) (pkgname, pkgver, pkgrel, arch string, ok bool) {
	if !strings.HasSuffix(filename, ".pkg.tar.zst") {
		return "", "", "", "", false
	}
	base := strings.TrimSuffix(filename, ".pkg.tar.zst")
	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		return "", "", "", "", false
	}

	arch = archHint
	last := parts[len(parts)-1]
	if archHint != "" && last == archHint {
		parts = parts[:len(parts)-1]
	} else if LooksLikeArch(last) {
		arch = last
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 3 {
		return "", "", "", "", false
	}
	pkgrel = parts[len(parts)-1]
	pkgver = parts[len(parts)-2]
	pkgname = strings.Join(parts[:len(parts)-2], "-")
	if pkgname == "" || pkgver == "" || pkgrel == "" {
		return "", "", "", "", false
	}
	return pkgname, pkgver, pkgrel, arch, true
}

func LooksLikeArch(value string) bool {
	switch value {
	case "any", "amd64", "x86_64", "i686", "i586", "i486", "aarch64", "arm64",
		"armv7h", "armv7l", "armv6h", "riscv64", "loong64", "wasm32", "s390x", "ppc64le", "ppc64":
		return true
	}
	if strings.Contains(value, "_") {
		return true
	}
	for _, prefix := range []string{"arm", "riscv", "mips", "ppc", "s390"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q for checksum: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Verify(path, expected string) error {
	actual, err := SHA256(path)
	if err != nil {
		return err
	}
	if expected != "" && !strings.EqualFold(actual, expected) {
		return fmt.Errorf("SHA-256 mismatch for %q: expected %s, got %s", path, expected, actual)
	}
	return nil
}

func CompareVersion(verA, relA, verB, relB string) int {
	partsA := append(strings.Split(verA, "-"), relA)
	partsB := append(strings.Split(verB, "-"), relB)
	for i := 0; i < len(partsA) && i < len(partsB); i++ {
		if partsA[i] == partsB[i] {
			continue
		}
		var a, b int
		if _, errA := fmt.Sscanf(partsA[i], "%d", &a); errA == nil {
			if _, errB := fmt.Sscanf(partsB[i], "%d", &b); errB == nil && a != b {
				if a < b {
					return -1
				}
				return 1
			}
		}
		if partsA[i] < partsB[i] {
			return -1
		}
		return 1
	}
	if len(partsA) < len(partsB) {
		return -1
	}
	if len(partsA) > len(partsB) {
		return 1
	}
	return 0
}
