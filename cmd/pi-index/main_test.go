package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/repoindex"
)

func TestRunIndexGeneratesAndMergesArchives(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	if err := os.MkdirAll(packages, 0o755); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, "staging")
	if err := os.MkdirAll(filepath.Join(staging, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "usr", "bin", "demo"), []byte("demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := footprint.Generate(staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Create(staging, filepath.Join(packages, "demo-1-1.pkg.tar.zst"), "demo", "1", "1", entries); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.json")
	var warnings bytes.Buffer
	if err := runIndex(indexPath, packages, "https://example.test/rolling", "x86_64", &warnings); err != nil {
		t.Fatal(err)
	}
	if err := archive.Create(staging, filepath.Join(packages, "demo-2-1.pkg.tar.zst"), "demo", "2", "1", entries); err != nil {
		t.Fatal(err)
	}
	if err := runIndex(indexPath, packages, "https://example.test/rolling", "x86_64", &warnings); err != nil {
		t.Fatal(err)
	}
	index, err := repoindex.Load(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	group := index.Packages["demo"]
	if len(group.Versions) != 2 || group.Latest != "2-1" {
		t.Fatalf("unexpected index after merge: %+v", group)
	}
}

func TestRunIndexWarnsForEmptyAndMalformedDirectories(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, "index.json")
	var warnings bytes.Buffer
	if err := runIndex(indexPath, filepath.Join(root, "missing"), "", "x86_64", &warnings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "no .pkg.tar.zst archives found") {
		t.Fatalf("missing empty-directory warning: %q", warnings.String())
	}
	packages := filepath.Join(root, "packages")
	if err := os.MkdirAll(packages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packages, "invalid.pkg.tar.zst"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings.Reset()
	if err := runIndex(indexPath, packages, "", "x86_64", &warnings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "unexpected filename") || !strings.Contains(warnings.String(), "no valid package archives") {
		t.Fatalf("missing malformed archive warnings: %q", warnings.String())
	}
}

func TestValidateBaseURL(t *testing.T) {
	if err := validateBaseURL("file:///tmp/packages"); err == nil || !strings.Contains(err.Error(), "only http and https") {
		t.Fatalf("unexpected file URL error: %v", err)
	}
}
