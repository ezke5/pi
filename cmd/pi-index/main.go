package main

import (
	"flag"
	"fmt"
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

	index := repoindex.New()
	if _, err := os.Stat(*indexPath); err == nil {
		var loadErr error
		index, loadErr = repoindex.Load(*indexPath)
		if loadErr != nil {
			fmt.Fprintln(os.Stderr, loadErr)
			os.Exit(1)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	paths, err := filepath.Glob(filepath.Join(*packages, "*.pkg.tar.zst"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, path := range paths {
		name, ver, rel, ok := parseArchive(filepath.Base(path))
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sum, err := repoindex.SHA256(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		repoindex.Merge(&index, repoindex.Entry{
			Pkgname: name, Pkgver: ver, Pkgrel: rel, Arch: *arch,
			Filename: filepath.Base(path), URL: binaryURL(*baseURL, filepath.Base(path)),
			SHA256: sum, Size: info.Size(),
		})
	}
	if err := repoindex.Save(*indexPath, index); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
