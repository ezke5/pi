package rootfs

import (
	"path/filepath"
	"testing"
)

func TestValidateRejectsHostRoot(t *testing.T) {
	if _, err := Validate("/", false); err == nil {
		t.Fatal("expected host root to be rejected")
	}
	if got, err := Validate("/", true); err != nil || got != "/" {
		t.Fatalf("explicit host root: got %q, err %v", got, err)
	}
}

func TestTargetPathEscaping(t *testing.T) {
	root := t.TempDir()
	got, err := TargetPath(root, "/usr/bin/pi")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "usr", "bin", "pi")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := TargetPath(root, "../outside"); err == nil {
		t.Fatal("expected escape to be rejected")
	}
}
