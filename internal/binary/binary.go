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

	"github.com/ezke5/pi/internal/fetch"
	"github.com/ezke5/pi/internal/repoindex"
)

func LoadIndex(path string) (repoindex.Index, error) {
	return repoindex.Load(path)
}

func Resolve(index repoindex.Index, name, arch, cache string, out io.Writer) (string, repoindex.Entry, error) {
	entry, ok := repoindex.Find(index, name, arch)
	if !ok {
		return "", repoindex.Entry{}, fmt.Errorf("package %q is not present in repository index", name)
	}
	if filepath.Base(entry.Filename) != entry.Filename || entry.Filename == "." || entry.Filename == ".." {
		return "", entry, fmt.Errorf("repository index entry %q has an unsafe filename", entry.Filename)
	}
	path := filepath.Join(cache, entry.Filename)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if err := repoindex.Verify(path, entry.SHA256); err == nil {
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
	if err := fetch.Download(rawURL, path, 30*time.Second); err != nil {
		return "", entry, err
	}
	if err := repoindex.Verify(path, entry.SHA256); err != nil {
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
