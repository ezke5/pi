package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestUSEResolutionGlobalAndPackageOverride(t *testing.T) {
	dir := t.TempDir()
	packageUse := filepath.Join(dir, "package.use")
	if err := os.WriteFile(packageUse, []byte("demo wayland -zstd systemd\ndemo -systemd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pi.ini")
	if err := os.WriteFile(path, []byte("[build]\njobs = 7\ncflags = -O2 -pipe\ncxxflags = -O2 -pipe\nldflags = -Wl,--as-needed\nstrip = false\n\n[use]\nflags = wayland zstd -systemd\npackage_use_file = package.use\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Build.Jobs != 7 || c.CFLAGS != "-O2 -pipe" || c.Build.Strip != "false" {
		t.Fatalf("unexpected build config: %+v", c.Build)
	}
	state := c.ResolveUSE("demo")
	if len(state.Enabled) != 1 || state.Enabled[0] != "wayland" {
		t.Fatalf("unexpected enabled USE: %+v", state.Enabled)
	}
	if len(state.Disabled) != 2 || state.Disabled[0] != "systemd" || state.Disabled[1] != "zstd" {
		t.Fatalf("unexpected disabled USE: %+v", state.Disabled)
	}
	if state.Hash == "" || state.Canonical == "" {
		t.Fatalf("missing canonical USE state: %+v", state)
	}
	other := c.ResolveUSE("other")
	if len(other.Enabled) != 2 || len(other.Disabled) != 1 {
		t.Fatalf("unexpected global USE state: %+v", other)
	}
}

func TestDownloadTimeoutConfiguration(t *testing.T) {
	def := Defaults()
	if def.DownloadTimeout != DefaultDownloadTimeout || def.Build.DownloadTimeout != DefaultDownloadTimeout {
		t.Fatalf("unexpected default download timeout: config=%v build=%v", def.DownloadTimeout, def.Build.DownloadTimeout)
	}

	dir := t.TempDir()
	piIni := filepath.Join(dir, "pi-section.ini")
	if err := os.WriteFile(piIni, []byte("[pi]\ndownload_timeout = 7m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(piIni)
	if err != nil {
		t.Fatal(err)
	}
	if c.DownloadTimeout != 7*time.Minute || c.Build.DownloadTimeout != 7*time.Minute {
		t.Fatalf("[pi] download_timeout not loaded: config=%v build=%v", c.DownloadTimeout, c.Build.DownloadTimeout)
	}

	buildIni := filepath.Join(dir, "build-section.ini")
	if err := os.WriteFile(buildIni, []byte("[build]\njobs = 4\ndownload_timeout = 420\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(buildIni)
	if err != nil {
		t.Fatal(err)
	}
	if c.DownloadTimeout != 420*time.Second || c.Build.DownloadTimeout != 420*time.Second || c.Build.Jobs != 4 {
		t.Fatalf("[build] download_timeout not loaded: config=%v build=%v jobs=%d", c.DownloadTimeout, c.Build.DownloadTimeout, c.Build.Jobs)
	}

	badIni := filepath.Join(dir, "bad.ini")
	if err := os.WriteFile(badIni, []byte("[build]\ndownload_timeout = not-a-duration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(badIni); err == nil || !strings.Contains(err.Error(), "download_timeout") {
		t.Fatalf("expected invalid download_timeout error, got: %v", err)
	}
}
