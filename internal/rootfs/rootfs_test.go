package rootfs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestBootstrapCreatesMinimalFHSSkeletonAndPreservesExistingEtc(t *testing.T) {
	root := filepath.Join(t.TempDir(), "crux-root")
	if err := Bootstrap(root); err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	for _, rel := range []string{"usr/bin", "usr/lib", "usr/lib64", "etc", "dev", "proc", "sys", "tmp", "var"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s: info=%v err=%v", rel, info, err)
		}
	}
	tmpInfo, err := os.Stat(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if tmpInfo.Mode()&os.ModeSticky == 0 {
		t.Fatalf("expected sticky bit on tmp, got mode %v", tmpInfo.Mode())
	}
	passwd, err := os.ReadFile(filepath.Join(root, "etc", "passwd"))
	if err != nil || !strings.Contains(string(passwd), "root:x:0:0:") {
		t.Fatalf("unexpected /etc/passwd: %q err=%v", passwd, err)
	}
	group, err := os.ReadFile(filepath.Join(root, "etc", "group"))
	if err != nil || !strings.Contains(string(group), "root:x:0:") {
		t.Fatalf("unexpected /etc/group: %q err=%v", group, err)
	}

	// Custom /etc/passwd and /etc/group must not be overwritten on repeat bootstrap.
	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte("custom-passwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(root); err != nil {
		t.Fatalf("second Bootstrap failed: %v", err)
	}
	passwd, err = os.ReadFile(filepath.Join(root, "etc", "passwd"))
	if err != nil || string(passwd) != "custom-passwd\n" {
		t.Fatalf("existing /etc/passwd was overwritten: %q err=%v", passwd, err)
	}
}

func TestEnsureSkeletonIfEmpty(t *testing.T) {
	emptyRoot := t.TempDir()
	created, err := EnsureSkeletonIfEmpty(emptyRoot)
	if err != nil || !created {
		t.Fatalf("empty root: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(filepath.Join(emptyRoot, "etc", "passwd")); err != nil {
		t.Fatalf("expected /etc/passwd after empty root bootstrap: %v", err)
	}
	created, err = EnsureSkeletonIfEmpty(emptyRoot)
	if err != nil || created {
		t.Fatalf("non-empty root should not re-bootstrap: created=%v err=%v", created, err)
	}
}

func TestChrootMountsAndUnmountsInReverseOrder(t *testing.T) {
	root := t.TempDir()
	if err := Bootstrap(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "bash"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostResolv := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(hostResolv, []byte("nameserver 1.1.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mounts, unmounts []string
	var executedCmd []string
	runner := ChrootRunner{
		HostResolvConf: hostResolv,
		Mount: func(spec MountSpec, targetPath string) error {
			mounts = append(mounts, spec.Target)
			return nil
		},
		Unmount: func(targetPath string) error {
			rel, _ := filepath.Rel(root, targetPath)
			unmounts = append(unmounts, filepath.ToSlash(rel))
			return nil
		},
		Exec: func(r string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
			executedCmd = append([]string(nil), cmd...)
			return nil
		},
	}

	var out bytes.Buffer
	if err := runner.Run(root, nil, nil, &out, &out); err != nil {
		t.Fatalf("ChrootRunner.Run failed: %v", err)
	}
	wantMounts := []string{"dev", "dev/pts", "proc", "sys"}
	wantUnmounts := []string{"sys", "proc", "dev/pts", "dev"}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Fatalf("mounts = %v, want %v", mounts, wantMounts)
	}
	if !reflect.DeepEqual(unmounts, wantUnmounts) {
		t.Fatalf("unmounts = %v, want %v", unmounts, wantUnmounts)
	}
	wantCmd := []string{"/bin/bash", "--noprofile", "--norc"}
	if !reflect.DeepEqual(executedCmd, wantCmd) {
		t.Fatalf("default cmd = %v, want %v", executedCmd, wantCmd)
	}
	resolv, err := os.ReadFile(filepath.Join(root, "etc", "resolv.conf"))
	if err != nil || string(resolv) != "nameserver 1.1.1.1\n" {
		t.Fatalf("resolv.conf not copied: %q err=%v", resolv, err)
	}

	// Without bash, default falls back to /bin/sh.
	if err := os.Remove(filepath.Join(root, "bin", "bash")); err != nil {
		t.Fatal(err)
	}
	if got := DefaultChrootCommand(root); !reflect.DeepEqual(got, []string{"/bin/sh"}) {
		t.Fatalf("fallback cmd = %v, want [/bin/sh]", got)
	}
}

func TestChrootUnmountsOnMountErrorAndCommandError(t *testing.T) {
	t.Run("partial mount failure unmounts mounted targets in reverse", func(t *testing.T) {
		root := t.TempDir()
		var unmounts []string
		execCalled := false
		runner := ChrootRunner{
			Mount: func(spec MountSpec, targetPath string) error {
				if spec.Target == "proc" {
					return errors.New("proc mount denied")
				}
				return nil
			},
			Unmount: func(targetPath string) error {
				rel, _ := filepath.Rel(root, targetPath)
				unmounts = append(unmounts, filepath.ToSlash(rel))
				return nil
			},
			Exec: func(r string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
				execCalled = true
				return nil
			},
		}
		err := runner.Run(root, []string{"/bin/sh"}, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "proc mount denied") {
			t.Fatalf("expected mount error, got: %v", err)
		}
		if execCalled {
			t.Fatal("chroot command should not execute after mount failure")
		}
		wantUnmounts := []string{"dev/pts", "dev"}
		if !reflect.DeepEqual(unmounts, wantUnmounts) {
			t.Fatalf("unmounts after partial mount = %v, want %v", unmounts, wantUnmounts)
		}
	})

	t.Run("command error still unmounts all in reverse and surfaces unmount error", func(t *testing.T) {
		root := t.TempDir()
		var unmounts []string
		runner := ChrootRunner{
			Mount: func(spec MountSpec, targetPath string) error { return nil },
			Unmount: func(targetPath string) error {
				rel, _ := filepath.Rel(root, targetPath)
				name := filepath.ToSlash(rel)
				unmounts = append(unmounts, name)
				if name == "proc" {
					return errors.New("target busy")
				}
				return nil
			},
			Exec: func(r string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
				return errors.New("exit status 42")
			},
		}
		err := runner.Run(root, []string{"/bin/sh", "-c", "exit 42"}, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "exit status 42") || !strings.Contains(err.Error(), "target busy") {
			t.Fatalf("expected combined exec and unmount errors, got: %v", err)
		}
		wantUnmounts := []string{"sys", "proc", "dev/pts", "dev"}
		if !reflect.DeepEqual(unmounts, wantUnmounts) {
			t.Fatalf("unmounts = %v, want %v", unmounts, wantUnmounts)
		}
	})

	t.Run("host root is rejected", func(t *testing.T) {
		runner := DefaultChrootRunner()
		if err := runner.Run("/", []string{"/bin/sh"}, nil, nil, nil); err == nil {
			t.Fatal("expected host root chroot to be rejected")
		}
	})
}
