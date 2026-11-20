// Package config loads pi configuration.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Ports   string `ini:"ports"`
	Work    string `ini:"work"`
	Temp    string `ini:"temp"`
	Cache   string `ini:"cache"`
	Binpkgs string `ini:"binpkgs"`
	Index   string `ini:"index"`
	RepoURL string `ini:"repo_url"`
	Arch    string `ini:"arch"`
	Root    string `ini:"root"`
	Config  string `ini:"-"`
}

func Defaults() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return Config{
		Ports:   filepath.Join(".", "ports"),
		Work:    filepath.Join(".", "work"),
		Temp:    filepath.Join(".", "temp"),
		Cache:   filepath.Join(home, ".pkg"),
		Binpkgs: filepath.Join(home, ".pkg"),
		Index:   filepath.Join(home, ".pkg", "index.json"),
		Arch:    "x86_64",
		Root:    "/",
	}
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
		if section != "" && section != "pi" && section != "paths" {
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
		}
	}
	if err := scanner.Err(); err != nil {
		return c, fmt.Errorf("read config %q: %w", path, err)
	}
	if c.Binpkgs == "" {
		c.Binpkgs = c.Cache
	}
	return c, nil
}

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
