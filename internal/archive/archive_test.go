package archive

import (
	"archive/tar"
	"compress/gzip"
	"io"
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

func TestExtractOverwritesReadOnlyFiles(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(pkgDir, "usr", "bin", "tool")
	docPath := filepath.Join(pkgDir, "usr", "share", "readme")
	if err := os.WriteFile(binPath, []byte("new-bin"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binPath, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("new-doc"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(docPath, 0o444); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(root, "ro-1-1.pkg.tar.zst")
	if err := Create(pkgDir, archivePath, "ro", "1", "1", nil); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	destRoot := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(destRoot, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destRoot, "usr", "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	existingBin := filepath.Join(destRoot, "usr", "bin", "tool")
	existingDoc := filepath.Join(destRoot, "usr", "share", "readme")
	if err := os.WriteFile(existingBin, []byte("old-bin"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existingBin, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingDoc, []byte("old-doc"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existingDoc, 0o444); err != nil {
		t.Fatal(err)
	}

	if err := Extract("", archivePath, destRoot); err != nil {
		t.Fatalf("extract over read-only files failed: %v", err)
	}
	for _, tc := range []struct {
		path string
		want string
		mode os.FileMode
	}{
		{existingBin, "new-bin", 0o555},
		{existingDoc, "new-doc", 0o444},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if string(data) != tc.want {
			t.Fatalf("%s content = %q, want %q", tc.path, data, tc.want)
		}
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != tc.mode {
			t.Fatalf("%s mode = %04o, want %04o", tc.path, info.Mode().Perm(), tc.mode)
		}
	}
}

func TestCreateWithUSENormalizesOwnershipToRoot(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/bin/demo", filepath.Join(pkgDir, "bin", "demo")); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(root, "demo-1-1.pkg.tar.zst")
	if err := CreateWithUSE(pkgDir, archivePath, "demo", "1", "1", "x86_64", []string{"nls"}, nil, "deadbeef", nil); err != nil {
		t.Fatalf("CreateWithUSE failed: %v", err)
	}

	tarPath := filepath.Join(root, "demo.tar")
	if err := decompress(archivePath, tarPath); err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	seen := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen++
		if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "root" || hdr.Gname != "root" {
			t.Fatalf("entry %q ownership = %d:%d (%s:%s), want 0:0 (root:root)", hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
		}
		if hdr.Name == "usr/bin/demo" && os.FileMode(hdr.Mode).Perm() != 0o755 {
			t.Fatalf("entry %q mode = %04o, want 0755", hdr.Name, os.FileMode(hdr.Mode).Perm())
		}
	}
	if seen < 4 {
		t.Fatalf("expected at least 4 tar entries, saw %d", seen)
	}
}

func TestCreateWithUSERejectsEmptyStagingDir(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "empty-1-1.pkg.tar.zst")
	err := Create(emptyDir, output, "empty", "1", "1", nil)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("expected empty staging dir error, got: %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("archive should not exist on empty staging failure: %v", statErr)
	}
}
