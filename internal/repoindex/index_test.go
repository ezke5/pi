package repoindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergePreservesVersionsAndUpdatesLatest(t *testing.T) {
	index := New()
	Merge(&index,
		Entry{Pkgname: "demo", Pkgver: "1.0", Pkgrel: "1", Arch: "x86_64", Filename: "demo-1.0-1.pkg.tar.zst"},
		Entry{Pkgname: "demo", Pkgver: "2.0", Pkgrel: "1", Arch: "x86_64", Filename: "demo-2.0-1.pkg.tar.zst"},
	)
	if len(index.Packages["demo"].Versions) != 2 || index.Packages["demo"].Latest != "2.0-1" {
		t.Fatalf("unexpected merged index: %+v", index)
	}
	Merge(&index, Entry{Pkgname: "demo", Pkgver: "2.0", Pkgrel: "1", Arch: "x86_64", Filename: "demo-2.0-1.pkg.tar.zst", SHA256: "updated"})
	if len(index.Packages["demo"].Versions) != 2 || index.Packages["demo"].Versions[1].SHA256 != "updated" {
		t.Fatalf("duplicate version was not replaced: %+v", index)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo", "index.json")
	index := New()
	Merge(&index, Entry{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Filename: "demo-1-1.pkg.tar.zst"})
	if err := Save(path, index); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Find(loaded, "demo", ""); !ok {
		t.Fatal("saved entry was not found")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestParseFilenameNamingConventions(t *testing.T) {
	tests := []struct {
		filename             string
		hint                 string
		name, ver, rel, arch string
	}{
		{"hello-1.0.0-1-x86_64.pkg.tar.zst", "", "hello", "1.0.0", "1", "x86_64"},
		{"hello-1.0.0-1.pkg.tar.zst", "aarch64", "hello", "1.0.0", "1", "aarch64"},
		{"foo-bar-1.0.0-1-aarch64.pkg.tar.zst", "", "foo-bar", "1.0.0", "1", "aarch64"},
		{"foo-bar-1.0.0-1.pkg.tar.zst", "", "foo-bar", "1.0.0", "1", ""},
	}
	for _, tt := range tests {
		name, ver, rel, arch, ok := ParseFilename(tt.filename, tt.hint)
		if !ok || name != tt.name || ver != tt.ver || rel != tt.rel || arch != tt.arch {
			t.Fatalf("parse %q = %q %q %q %q %v", tt.filename, name, ver, rel, arch, ok)
		}
	}
}

func TestMergeTreatsNoArchAndArchHintAsSameVersion(t *testing.T) {
	index := New()
	Merge(&index, Entry{Pkgname: "hello", Pkgver: "1.0.0", Pkgrel: "1", Arch: "", Filename: "hello-1.0.0-1.pkg.tar.zst"})
	Merge(&index, Entry{Pkgname: "hello", Pkgver: "1.0.0", Pkgrel: "1", Arch: "x86_64", Filename: "hello-1.0.0-1-x86_64.pkg.tar.zst"})
	group := index.Packages["hello"]
	if len(group.Versions) != 1 || group.Latest != "1.0.0-1" || group.Versions[0].Arch != "x86_64" {
		t.Fatalf("incremental architecture merge = %+v", group)
	}
}

func TestMergeKeepsSeparateUSEVariantsAndFindsExactState(t *testing.T) {
	index := New()
	Merge(&index,
		Entry{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64", Filename: "demo-wayland.pkg.tar.zst", USEHash: "hash-wayland", USEFlags: []string{"wayland"}},
		Entry{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64", Filename: "demo-no-wayland.pkg.tar.zst", USEHash: "hash-no-wayland"},
	)
	if len(index.Packages["demo"].Versions) != 2 {
		t.Fatalf("USE variants were not preserved: %+v", index.Packages["demo"])
	}
	entry, ok := FindForUSE(index, "demo", "x86_64", "hash-wayland")
	if !ok || entry.Filename != "demo-wayland.pkg.tar.zst" {
		t.Fatalf("USE-aware find = %+v, %v", entry, ok)
	}
}
