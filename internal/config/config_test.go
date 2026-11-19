package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndCLIOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi.ini")
	if err := os.WriteFile(path, []byte("[paths]\nports = /srv/ports\nwork = /srv/work\ncache = /srv/pkg\nroot = /srv/crux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ports != "/srv/ports" || c.Work != "/srv/work" || c.Cache != "/srv/pkg" || c.Root != "/srv/crux" {
		t.Fatalf("unexpected config: %+v", c)
	}
	c.Merge("/cli/ports", "", "", "/cli/pkg", "/cli/root")
	if c.Ports != "/cli/ports" || c.Cache != "/cli/pkg" || c.Binpkgs != "/cli/pkg" || c.Root != "/cli/root" {
		t.Fatalf("CLI overrides not applied: %+v", c)
	}
}
