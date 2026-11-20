package binary

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ezke5/pi/internal/repoindex"
)

func TestResolveVerifiesCachedBinary(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "demo-1-1.pkg.tar.zst")
	if err := os.WriteFile(path, []byte("valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := repoindex.SHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64", Filename: filepath.Base(path), SHA256: sum})
	got, _, err := Resolve(index, "demo", "x86_64", cache, &bytes.Buffer{})
	if err != nil || got != path {
		t.Fatalf("resolve = %q, %v", got, err)
	}
}

func TestResolveRemovesChecksumFailure(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(cache, "demo-1-1.pkg.tar.zst")
	if err := os.WriteFile(path, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64", Filename: filepath.Base(path), SHA256: "bad"})
	if _, _, err := Resolve(index, "demo", "x86_64", cache, &bytes.Buffer{}); err == nil {
		t.Fatal("expected checksum failure")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid binary was retained: %v", err)
	}
}
