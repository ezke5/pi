package deps

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveRuntimeExcludesMakeDepends(t *testing.T) {
	ports := t.TempDir()
	writeRecipe := func(name, body string) {
		dir := filepath.Join(ports, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "PKGBUILD"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRecipe("glibc", "pkgname=glibc\npkgver=2.40\npkgrel=1\n")
	writeRecipe("meson", "pkgname=meson\npkgver=1.6\npkgrel=1\n")
	writeRecipe("wlroots", "pkgname=wlroots\npkgver=0.18\npkgrel=1\ndepends=(glibc)\nmakedepends=(meson)\n")

	r := Resolver{Ports: ports}
	rt, err := r.ResolveRuntime("wlroots")
	if err != nil {
		t.Fatalf("ResolveRuntime failed: %v", err)
	}
	if !reflect.DeepEqual(rt.Order, []string{"glibc", "wlroots"}) {
		t.Fatalf("runtime order = %v, want [glibc wlroots]", rt.Order)
	}
	full, err := r.Resolve("wlroots")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !reflect.DeepEqual(full.Order, []string{"glibc", "meson", "wlroots"}) {
		t.Fatalf("build order = %v, want [glibc meson wlroots]", full.Order)
	}
}
