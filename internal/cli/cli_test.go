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
	"github.com/ezke5/pi/internal/pkgbuild"
	"github.com/ezke5/pi/internal/repoindex"
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

func TestSelectInstallArchivePrefersBinaryAndFallsBackToRecipeArchive(t *testing.T) {
	cache := t.TempDir()
	recipe := pkgbuild.Recipe{Pkgname: "demo", Pkgver: "2.0", Pkgrel: "1"}
	if path, source := selectInstallArchive(cache, recipe); path != filepath.Join(cache, recipe.ArchiveName()) || source != "build from PKGBUILD" {
		t.Fatalf("missing archive selection = %q, %q", path, source)
	}
	older := filepath.Join(cache, "demo-1.0-1.pkg.tar.zst")
	if err := os.WriteFile(older, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, source := selectInstallArchive(cache, recipe); path != older || source != "existing archive" {
		t.Fatalf("fallback archive selection = %q, %q", path, source)
	}
	exact := filepath.Join(cache, recipe.ArchiveName())
	if err := os.WriteFile(exact, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, source := selectInstallArchive(cache, recipe); path != exact || source != "existing archive" {
		t.Fatalf("exact archive selection = %q, %q", path, source)
	}
}

func TestIndexedChecksumMismatchFallsBackToPKGBUILD(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	recipeDir := filepath.Join(ports, "demo")
	cache := filepath.Join(root, "cache")
	work := filepath.Join(root, "work")
	target := filepath.Join(root, "target")
	indexPath := filepath.Join(root, "index.json")
	for _, dir := range []string{recipeDir, cache, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte(`pkgname=demo
pkgver=1
pkgrel=1
package() {
  mkdir -p "$pkgdir/usr/share"
  printf source-built > "$pkgdir/usr/share/demo"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(cache, "demo-1-1.pkg.tar.zst")
	if err := os.WriteFile(binaryPath, []byte("corrupt binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{
		Pkgname: "demo", Pkgver: "1", Pkgrel: "1", Arch: "x86_64",
		Filename: filepath.Base(binaryPath), SHA256: "not-the-cache-hash",
	})
	if err := repoindex.Save(indexPath, index); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{
		"--ports", ports, "--work", work, "--cache", cache, "--root", target,
		"--index", indexPath, "install", "demo",
	}, &output, &output); err != nil {
		t.Fatalf("install fallback failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "build from PKGBUILD") {
		t.Fatalf("missing source fallback output: %s", output.String())
	}
	data, err := os.ReadFile(filepath.Join(target, "usr/share/demo"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "source-built" {
		t.Fatalf("unexpected installed content: %q", data)
	}
}

func TestInstallFindsArchNamedIndexedBinary(t *testing.T) {
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	recipeDir := filepath.Join(ports, "hello")
	cache := filepath.Join(root, "cache")
	target := filepath.Join(root, "target")
	for _, dir := range []string{recipeDir, cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte("pkgname=hello\npkgver=1.0.0\npkgrel=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(cache, "hello-1.0.0-1-x86_64.pkg.tar.zst")
	if err := os.WriteFile(archivePath, []byte("indexed archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := repoindex.SHA256(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.json")
	index := repoindex.New()
	repoindex.Merge(&index, repoindex.Entry{
		Pkgname: "hello", Pkgver: "1.0.0", Pkgrel: "1", Arch: "x86_64",
		Filename: filepath.Base(archivePath), SHA256: sum,
	})
	if err := repoindex.Save(indexPath, index); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{
		"--ports", ports, "--cache", cache, "--root", target,
		"--index", indexPath, "--arch", "x86_64", "--dry-run", "install", "hello",
	}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "repository binary") || strings.Contains(output.String(), "build from PKGBUILD") {
		t.Fatalf("arch binary was not selected: %s", output.String())
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

func TestBuildRejectsAbsoluteSymlinkBeforePackaging(t *testing.T) {
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	cache := filepath.Join(root, "cache")
	recipeDir := filepath.Join(ports, "bash")
	for _, dir := range []string{recipeDir, work, cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	recipe := `pkgname=bash
pkgver=5.2.37
pkgrel=1
package() {
  mkdir -p "$pkgdir/usr/bin" "$pkgdir/bin"
  printf '#!/bin/sh\n' > "$pkgdir/usr/bin/bash"
  chmod 755 "$pkgdir/usr/bin/bash"
  ln -s /usr/bin/bash "$pkgdir/bin/bash"
}
`
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Run([]string{"--ports", ports, "--work", work, "--cache", cache, "build", "bash"}, &output, &output)
	if err == nil {
		t.Fatal("expected build to reject absolute symlink target")
	}
	for _, want := range []string{"absolute symlink targets are not allowed", "bin/bash", "/usr/bin/bash", "../usr/bin/bash"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("build error %q missing expected fragment %q", err.Error(), want)
		}
	}
	archivePath := filepath.Join(cache, "bash-5.2.37-1.pkg.tar.zst")
	if _, statErr := os.Stat(archivePath); !os.IsNotExist(statErr) {
		t.Fatalf("package archive should not have been finalized: %v", statErr)
	}
}

func TestBuildAcceptsRelativeSymlink(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	cache := filepath.Join(root, "cache")
	recipeDir := filepath.Join(ports, "bash")
	for _, dir := range []string{recipeDir, work, cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	recipe := `pkgname=bash
pkgver=5.2.37
pkgrel=1
package() {
  mkdir -p "$pkgdir/usr/bin" "$pkgdir/bin"
  printf '#!/bin/sh\n' > "$pkgdir/usr/bin/bash"
  chmod 755 "$pkgdir/usr/bin/bash"
  ln -s ../usr/bin/bash "$pkgdir/bin/bash"
}
`
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{"--ports", ports, "--work", work, "--cache", cache, "build", "bash"}, &output, &output); err != nil {
		t.Fatalf("build with relative symlink failed: %v\n%s", err, output.String())
	}
	archivePath := filepath.Join(cache, "bash-5.2.37-1.pkg.tar.zst")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("expected package archive %s to be created: %v", archivePath, err)
	}
}

func TestSmartSkipOnMatchingVersionAndUSEHashAndRebuild(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	ports := filepath.Join(root, "ports")
	work := filepath.Join(root, "work")
	temp := filepath.Join(root, "temp")
	cache := filepath.Join(root, "cache")
	target := filepath.Join(root, "target")
	recipeDir := filepath.Join(ports, "demo")
	buildCounter := filepath.Join(root, "build-count.log")
	for _, dir := range []string{recipeDir, work, temp, cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	recipe := `pkgname=demo
pkgver=1.0
pkgrel=1
package() {
  echo build >> "` + buildCounter + `"
  mkdir -p "$pkgdir/usr/share"
  printf '%s' "$PI_USE_FLAGS" > "$pkgdir/usr/share/demo-use"
}
`
	if err := os.WriteFile(filepath.Join(recipeDir, "PKGBUILD"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "pi.ini")
	baseCfg := "[paths]\nports = " + ports + "\nwork = " + work + "\ntemp = " + temp + "\ncache = " + cache + "\nbinpkgs = " + cache + "\nroot = " + target + "\n\n[use]\n"
	if err := os.WriteFile(cfgPath, []byte(baseCfg+"flags = readline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buildCount := func() int {
		data, err := os.ReadFile(buildCounter)
		if err != nil {
			return 0
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			return 0
		}
		return len(strings.Split(trimmed, "\n"))
	}

	// 1. Initial install builds and installs package.
	var output bytes.Buffer
	if err := Run([]string{"--config", cfgPath, "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("initial install failed: %v\n%s", err, output.String())
	}
	if buildCount() != 1 {
		t.Fatalf("buildCount after initial install = %d, want 1", buildCount())
	}
	gotUse, err := os.ReadFile(filepath.Join(target, "usr", "share", "demo-use"))
	if err != nil || string(gotUse) != "readline" {
		t.Fatalf("installed USE file = %q, err=%v", gotUse, err)
	}

	// 2. Repeat install with identical version + use_hash skips cleanly.
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("second install failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "skipping") || buildCount() != 1 {
		t.Fatalf("expected smart skip on repeat install: buildCount=%d output=%s", buildCount(), output.String())
	}

	// 3. Repeat build with matching installed version + use_hash in target root also skips.
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "build", "demo"}, &output, &output); err != nil {
		t.Fatalf("repeat build failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "skipping") || buildCount() != 1 {
		t.Fatalf("expected smart skip on repeat build: buildCount=%d output=%s", buildCount(), output.String())
	}

	// 4. Changing USE flags changes use_hash and triggers rebuild + reinstall.
	if err := os.WriteFile(cfgPath, []byte(baseCfg+"flags = readline nls\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("USE-changed install failed: %v\n%s", err, output.String())
	}
	if buildCount() != 2 {
		t.Fatalf("buildCount after USE change = %d, want 2\n%s", buildCount(), output.String())
	}
	gotUse, err = os.ReadFile(filepath.Join(target, "usr", "share", "demo-use"))
	if err != nil || string(gotUse) != "nls readline" {
		t.Fatalf("updated USE file = %q, err=%v", gotUse, err)
	}

	// 5. --rebuild forces rebuild and reinstall even when version + use_hash match.
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "--rebuild", "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("--rebuild install failed: %v\n%s", err, output.String())
	}
	if buildCount() != 3 {
		t.Fatalf("buildCount after --rebuild = %d, want 3\n%s", buildCount(), output.String())
	}

	// 6. Unreadable (permission error) or missing installed file disables skip and repairs.
	demoUseFile := filepath.Join(target, "usr", "share", "demo-use")
	if err := os.Chmod(demoUseFile, 0o000); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("permission-repair install failed: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "skipping") {
		t.Fatalf("expected reinstall when installed file has permission error, got: %s", output.String())
	}
	if _, err := os.ReadFile(demoUseFile); err != nil {
		t.Fatalf("repaired installed file should be readable: %v", err)
	}

	if err := os.Remove(demoUseFile); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Run([]string{"--config", cfgPath, "install", "demo"}, &output, &output); err != nil {
		t.Fatalf("repair install failed: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "skipping") {
		t.Fatalf("expected repair install when file is missing, got: %s", output.String())
	}
	if _, err := os.Stat(demoUseFile); err != nil {
		t.Fatalf("missing installed file was not restored: %v", err)
	}
}

func TestBootstrapSubcommandAndPreInstallSkeleton(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd is required for package archive creation")
	}
	root := t.TempDir()
	bootstrapRoot := filepath.Join(root, "bootstrapped")

	var output bytes.Buffer
	if err := Run([]string{"--root", bootstrapRoot, "--dry-run", "bootstrap"}, &output, &output); err != nil {
		t.Fatalf("dry-run bootstrap failed: %v", err)
	}
	if _, err := os.Stat(bootstrapRoot); !os.IsNotExist(err) {
		t.Fatalf("dry-run bootstrap created target directory: %v", err)
	}

	output.Reset()
	if err := Run([]string{"--root", bootstrapRoot, "bootstrap"}, &output, &output); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	for _, rel := range []string{"usr/bin", "usr/lib", "usr/lib64", "etc/passwd", "etc/group", "dev", "proc", "sys", "tmp", "var"} {
		if _, err := os.Stat(filepath.Join(bootstrapRoot, rel)); err != nil {
			t.Fatalf("missing bootstrapped path %s: %v", rel, err)
		}
	}

	// Installing into an empty target root automatically creates the FHS skeleton.
	installRoot := filepath.Join(root, "install-target")
	pkgDir := filepath.Join(root, "pkg")
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "hello"), []byte("hi"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := footprint.Generate(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(cache, "hello-1-1.pkg.tar.zst")
	if err := archive.Create(pkgDir, archivePath, "hello", "1", "1", entries); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Run([]string{"--root", installRoot, "--cache", cache, "install", archivePath}, &output, &output); err != nil {
		t.Fatalf("install into empty root failed: %v\n%s", err, output.String())
	}
	for _, rel := range []string{"usr/bin/hello", "usr/lib64", "etc/passwd", "etc/group", "proc", "sys"} {
		if _, err := os.Stat(filepath.Join(installRoot, rel)); err != nil {
			t.Fatalf("expected skeleton/installed path %s: %v", rel, err)
		}
	}
}

func TestChrootSubcommandDryRunAndFlagSplitting(t *testing.T) {
	global, rest := splitFlags([]string{"--root", "/srv/crux", "chroot", "/bin/bash", "--noprofile", "--force"})
	if strings.Join(global, " ") != "--root /srv/crux" {
		t.Fatalf("chroot global flags = %v", global)
	}
	if strings.Join(rest, " ") != "chroot /bin/bash --noprofile --force" {
		t.Fatalf("chroot rest args = %v", rest)
	}

	target := t.TempDir()
	var output bytes.Buffer
	if err := Run([]string{"--root", target, "--dry-run", "chroot", "/bin/sh", "-c", "echo ok"}, &output, &output); err != nil {
		t.Fatalf("dry-run chroot failed: %v", err)
	}
	if !strings.Contains(output.String(), "dry-run: chroot "+target+" (/bin/sh -c echo ok)") {
		t.Fatalf("unexpected dry-run chroot output: %s", output.String())
	}

	if err := Run([]string{"--root", "/", "--allow-host-root", "chroot", "/bin/sh"}, &output, &output); err == nil {
		t.Fatal("expected chroot into host root / to be rejected")
	}
}
