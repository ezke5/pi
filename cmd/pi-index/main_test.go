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

func TestRunIndexHandlesArchAndHyphenatedNames(t *testing.T) {
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	if err := os.MkdirAll(packages, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{
		"hello-1.0.0-1-x86_64.pkg.tar.zst",
		"foo-bar-1.0.0-1.pkg.tar.zst",
	} {
		if err := os.WriteFile(filepath.Join(packages, filename), []byte(filename), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	indexPath := filepath.Join(root, "index.json")
	var warnings bytes.Buffer
	if err := runIndex(indexPath, packages, "https://example.test/rolling", "x86_64", &warnings); err != nil {
		t.Fatal(err)
	}
	index, err := repoindex.Load(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if group, ok := index.Packages["hello"]; !ok || len(group.Versions) != 1 || group.Versions[0].Pkgver != "1.0.0" || group.Versions[0].Pkgrel != "1" || group.Versions[0].Arch != "x86_64" {
		t.Fatalf("hello index entry = %+v, ok=%v", group, ok)
	}
	if group, ok := index.Packages["foo-bar"]; !ok || len(group.Versions) != 1 || group.Latest != "1.0.0-1" {
		t.Fatalf("hyphenated index entry = %+v, ok=%v", group, ok)
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

func TestParseArchiveNamingConventions(t *testing.T) {
	tests := []struct {
		filename             string
		hint                 string
		name, ver, rel, arch string
	}{
		{"hello-1.0.0-1-x86_64.pkg.tar.zst", "", "hello", "1.0.0", "1", "x86_64"},
		{"hello-1.0.0-1.pkg.tar.zst", "x86_64", "hello", "1.0.0", "1", "x86_64"},
		{"foo-bar-1.0.0-1-x86_64.pkg.tar.zst", "", "foo-bar", "1.0.0", "1", "x86_64"},
		{"foo-bar-1.0.0-1.pkg.tar.zst", "", "foo-bar", "1.0.0", "1", ""},
	}
	for _, tt := range tests {
		name, ver, rel, arch, ok := parseArchive(tt.filename, tt.hint)
		if !ok || name != tt.name || ver != tt.ver || rel != tt.rel || arch != tt.arch {
			t.Fatalf("parse %q = %q %q %q %q %v", tt.filename, name, ver, rel, arch, ok)
		}
	}
}
