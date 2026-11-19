// Package fetch downloads sources and performs SHA-256 verification.
package fetch

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Result struct {
	Path   string
	Name   string
	Reused bool
}

// ResolveSource returns a local source path, downloading HTTP(S) sources into
// cache when needed. Relative local sources are resolved from recipeDir.
func ResolveSource(rawSource, recipeDir, cache string, reuse bool, in io.Reader, out io.Writer) (Result, error) {
	sourceName, rawURL := splitSourceName(rawSource)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("parse source %q: %w", rawSource, err)
	}
	if parsed.Scheme == "" {
		local := rawURL
		if !filepath.IsAbs(local) {
			local = filepath.Join(recipeDir, local)
		}
		if sourceName == "" {
			sourceName = filepath.Base(local)
		}
		info, statErr := os.Stat(local)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return Result{}, fmt.Errorf("local source %q is missing from recipe directory %q", rawURL, recipeDir)
			}
			return Result{}, fmt.Errorf("inspect local source %q: %w", local, statErr)
		}
		if info.IsDir() {
			return Result{}, fmt.Errorf("local source %q is a directory, not a file", local)
		}
		return Result{Path: local, Name: sourceName}, nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Result{}, fmt.Errorf("unsupported source URL scheme %q in %q", parsed.Scheme, rawSource)
	}
	base := filepath.Base(parsed.Path)
	if base == "." || base == "/" || base == "" {
		return Result{}, fmt.Errorf("source URL %q does not contain a filename", rawURL)
	}
	if sourceName == "" {
		sourceName = base
	}
	local := filepath.Join(cache, base)
	if info, statErr := os.Stat(local); statErr == nil && !info.IsDir() {
		if reuse {
			fmt.Fprintf(out, "reusing cached source %s\n", local)
			return Result{Path: local, Name: sourceName, Reused: true}, nil
		}
		if file, ok := in.(*os.File); ok {
			if fileInfo, infoErr := file.Stat(); infoErr == nil && fileInfo.Mode()&os.ModeCharDevice != 0 {
				shouldReuse, confirmErr := ConfirmReuse(local, in, out)
				if confirmErr != nil {
					return Result{}, confirmErr
				}
				if shouldReuse {
					return Result{Path: local, Name: sourceName, Reused: true}, nil
				}
			}
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return Result{}, fmt.Errorf("inspect cached source %q: %w", local, statErr)
	}
	if err := Download(rawURL, local, 30*time.Second); err != nil {
		return Result{}, err
	}
	return Result{Path: local, Name: sourceName}, nil
}

func splitSourceName(source string) (string, string) {
	if name, value, ok := strings.Cut(source, "::"); ok {
		return name, value
	}
	return "", source
}

func ConfirmReuse(path string, in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprintf(out, "Cached source %s exists. Reuse it? [Y/n] ", path)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read cache reuse response: %w", err)
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "" || answer == "y" || answer == "yes", nil
}

func VerifySHA256(path, expected string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open source %q for checksum: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("hash source %q: %w", path, err)
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))
	if expected != "" && !strings.EqualFold(expected, actual) {
		return false, fmt.Errorf("SHA-256 mismatch for %q: expected %s, got %s", path, expected, actual)
	}
	return true, nil
}

func Download(rawURL, dest string, timeout time.Duration) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid source URL %q: %w", rawURL, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("missing local file %q", rawURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q for source %q", u.Scheme, rawURL)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create download cache directory: %w", err)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(rawURL)
	if err != nil {
		var detail string
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") || strings.Contains(lower, "x509"):
			detail = "TLS/certificate error"
		case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
			detail = "network timeout"
		case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
			detail = "DNS lookup failure"
		default:
			detail = "network request failure"
		}
		return fmt.Errorf("%s fetching %q: %w", detail, rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP status %s fetching %q", resp.Status, rawURL)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".pi-download-*")
	if err != nil {
		return fmt.Errorf("create temporary download in %q: %w", filepath.Dir(dest), err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download %q: %w", rawURL, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary download %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("store downloaded source as %q: %w", dest, err)
	}
	return nil
}

func ManualDownloadNotice(err error) string {
	return fmt.Sprintf("\nSource fetch failed: %v\n- Download the source manually and place it in the pi cache.\n- For a retry, verify the URL and run build again.\n- To report the issue, include this exact error, the recipe PKGBUILD, and relevant system details.\n", err)
}
