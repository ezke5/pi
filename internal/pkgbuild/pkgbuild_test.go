package pkgbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PKGBUILD")
	content := `pkgname="demo"
pkgver=1.2
pkgrel=3
source=("demo.tar.gz")
sha256sums=("SKIP")
depends=("glibc")
build() {
  make
}
package() {
  make DESTDIR="$pkgdir" install
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pkgname != "demo" || r.Pkgver != "1.2" || r.Pkgrel != "3" {
		t.Fatalf("unexpected recipe: %+v", r)
	}
	if len(r.Source) != 1 || len(r.Depends) != 1 {
		t.Fatalf("unexpected arrays: %+v", r)
	}
	if r.BuildFunction == "" || r.PackageFunction == "" {
		t.Fatal("missing functions")
	}
}

func TestReadNestedBlocksAndHeredoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PKGBUILD")
	content := `pkgname=demo
pkgver=1
source=("demo.tar.gz")
noextract=("keep.tar.gz")
build() {
  if true; then
    for item in one two; do
      echo "$item"
    done
  fi
  cat << 'EOF'
}
EOF
}
package() {
  install -Dm644 "$srcdir/demo" "$pkgdir/usr/share/demo"
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.BuildFunction, "for item") || !strings.Contains(r.BuildFunction, "EOF") {
		t.Fatalf("nested/heredoc body truncated: %q", r.BuildFunction)
	}
	if len(r.Noextract) != 1 || r.Noextract[0] != "keep.tar.gz" {
		t.Fatalf("unexpected noextract: %#v", r.Noextract)
	}
}

func TestFindRepositorySubdirectories(t *testing.T) {
	ports := t.TempDir()
	for _, repo := range []string{"extra", "core"} {
		dir := filepath.Join(ports, repo, "demo")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "PKGBUILD"), []byte("pkgname=demo\npkgver=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Find(ports, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(filepath.Dir(got))) != "core" {
		t.Fatalf("expected deterministic core result, got %q", got)
	}
}
