package pkgbuild

import (
	"os"
	"path/filepath"
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
