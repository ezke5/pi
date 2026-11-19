package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRecipe(t *testing.T, root, name string, depends ...string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "pkgname=" + name + "\npkgver=1\npkgrel=1\n"
	if len(depends) > 0 {
		content += "depends=(" + strings.Join(depends, " ") + ")\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "PKGBUILD"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDiamond(t *testing.T) {
	ports := t.TempDir()
	writeRecipe(t, ports, "base")
	writeRecipe(t, ports, "liba", "base")
	writeRecipe(t, ports, "libb", "base")
	writeRecipe(t, ports, "app", "liba", "libb")
	graph, err := (Resolver{Ports: ports}).Resolve("app")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"base", "liba", "libb", "app"}
	if strings.Join(graph.Order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", graph.Order, want)
	}
}

func TestResolveCycleAndMissingDependency(t *testing.T) {
	ports := t.TempDir()
	writeRecipe(t, ports, "a", "b")
	writeRecipe(t, ports, "b", "a")
	if _, err := (Resolver{Ports: ports}).Resolve("a"); err == nil || !strings.Contains(err.Error(), "a -> b -> a") {
		t.Fatalf("cycle error = %v", err)
	}
	ports = t.TempDir()
	writeRecipe(t, ports, "app", "missing")
	if _, err := (Resolver{Ports: ports}).Resolve("app"); err == nil || !strings.Contains(err.Error(), `missing dependency "missing" required by "app"`) {
		t.Fatalf("missing dependency error = %v", err)
	}
}
