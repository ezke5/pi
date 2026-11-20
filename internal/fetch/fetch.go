// Package fetch downloads sources and performs SHA-256 verification.
package fetch

import (
	"bufio"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultDownloadTimeout is the per-attempt overall download timeout used
	// when no explicit timeout is supplied.
	DefaultDownloadTimeout       = 10 * time.Minute
	defaultDialTimeout           = 30 * time.Second
	defaultTLSHandshakeTimeout   = 30 * time.Second
	defaultResponseHeaderTimeout = 60 * time.Second
	defaultMaxDownloadAttempts   = 3
	defaultRetryBaseDelay        = 1 * time.Second
)

type downloadOptions struct {
	timeout     time.Duration
	maxAttempts int
	baseDelay   time.Duration
}

type Result struct {
	Path   string
	Name   string
	Reused bool
}

// ResolveSource returns a local source path, downloading HTTP(S) sources into
// cache when needed. Relative local sources are resolved from recipeDir.
func ResolveSource(rawSource, recipeDir, cache string, reuse bool, in io.Reader, out io.Writer) (Result, error) {
	return ResolveSourceWithTimeout(rawSource, recipeDir, cache, reuse, DefaultDownloadTimeout, in, out)
}

// ResolveSourceWithTimeout behaves like ResolveSource but uses timeout for
// HTTP(S) downloads (defaulting to DefaultDownloadTimeout when timeout <= 0).
func ResolveSourceWithTimeout(rawSource, recipeDir, cache string, reuse bool, timeout time.Duration, in io.Reader, out io.Writer) (Result, error) {
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
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}
	if err := Download(rawURL, local, timeout); err != nil {
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
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}
	return downloadWithOptions(rawURL, dest, downloadOptions{
		timeout:     timeout,
		maxAttempts: defaultMaxDownloadAttempts,
		baseDelay:   defaultRetryBaseDelay,
	})
}

func downloadWithOptions(rawURL, dest string, opts downloadOptions) error {
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
	if opts.timeout <= 0 {
		opts.timeout = DefaultDownloadTimeout
	}
	if opts.maxAttempts <= 0 {
		opts.maxAttempts = 1
	}

	transport := newDownloadTransport(opts.timeout)
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   opts.timeout,
	}

	var lastErr error
	for attempt := 1; attempt <= opts.maxAttempts; attempt++ {
		retryable, err := downloadOnce(client, rawURL, dest)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == opts.maxAttempts {
			if attempt > 1 {
				return fmt.Errorf("after %d attempts: %w", attempt, lastErr)
			}
			return lastErr
		}
		delay := opts.baseDelay << (attempt - 1)
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return lastErr
}

func newDownloadTransport(timeout time.Duration) *http.Transport {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	dialTimeout := minDuration(defaultDialTimeout, timeout)
	tlsTimeout := minDuration(defaultTLSHandshakeTimeout, timeout)
	headerTimeout := minDuration(defaultResponseHeaderTimeout, timeout)
	transport.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = tlsTimeout
	transport.ResponseHeaderTimeout = headerTimeout
	return transport
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func downloadOnce(client *http.Client, rawURL, dest string) (bool, error) {
	resp, err := client.Get(rawURL)
	if err != nil {
		detail, retryable := classifyRequestError(err)
		return retryable, fmt.Errorf("%s fetching %q: %w", detail, rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return isRetryableHTTPStatus(resp.StatusCode), fmt.Errorf("HTTP status %s fetching %q", resp.Status, rawURL)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".pi-download-*")
	if err != nil {
		return false, fmt.Errorf("create temporary download in %q: %w", filepath.Dir(dest), err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if readErr, writeErr := copyResponseBody(tmp, resp.Body); readErr != nil || writeErr != nil {
		tmp.Close()
		if writeErr != nil {
			return false, fmt.Errorf("write temporary download %q: %w", tmpName, writeErr)
		}
		return true, fmt.Errorf("download %q: %w", rawURL, readErr)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temporary download %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return false, fmt.Errorf("store downloaded source as %q: %w", dest, err)
	}
	return false, nil
}

func classifyRequestError(err error) (detail string, retryable bool) {
	var unknownAuth x509.UnknownAuthorityError
	var certInvalid x509.CertificateInvalidError
	var hostErr x509.HostnameError
	if errors.As(err, &unknownAuth) || errors.As(err, &certInvalid) || errors.As(err, &hostErr) {
		return "TLS/certificate error", false
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "network timeout", true
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") || strings.Contains(lower, "x509"):
		return "TLS/certificate error", false
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
		return "DNS lookup failure", true
	default:
		return "network request failure", true
	}
}

func isRetryableHTTPStatus(code int) bool {
	return code == http.StatusRequestTimeout ||
		code == http.StatusTooManyRequests ||
		(code >= http.StatusInternalServerError && code <= 599)
}

func copyResponseBody(dst io.Writer, src io.Reader) (readErr, writeErr error) {
	buf := make([]byte, 32*1024)
	for {
		n, rErr := src.Read(buf)
		if n > 0 {
			written := 0
			for written < n {
				nw, wErr := dst.Write(buf[written:n])
				written += nw
				if wErr != nil {
					return nil, wErr
				}
				if nw == 0 {
					return nil, io.ErrShortWrite
				}
			}
		}
		if rErr == io.EOF {
			return nil, nil
		}
		if rErr != nil {
			return rErr, nil
		}
	}
}

func ManualDownloadNotice(err error) string {
	return fmt.Sprintf("\nSource fetch failed: %v\n- Download the source manually and place it in the pi cache.\n- For a retry, verify the URL and run build again.\n- To report the issue, include this exact error, the recipe PKGBUILD, and relevant system details.\n", err)
}
