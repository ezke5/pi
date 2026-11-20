package archive

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeArchivePath(t *testing.T) {
	root := t.TempDir()
	got, err := safeArchivePath(root, filepath.Join("usr", "bin", "pi"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "usr", "bin", "pi") {
		t.Fatalf("unexpected archive path %q", got)
	}
	if _, err := safeArchivePath(root, filepath.Join("..", "outside")); err == nil {
		t.Fatal("expected archive traversal to be rejected")
	}
}

func TestSafeSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "usr", "bin", "pi")
	if _, err := safeSymlinkTarget(root, link, "../../bin/pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := safeSymlinkTarget(root, link, "../../../outside"); err == nil {
		t.Fatal("expected escaping symlink target to be rejected")
	}
	if _, err := safeSymlinkTarget(root, link, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute symlink target to be rejected")
	}
}

func TestInvalidCompressionReportsActionableError(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for compression inspection")
	}
	path := filepath.Join(t.TempDir(), "bad.pkg.tar.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "usr/bin/demo", Mode: 0o755, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("demo")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Entries(path)
	if err == nil || !strings.Contains(err.Error(), "expected a zstd-compressed package archive") {
		t.Fatalf("unexpected compression error: %v", err)
	}
}

func TestCreateRejectsAbsoluteSymlinkWithRelativeSuggestion(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "bash"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/bash", filepath.Join(pkgDir, "bin", "bash")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "bash-5.2.37-1.pkg.tar.zst")
	err := Create(pkgDir, output, "bash", "5.2.37", "1", nil)
	if err == nil {
		t.Fatal("expected absolute symlink to be rejected during archive creation")
	}
	for _, want := range []string{"absolute symlink targets are not allowed", "bin/bash", "/usr/bin/bash", "../usr/bin/bash"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing expected fragment %q", err.Error(), want)
		}
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive should not be created when symlink validation fails: %v", statErr)
	}
}

func TestCreateRejectsEscapingRelativeSymlink(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../outside", filepath.Join(pkgDir, "usr", "bin", "escape")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "escape-1-1.pkg.tar.zst")
	err := Create(pkgDir, output, "escape", "1", "1", nil)
	if err == nil || !strings.Contains(err.Error(), "target escapes destination root") {
		t.Fatalf("expected escaping symlink error, got: %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive should not be created when symlink validation fails: %v", statErr)
	}
}

func TestCreateAndExtractAllowsSafeRelativeSymlink(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "bash"), []byte("bash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/bin/bash", filepath.Join(pkgDir, "bin", "bash")); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "bash-5.2.37-1.pkg.tar.zst")
	if err := Create(pkgDir, output, "bash", "5.2.37", "1", nil); err != nil {
		t.Fatalf("expected relative symlink to be accepted: %v", err)
	}
	destRoot := filepath.Join(root, "extract")
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Extract("", output, destRoot); err != nil {
		t.Fatalf("extract relative symlink archive failed: %v", err)
	}
	got, err := os.Readlink(filepath.Join(destRoot, "bin", "bash"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "../usr/bin/bash" {
		t.Fatalf("extracted symlink target = %q, want ../usr/bin/bash", got)
	}
}
