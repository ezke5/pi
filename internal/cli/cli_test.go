package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
