package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/state"
)

func TestBuildExportsMetadataAndExtractsSource(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	cache := filepath.Join(root, "cache")
	for _, dir := range []string{ports, work, cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	recipeDir := filepath.Join(ports, "demo")
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "extracted.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(cache, "demo.tar.gz")
	if output, err := exec.Command("tar", "-czf", source, "-C", sourceDir, "extracted.txt").CombinedOutput(); err != nil {
		t.Fatalf("create source archive: %v (%s)", err, output)
	}
	if err := os.Rename(source, filepath.Join(recipeDir, "demo.tar.gz")); err != nil {
		t.Fatal(err)
	}
	pkgbuild := `pkgname=demo
pkgver=1
pkgrel=1
source=("demo.tar.gz")
build() {
  test -f "$srcdir/extracted.txt"
}
package() {
  mkdir -p "$pkgdir/usr/share"
  printf '%s-%s-%s' "$pkgname" "$pkgver" "$pkgrel" > "$pkgdir/usr/share/metadata"
}
`
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte(pkgbuild), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{"--ports", ports, "--work", work, "--cache", cache, "build", "demo"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "built "+filepath.Join(cache, "demo-1-1.pkg.tar.zst")) {
		t.Fatalf("unexpected build output: %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(work, "demo-1", "src", "extracted.txt")); err != nil {
		t.Fatalf("source was not extracted: %v", err)
	}
}

func TestDryRunInstallResolvesDependenciesWithoutCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	cache := filepath.Join(root, "cache")
	target := filepath.Join(root, "target")
	for _, name := range []string{"base", "app"} {
		dir := filepath.Join(ports, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		depends := ""
		if name == "app" {
			depends = "depends=(base)\n"
		}
		content := "pkgname=" + name + "\npkgver=1\npkgrel=1\n" + depends
		if err := os.WriteFile(filepath.Join(dir, "PKGBUILD"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := Run([]string{"--ports", ports, "--work", work, "--cache", cache, "--root", target, "--dry-run", "install", "app"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1. base-1-1") || !strings.Contains(output.String(), "2. app-1-1") {
		t.Fatalf("unexpected plan: %s", output.String())
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("dry-run created work directory: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run created target root: %v", err)
	}
}

func TestSplitFlagsAcceptsFlagsBeforeAndAfterSubcommand(t *testing.T) {
	global, rest := splitFlags([]string{"--root", "/tmp/crux", "install", "base", "--dry-run", "--force"})
	if strings.Join(global, " ") != "--root /tmp/crux --dry-run --force" {
		t.Fatalf("before flags: global=%v rest=%v", global, rest)
	}
	if strings.Join(rest, " ") != "install base" {
		t.Fatalf("before rest: %v", rest)
	}
	global, rest = splitFlags([]string{"install", "base", "--ports", "/tmp/ports", "--root=/tmp/crux", "--reuse=false"})
	if strings.Join(global, " ") != "--ports /tmp/ports --root=/tmp/crux --reuse=false" {
		t.Fatalf("after flags: global=%v rest=%v", global, rest)
	}
	if strings.Join(rest, " ") != "install base" {
		t.Fatalf("after rest: %v", rest)
	}
}

func TestInstallPackageNameResolvesNewestCachedArchive(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	target := filepath.Join(root, "root")
	cache := filepath.Join(root, "cache")
	ports := filepath.Join(root, "ports")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ports, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "usr/bin/demo"), []byte("demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := footprint.Generate(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(cache, "demo-1.0-1.pkg.tar.zst")
	newer := filepath.Join(cache, "demo-2.0-1.pkg.tar.zst")
	if err := archive.Create(pkgDir, older, "demo", "1.0", "1", entries); err != nil {
		t.Fatal(err)
	}
	if err := archive.Create(pkgDir, newer, "demo", "2.0", "1", entries); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{"install", "--root", target, "--ports", ports, "--cache", cache, "--dry-run", "demo"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "demo-2.0-1.pkg.tar.zst") {
		t.Fatalf("did not select newest archive: %s", output.String())
	}
}

func TestUpgradeRemovesOrphanPreservesBackupAndAvoidsSelfConflict(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	target := filepath.Join(root, "root")
	cache := filepath.Join(root, "cache")
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	for _, dir := range []string{target, cache, ports, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	makeArchive := func(version string, backup []string) (string, []footprint.Entry) {
		pkgDir := filepath.Join(root, "pkg-"+version)
		if err := os.MkdirAll(filepath.Join(pkgDir, "usr/bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "usr/bin/common-tool"), []byte(version), 0o755); err != nil {
			t.Fatal(err)
		}
		if version == "1.0" {
			if err := os.WriteFile(filepath.Join(pkgDir, "usr/bin/base-only"), []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(pkgDir, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "etc/base.conf"), []byte(version), 0o644); err != nil {
			t.Fatal(err)
		}
		entries, err := footprint.Generate(pkgDir)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(cache, "base-"+version+"-1.pkg.tar.zst")
		if err := archive.Create(pkgDir, path, "base", version, "1", entries, backup); err != nil {
			t.Fatal(err)
		}
		return path, entries
	}
	v1, _ := makeArchive("1.0", []string{"etc/base.conf"})
	var output bytes.Buffer
	if err := Run([]string{"--root", target, "--cache", cache, "--ports", ports, "install", v1}, &output, &output); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(target, "etc/base.conf")
	if err := os.WriteFile(conf, []byte("local modification"), 0o644); err != nil {
		t.Fatal(err)
	}
	v2, _ := makeArchive("2.0", []string{"etc/base.conf"})
	output.Reset()
	if err := Run([]string{"install", "--root", target, "--cache", cache, "--ports", ports, v2}, &output, &output); err != nil {
		t.Fatalf("upgrade failed: %v (%s)", err, output.String())
	}
	if _, err := os.Lstat(filepath.Join(target, "usr/bin/base-only")); !os.IsNotExist(err) {
		t.Fatalf("stale base-only file remains: %v", err)
	}
	data, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local modification" {
		t.Fatalf("backup config overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(target, "usr/bin/common-tool")); err != nil {
		t.Fatalf("common tool missing: %v", err)
	}
}

func TestRemoveBlocksReverseDependencyWithoutForce(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "var/lib/pi/packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseEntries := []footprint.Entry{{Path: "/usr/bin/base", Type: "file", Mode: 0o755}}
	if err := state.Write(target, state.Record{Pkgname: "base", Pkgver: "1", Pkgrel: "1"}, baseEntries); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(target, state.Record{Pkgname: "app", Pkgver: "1", Pkgrel: "1", Depends: []string{"base"}}, nil); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Run([]string{"--root", target, "remove", "base"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "app") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("remove error = %v", err)
	}
	if err := Run([]string{"remove", "--root", target, "--force", "base"}, &output, &output); err != nil {
		t.Fatalf("forced remove failed: %v", err)
	}
}
