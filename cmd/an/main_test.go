package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBootstrapDryRunAndCreatesSkeleton(t *testing.T) {
	root := t.TempDir()
	bootstrapRoot := filepath.Join(root, "anrix-root")
	cfgPath := filepath.Join(root, "pi.ini")
	if err := os.WriteFile(cfgPath, []byte("[paths]\nroot = "+bootstrapRoot+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"--config", cfgPath, "--dry-run", "bootstrap"}, nil, &out, &out); err != nil {
		t.Fatalf("dry-run bootstrap failed: %v", err)
	}
	if _, err := os.Stat(bootstrapRoot); !os.IsNotExist(err) {
		t.Fatalf("dry-run bootstrap created target directory: %v", err)
	}

	out.Reset()
	if err := run([]string{"--config", cfgPath, "bootstrap"}, nil, &out, &out); err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, out.String())
	}
	for _, rel := range []string{"usr/bin", "usr/lib", "usr/lib64", "etc/passwd", "etc/group", "dev", "dev/pts", "dev/shm", "proc", "sys", "run", "root", "tmp", "var", "var/tmp"} {
		if _, err := os.Stat(filepath.Join(bootstrapRoot, rel)); err != nil {
			t.Fatalf("missing bootstrapped path %s: %v", rel, err)
		}
	}

	// Explicit --root overrides root from --config.
	overrideRoot := filepath.Join(root, "override-root")
	out.Reset()
	if err := run([]string{"--config", cfgPath, "--root", overrideRoot, "bootstrap"}, nil, &out, &out); err != nil {
		t.Fatalf("override bootstrap failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(overrideRoot, "etc", "passwd")); err != nil {
		t.Fatalf("override root was not bootstrapped: %v", err)
	}
}

func TestRunChrootDryRunAndFlagSplitting(t *testing.T) {
	global, rest := splitFlags([]string{"--root", "/srv/anrix", "chroot", "/bin/bash", "--noprofile", "--dry-run"})
	if strings.Join(global, " ") != "--root /srv/anrix" {
		t.Fatalf("chroot global flags = %v", global)
	}
	if strings.Join(rest, " ") != "chroot /bin/bash --noprofile --dry-run" {
		t.Fatalf("chroot rest args = %v", rest)
	}

	target := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"--root", target, "--dry-run", "chroot", "/bin/sh", "-c", "echo ok"}, nil, &out, &out); err != nil {
		t.Fatalf("dry-run chroot failed: %v", err)
	}
	if !strings.Contains(out.String(), "dry-run: chroot "+target+" (/bin/sh -c echo ok)") {
		t.Fatalf("unexpected dry-run chroot output: %s", out.String())
	}

	if err := run([]string{"--root", "/", "--allow-host-root", "chroot", "/bin/sh"}, nil, &out, &out); err == nil {
		t.Fatal("expected chroot into host root / to be rejected")
	}
}

func TestRunHelpAndUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"help"}, nil, &out, &out); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out.String(), "bootstrap") || !strings.Contains(out.String(), "chroot") {
		t.Fatalf("unexpected help output: %s", out.String())
	}
	if err := run([]string{"unknown"}, nil, &out, &out); err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}
