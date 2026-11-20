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
