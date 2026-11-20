// Package config loads pi configuration.
package config

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultDownloadTimeout = 10 * time.Minute

type Config struct {
	Ports           string        `ini:"ports"`
	Work            string        `ini:"work"`
	Temp            string        `ini:"temp"`
	Cache           string        `ini:"cache"`
	Binpkgs         string        `ini:"binpkgs"`
	Index           string        `ini:"index"`
	RepoURL         string        `ini:"repo_url"`
	Arch            string        `ini:"arch"`
	Root            string        `ini:"root"`
	DownloadTimeout time.Duration `ini:"download_timeout"`
	Config          string        `ini:"-"`
	Build           BuildConfig   `ini:"-"`
	Use             UseConfig     `ini:"-"`
	Jobs            int           `ini:"-"`
	CFLAGS          string        `ini:"-"`
	CXXFLAGS        string        `ini:"-"`
	LDFLAGS         string        `ini:"-"`
	STRIP           string        `ini:"-"`
}

type BuildConfig struct {
	Jobs            int
	CFlags          string
	CFLAGS          string
	CXXFlags        string
	CXXFLAGS        string
	LdFlags         string
	LDFLAGS         string
	Strip           string
	DownloadTimeout time.Duration
}

type UseConfig struct {
	Flags          []string
	PackageUseFile string
	PackageUse     map[string][]string
}

type USEState struct {
	Enabled   []string
	Disabled  []string
	Canonical string
	Hash      string
}

func Defaults() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	build := BuildConfig{
		Jobs: runtime.NumCPU(), CFlags: "", CFLAGS: "", CXXFlags: "", CXXFLAGS: "", LdFlags: "", LDFLAGS: "", Strip: "true",
		DownloadTimeout: DefaultDownloadTimeout,
	}
	use := UseConfig{PackageUse: map[string][]string{}}
	c := Config{
		Ports:           filepath.Join(".", "ports"),
		Work:            filepath.Join(".", "work"),
		Temp:            filepath.Join(".", "temp"),
		Cache:           filepath.Join(home, ".pkg"),
		Binpkgs:         filepath.Join(home, ".pkg"),
		Index:           filepath.Join(home, ".pkg", "index.json"),
		Arch:            "x86_64",
		Root:            "/",
		DownloadTimeout: DefaultDownloadTimeout,
		Build:           build, Use: use,
		Jobs: build.Jobs, CFLAGS: "", CXXFLAGS: "", LDFLAGS: "", STRIP: "true",
	}
	return c
}

// Load reads INI-style key = value configuration. Keys may be in [pi],
// [paths], or at the top level. CLI overrides should be applied after Load.
func Load(path string) (Config, error) {
	c := Defaults()
	if path == "" {
		return c, nil
	}
	c.Config = path
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	section := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("parse config %q line %d: expected key = value", path, lineNo)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(strings.Trim(value, `"'`))
		if section != "" && section != "pi" && section != "paths" && section != "build" && section != "use" {
			continue
		}
		if section == "build" {
			switch key {
			case "jobs":
				fmt.Sscanf(value, "%d", &c.Build.Jobs)
				c.Jobs = c.Build.Jobs
			case "cflags":
				c.Build.CFlags, c.Build.CFLAGS, c.CFLAGS = value, value, value
			case "cxxflags":
				c.Build.CXXFlags, c.Build.CXXFLAGS, c.CXXFLAGS = value, value, value
			case "ldflags":
				c.Build.LdFlags, c.Build.LDFLAGS, c.LDFLAGS = value, value, value
			case "strip":
				c.Build.Strip, c.STRIP = value, value
			case "download_timeout", "download-timeout":
				d, parseErr := parseDurationSetting(value)
				if parseErr != nil {
					return c, fmt.Errorf("parse config %q line %d (%s): %w", path, lineNo, key, parseErr)
				}
				c.Build.DownloadTimeout, c.DownloadTimeout = d, d
			}
			continue
		}
		if section == "use" {
			switch key {
			case "flags":
				c.Use.Flags = strings.Fields(value)
			case "package_use_file", "package-use-file", "packageuse":
				c.Use.PackageUseFile = value
				if !filepath.IsAbs(value) {
					c.Use.PackageUseFile = filepath.Join(filepath.Dir(path), value)
				}
			}
			continue
		}
		switch key {
		case "ports", "portsdir", "port_dir", "portdir":
			c.Ports = value
		case "work", "workdir":
			c.Work = value
		case "temp", "tempdir":
			c.Temp = value
		case "cache", "cachedir":
			c.Cache = value
		case "binpkgs", "binpkgdir", "package", "packages":
			c.Binpkgs = value
		case "index", "indexfile", "repository_index":
			c.Index = value
		case "repo_url", "repository_url", "url":
			c.RepoURL = value
		case "arch", "architecture":
			c.Arch = value
		case "root", "target_root", "target-root":
			c.Root = value
		case "download_timeout", "download-timeout":
			d, parseErr := parseDurationSetting(value)
			if parseErr != nil {
				return c, fmt.Errorf("parse config %q line %d (%s): %w", path, lineNo, key, parseErr)
			}
			c.DownloadTimeout, c.Build.DownloadTimeout = d, d
		}
	}
	if err := scanner.Err(); err != nil {
		return c, fmt.Errorf("read config %q: %w", path, err)
	}
	if c.Binpkgs == "" {
		c.Binpkgs = c.Cache
	}
	c.syncBuildConvenience()
	if c.Use.PackageUse == nil {
		c.Use.PackageUse = map[string][]string{}
	}
	if c.Use.PackageUseFile != "" {
		if err := c.loadPackageUse(); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c *Config) syncBuildConvenience() {
	if c.Build.CFLAGS == "" {
		c.Build.CFLAGS = c.Build.CFlags
	}
	if c.Build.CXXFLAGS == "" {
		c.Build.CXXFLAGS = c.Build.CXXFlags
	}
	if c.Build.LDFLAGS == "" {
		c.Build.LDFLAGS = c.Build.LdFlags
	}
	if c.Build.Jobs > 0 {
		c.Jobs = c.Build.Jobs
	}
	c.CFLAGS = c.Build.CFLAGS
	c.CXXFLAGS = c.Build.CXXFLAGS
	c.LDFLAGS = c.Build.LDFLAGS
	c.STRIP = c.Build.Strip
	if c.Build.DownloadTimeout > 0 {
		c.DownloadTimeout = c.Build.DownloadTimeout
	} else if c.DownloadTimeout > 0 {
		c.Build.DownloadTimeout = c.DownloadTimeout
	}
}

func parseDurationSetting(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0, fmt.Errorf("duration %q must be positive", value)
		}
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (expected Go duration like 10m or integer seconds): %w", value, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", value)
	}
	return d, nil
}

func (c *Config) loadPackageUse() error {
	f, err := os.Open(c.Use.PackageUseFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open package.use file %q: %w", c.Use.PackageUseFile, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fmt.Errorf("parse package.use file %q line %d: expected package and USE flags", c.Use.PackageUseFile, lineNo)
		}
		pkg := fields[0]
		c.Use.PackageUse[pkg] = append(c.Use.PackageUse[pkg], fields[1:]...)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read package.use file %q: %w", c.Use.PackageUseFile, err)
	}
	return nil
}

// ResolveUSE returns the effective enabled and disabled USE flags for pkgname.
func (c Config) ResolveUSE(pkgname string) USEState {
	state := USEState{}
	enabled := map[string]bool{}
	knownDisabled := map[string]bool{}
	apply := func(tokens []string) {
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if strings.HasPrefix(token, "-") {
				name := strings.TrimPrefix(token, "-")
				if name != "" {
					enabled[name] = false
					knownDisabled[name] = true
				}
			} else {
				enabled[token] = true
				delete(knownDisabled, token)
			}
		}
	}
	apply(c.Use.Flags)
	if overrides := c.Use.PackageUse[pkgname]; len(overrides) > 0 {
		apply(overrides)
	}
	for flag, on := range enabled {
		if on {
			state.Enabled = append(state.Enabled, flag)
		}
	}
	for flag := range knownDisabled {
		if !enabled[flag] {
			state.Disabled = append(state.Disabled, flag)
		}
	}
	sort.Strings(state.Enabled)
	sort.Strings(state.Disabled)
	state.Canonical = "enabled=" + strings.Join(state.Enabled, ",") + ";disabled=" + strings.Join(state.Disabled, ",")
	sum := sha256.Sum256([]byte(state.Canonical))
	state.Hash = hex.EncodeToString(sum[:])
	return state
}

func (c Config) EffectiveUSE(pkgname string) USEState      { return c.ResolveUSE(pkgname) }
func (c Config) EffectiveUSEFlags(pkgname string) []string { return c.ResolveUSE(pkgname).Enabled }
func (c Config) USEHash(pkgname string) string             { return c.ResolveUSE(pkgname).Hash }

// Merge applies non-empty CLI overrides to c.
func (c *Config) Merge(ports, work, temp, cache, root string) {
	if ports != "" {
		c.Ports = ports
	}
	if work != "" {
		c.Work = work
	}
	if temp != "" {
		c.Temp = temp
	}
	if cache != "" {
		c.Cache = cache
		c.Binpkgs = cache
	}
	if root != "" {
		c.Root = root
	}
	c.syncBuildConvenience()
}

// EnsureDirs creates writable working directories.
func (c Config) EnsureDirs() error {
	for name, dir := range map[string]string{
		"ports": c.Ports, "work": c.Work, "temp": c.Temp,
		"cache": c.Cache,
	} {
		if dir == "" {
			return fmt.Errorf("configuration path %q is empty", name)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s directory %q: %w", name, dir, err)
		}
	}
	return nil
}
