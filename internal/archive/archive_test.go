package archive

import (
	"path/filepath"
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
