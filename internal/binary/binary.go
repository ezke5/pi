// Package binary resolves repository-index entries into verified local archives.
package binary

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ezke5/pi/internal/archive"
	"github.com/ezke5/pi/internal/fetch"
	"github.com/ezke5/pi/internal/repoindex"
)

func LoadIndex(path string) (repoindex.Index, error) {
	return repoindex.Load(path)
}

func Resolve(index repoindex.Index, name, arch, cache string, out io.Writer) (string, repoindex.Entry, error) {
	return ResolveForUSE(index, name, arch, "", cache, out)
}

// ResolveForUSE resolves only a binary matching arch and effective USE hash.
// An entry with no USE metadata is treated as a legacy, backward-compatible entry.
func ResolveForUSE(index repoindex.Index, name, arch, useHash, cache string, out io.Writer) (string, repoindex.Entry, error) {
	return ResolveForUSEWithTimeout(index, name, arch, useHash, cache, fetch.DefaultDownloadTimeout, out)
}

// ResolveForUSEWithTimeout behaves like ResolveForUSE with a configurable download timeout.
func ResolveForUSEWithTimeout(index repoindex.Index, name, arch, useHash, cache string, timeout time.Duration, out io.Writer) (string, repoindex.Entry, error) {
	entry, ok := repoindex.FindForUSE(index, name, arch, useHash)
	if !ok {
		return "", repoindex.Entry{}, fmt.Errorf("package %q is not present in repository index", name)
	}
	if filepath.Base(entry.Filename) != entry.Filename || entry.Filename == "." || entry.Filename == ".." {
		return "", entry, fmt.Errorf("repository index entry %q has an unsafe filename", entry.Filename)
	}
	path := filepath.Join(cache, entry.Filename)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if err := VerifyUSE(path, entry, useHash); err == nil {
			return path, entry, nil
		} else {
			_ = os.Remove(path)
			fmt.Fprintf(out, "discarding invalid cached binary %s: %v\n", path, err)
		}
	}
	if entry.URL == "" {
		return "", entry, fmt.Errorf("repository index entry %q has no download URL", entry.Filename)
	}
	rawURL := entry.URL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", entry, fmt.Errorf("invalid repository binary URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", entry, fmt.Errorf("unsupported repository binary URL scheme %q for %q; only http and https are supported", parsed.Scheme, rawURL)
	}
	if timeout <= 0 {
		timeout = fetch.DefaultDownloadTimeout
	}
	if err := fetch.Download(rawURL, path, timeout); err != nil {
		return "", entry, err
	}
	if err := VerifyUSE(path, entry, useHash); err != nil {
		_ = os.Remove(path)
		return "", entry, err
	}
	fmt.Fprintf(out, "downloaded binary %s\n", path)
	return path, entry, nil
}

func NormalizeURL(baseURL, filename string) string {
	if baseURL == "" {
		return filename
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(filename, "/")
}

func VerifyUSE(path string, entry repoindex.Entry, useHash string) error {
	if err := repoindex.Verify(path, entry.SHA256); err != nil {
		return err
	}
	info, err := archive.ReadInfo(path)
	if err != nil {
		return nil // legacy archive metadata is unavailable
	}
	if info.USEHash != "" && useHash != "" && info.USEHash != useHash {
		return fmt.Errorf("USE mismatch for %q: expected %s, got %s", path, useHash, info.USEHash)
	}
	if entry.USEHash != "" && info.USEHash != "" && info.USEHash != entry.USEHash {
		return fmt.Errorf("USE mismatch for %q: index expects %s, archive has %s", path, entry.USEHash, info.USEHash)
	}
	return nil
}
