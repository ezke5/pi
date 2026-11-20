package binary

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	matches, err := filepath.Glob(filepath.Join(cache, ".pi-download-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary downloads were retained: %v", matches)
	}
}

func TestResolveRejectsNonHTTPURL(t *testing.T) {
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{
		Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64",
		Filename: "demo-1-1.pkg.tar.zst", URL: "file:///tmp/demo-1-1.pkg.tar.zst",
	})
	_, _, err := Resolve(index, "demo", "x86_64", t.TempDir(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "only http and https") {
		t.Fatalf("unexpected URL error: %v", err)
	}
}

func TestResolveRemovesDownloadedChecksumFailureAndTemps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bad download"))
	}))
	defer server.Close()
	cache := t.TempDir()
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{
		Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64",
		Filename: "demo-1-1.pkg.tar.zst", URL: server.URL + "/demo-1-1.pkg.tar.zst",
		SHA256: "expected-different-checksum",
	})
	_, _, err := Resolve(index, "demo", "x86_64", cache, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unexpected download checksum error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "demo-1-1.pkg.tar.zst")); !os.IsNotExist(err) {
		t.Fatalf("invalid downloaded binary was retained: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(cache, ".pi-download-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary downloads were retained: %v", matches)
	}
}
