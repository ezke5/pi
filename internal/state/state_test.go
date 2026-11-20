package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezke5/pi/internal/footprint"
)

func TestVerifyInstalledSymlinkChainsAndBrokenLib64Targets(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"lib64", "usr/lib", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ldTarget := filepath.Join(root, "usr", "lib", "ld-linux-x86-64.so.2")
	libcReal := filepath.Join(root, "usr", "lib", "libc-2.40.so")
	ncursesReal := filepath.Join(root, "usr", "lib", "libncursesw.so.6.5")
	for _, p := range []string{ldTarget, libcReal, ncursesReal} {
		if err := os.WriteFile(p, []byte("elf"), 0o555); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../usr/lib/ld-linux-x86-64.so.2", filepath.Join(root, "lib64", "ld-linux-x86-64.so.2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libc-2.40.so", filepath.Join(root, "usr", "lib", "libc.so.6")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libncursesw.so.6.5", filepath.Join(root, "usr", "lib", "libtinfo.so.6")); err != nil {
		t.Fatal(err)
	}
	// Symlink to virtual /proc path must be allowed while /proc is unmounted.
	if err := os.Symlink("../proc/self/mounts", filepath.Join(root, "etc", "mtab")); err != nil {
		t.Fatal(err)
	}

	entries := []footprint.Entry{
		{Path: "/lib64", Type: "dir", Mode: 0o755},
		{Path: "/usr/lib", Type: "dir", Mode: 0o755},
		{Path: "/usr/lib/ld-linux-x86-64.so.2", Type: "file", Mode: 0o555},
		{Path: "/usr/lib/libc-2.40.so", Type: "file", Mode: 0o555},
		{Path: "/usr/lib/libncursesw.so.6.5", Type: "file", Mode: 0o555},
		{Path: "/lib64/ld-linux-x86-64.so.2", Type: "symlink", Mode: 0o777, LinkTarget: "../usr/lib/ld-linux-x86-64.so.2"},
		{Path: "/usr/lib/libc.so.6", Type: "symlink", Mode: 0o777, LinkTarget: "libc-2.40.so"},
		{Path: "/usr/lib/libtinfo.so.6", Type: "symlink", Mode: 0o777, LinkTarget: "libncursesw.so.6.5"},
		{Path: "/etc/mtab", Type: "symlink", Mode: 0o777, LinkTarget: "../proc/self/mounts"},
	}
	rec := Record{Pkgname: "glibc", Pkgver: "2.40", Pkgrel: "1", USEHash: "hash-a"}
	if err := Write(root, rec, entries); err != nil {
		t.Fatal(err)
	}
	if ok, reason := VerifyInstalled(root, "glibc", "2.40", "1", "hash-a"); !ok {
		t.Fatalf("expected healthy glibc rootfs to verify, got: %s", reason)
	}

	// Break libtinfo.so.6 target -> must detect broken symlink.
	if err := os.Chmod(ncursesReal, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ncursesReal); err != nil {
		t.Fatal(err)
	}
	ok, reason := VerifyInstalled(root, "glibc", "2.40", "1", "hash-a")
	if ok || !strings.Contains(reason, "broken symlink /usr/lib/libtinfo.so.6") {
		t.Fatalf("expected broken symlink error, got ok=%v reason=%q", ok, reason)
	}

	// Restore ncursesReal and test symlink loop detection.
	if err := os.WriteFile(ncursesReal, []byte("elf"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ncursesReal, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "usr", "lib", "libc.so.6")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libc.so.6", filepath.Join(root, "usr", "lib", "libc.so.6")); err != nil {
		t.Fatal(err)
	}
	entries[6].LinkTarget = "libc.so.6"
	if err := Write(root, rec, entries); err != nil {
		t.Fatal(err)
	}
	ok, reason = VerifyInstalled(root, "glibc", "2.40", "1", "hash-a")
	if ok || !strings.Contains(reason, "symlink loop") {
		t.Fatalf("expected symlink loop error, got ok=%v reason=%q", ok, reason)
	}
}

func TestVerifyInstalledDetectsModeDrift(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "usr", "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libFile := filepath.Join(libDir, "libdemo.so")
	if err := os.WriteFile(libFile, []byte("so"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(libFile, 0o555); err != nil {
		t.Fatal(err)
	}
	entries := []footprint.Entry{
		{Path: "/usr/lib", Type: "dir", Mode: 0o755},
		{Path: "/usr/lib/libdemo.so", Type: "file", Mode: 0o555},
	}
	rec := Record{Pkgname: "demo", Pkgver: "1", Pkgrel: "1", USEHash: "h1"}
	if err := Write(root, rec, entries); err != nil {
		t.Fatal(err)
	}
	if ok, reason := VerifyInstalled(root, "demo", "1", "1", "h1"); !ok {
		t.Fatalf("unexpected verify failure: %s", reason)
	}
	// Induce mode drift (0555 -> 0644).
	if err := os.Chmod(libFile, 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := VerifyInstalled(root, "demo", "1", "1", "h1")
	if ok || !strings.Contains(reason, "mode drift on /usr/lib/libdemo.so: got 0644, want 0555") {
		t.Fatalf("expected mode drift error, got ok=%v reason=%q", ok, reason)
	}
}

func TestDBLockRejectsConcurrentExclusiveLock(t *testing.T) {
	root := t.TempDir()
	lock1, err := Lock(root, true)
	if err != nil {
		t.Fatalf("first exclusive lock failed: %v", err)
	}
	defer lock1.Unlock()

	if _, err := Lock(root, true); err == nil || !strings.Contains(err.Error(), "locked by another process") {
		t.Fatalf("expected concurrent exclusive lock to fail, got: %v", err)
	}
	if err := lock1.Unlock(); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}
	lock2, err := Lock(root, true)
	if err != nil {
		t.Fatalf("lock after unlock failed: %v", err)
	}
	_ = lock2.Unlock()
}
