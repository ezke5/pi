package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ezke5/pi/internal/repoindex"
)

func main() {
	fs := flag.NewFlagSet("pi-index", flag.ExitOnError)
	indexPath := fs.String("index", "index.json", "JSON index to create or update")
	packages := fs.String("packages", ".", "directory containing package archives")
	baseURL := fs.String("base-url", "", "base URL for package downloads")
	arch := fs.String("arch", defaultArch(), "package architecture")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := runIndex(*indexPath, *packages, *baseURL, *arch, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runIndex(indexPath, packages, baseURL, arch string, stderr io.Writer) error {
	if baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			return err
		}
	}
	index := repoindex.New()
	if _, err := os.Stat(indexPath); err == nil {
		var loadErr error
		index, loadErr = repoindex.Load(indexPath)
		if loadErr != nil {
			return loadErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(packages, "*.pkg.tar.zst"))
	if err != nil {
		return err
	}
	seen := 0
	for _, path := range paths {
		name, ver, rel, ok := parseArchive(filepath.Base(path))
		if !ok {
			fmt.Fprintf(stderr, "warning: ignoring archive with unexpected filename %q; expected <pkgname>-<pkgver>-<pkgrel>.pkg.tar.zst\n", filepath.Base(path))
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(stderr, "warning: cannot inspect %q: %v\n", path, err)
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintf(stderr, "warning: ignoring non-regular package archive %q\n", path)
			continue
		}
		sum, err := repoindex.SHA256(path)
		if err != nil {
			fmt.Fprintf(stderr, "warning: cannot hash %q: %v\n", path, err)
			continue
		}
		repoindex.Merge(&index, repoindex.Entry{
			Pkgname: name, Pkgver: ver, Pkgrel: rel, Arch: arch,
			Filename: filepath.Base(path), URL: binaryURL(baseURL, filepath.Base(path)),
			SHA256: sum, Size: info.Size(),
		})
		seen++
	}
	if len(paths) == 0 {
		fmt.Fprintf(stderr, "warning: no .pkg.tar.zst archives found in %q; existing index versions were preserved\n", packages)
	} else if seen == 0 {
		fmt.Fprintf(stderr, "warning: no valid package archives found in %q; existing index versions were preserved\n", packages)
	}
	if err := repoindex.Save(indexPath, index); err != nil {
		return err
	}
	return nil
}

func defaultArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

func parseArchive(filename string) (string, string, string, bool) {
	base := strings.TrimSuffix(filename, ".pkg.tar.zst")
	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		return "", "", "", false
	}
	rel := parts[len(parts)-1]
	ver := parts[len(parts)-2]
	name := strings.Join(parts[:len(parts)-2], "-")
	return name, ver, rel, name != ""
}

func binaryURL(base, filename string) string {
	if base == "" {
		return filename
	}
	return strings.TrimRight(base, "/") + "/" + filename
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid repository base URL %q: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported repository base URL scheme %q; only http and https are supported", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("repository base URL %q must include a host", raw)
	}
	return nil
}
