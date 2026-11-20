package footprint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadPreservesModesAndNormalizesSymlinks(t *testing.T) {
	dir := t.TempDir()
	fpPath := filepath.Join(dir, "demo.footprint")
	want := []Entry{
		{Path: "/usr/lib", Type: "dir", Mode: 0o755},
		{Path: "/usr/lib/libc.so.6", Type: "file", Mode: 0o555},
		{Path: "/usr/share/doc/readme", Type: "file", Mode: 0o444},
		{Path: "/lib64/ld-linux-x86-64.so.2", Type: "symlink", Mode: 0o700, LinkTarget: "../usr/lib/ld-linux-x86-64.so.2"},
	}
	if err := Write(fpPath, want); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got, err := Read(fpPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	if got[0].Type != "dir" || got[0].Mode.Perm() != 0o755 || got[0].Path != "/usr/lib" {
		t.Fatalf("dir entry = %+v", got[0])
	}
	if got[1].Type != "file" || got[1].Mode.Perm() != 0o555 || got[1].Path != "/usr/lib/libc.so.6" {
		t.Fatalf("0555 file entry = %+v", got[1])
	}
	if got[2].Type != "file" || got[2].Mode.Perm() != 0o444 || got[2].Path != "/usr/share/doc/readme" {
		t.Fatalf("0444 file entry = %+v", got[2])
	}
	if got[3].Type != "symlink" || got[3].Mode.Perm() != 0o777 || got[3].LinkTarget != "../usr/lib/ld-linux-x86-64.so.2" {
		t.Fatalf("symlink entry = %+v", got[3])
	}

	// Legacy single-column footprints remain readable with Mode == 0.
	legacyPath := filepath.Join(dir, "legacy.footprint")
	if err := os.WriteFile(legacyPath, []byte("/usr/bin/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, err := Read(legacyPath)
	if err != nil || len(legacy) != 1 || legacy[0].Path != "/usr/bin/old" || legacy[0].Mode != 0 {
		t.Fatalf("legacy footprint = %+v, err=%v", legacy, err)
	}
}
