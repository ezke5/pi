package fetch

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifySHA256Mismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySHA256(path, "000000"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	sum := sha256.Sum256([]byte("content"))
	if _, err := VerifySHA256(path, fmt.Sprintf("%x", sum)); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
}

func TestResolveSourceDownloadsAndReusesRemoteArtifact(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("remote source"))
	}))
	defer server.Close()

	cache := t.TempDir()
	var output bytes.Buffer
	first, err := ResolveSource(server.URL+"/downloads/source.tar.gz?mirror=1", t.TempDir(), cache, true, nil, &output)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused {
		t.Fatal("first download unexpectedly marked as reused")
	}
	if data, err := os.ReadFile(first.Path); err != nil || string(data) != "remote source" {
		t.Fatalf("cached source = %q, err=%v", data, err)
	}
	second, err := ResolveSource(server.URL+"/downloads/source.tar.gz?mirror=2", t.TempDir(), cache, true, nil, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || requests.Load() != 1 {
		t.Fatalf("reuse=%v requests=%d output=%q", second.Reused, requests.Load(), output.String())
	}
}

func TestResolveSourceReportsMissingLocalArtifact(t *testing.T) {
	_, err := ResolveSource("missing.tar.gz", t.TempDir(), t.TempDir(), true, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "missing from recipe directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadRejectsUnsupportedSchemeClearly(t *testing.T) {
	err := Download("file:///tmp/source.tar.gz", filepath.Join(t.TempDir(), "source.tar.gz"), 0)
	if err == nil || !strings.Contains(err.Error(), `unsupported URL scheme "file"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadRetriesTransientBodyTimeoutAndReusesCache(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		_, _ = w.Write([]byte("complete-tarball-bytes"))
	}))
	defer server.Close()

	cache := t.TempDir()
	dest := filepath.Join(cache, "bash-5.2.37.tar.gz")
	err := downloadWithOptions(server.URL+"/bash-5.2.37.tar.gz", dest, downloadOptions{
		timeout:     80 * time.Millisecond,
		maxAttempts: 3,
		baseDelay:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected download to succeed on retry: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "complete-tarball-bytes" {
		t.Fatalf("downloaded data = %q, err=%v", data, err)
	}
	if temps, _ := filepath.Glob(filepath.Join(cache, ".pi-download-*")); len(temps) != 0 {
		t.Fatalf("leftover temp files: %v", temps)
	}

	var output bytes.Buffer
	res, err := ResolveSourceWithTimeout(server.URL+"/bash-5.2.37.tar.gz", t.TempDir(), cache, true, 80*time.Millisecond, nil, &output)
	if err != nil {
		t.Fatalf("cache reuse failed: %v", err)
	}
	if !res.Reused || res.Path != dest || attempts.Load() != 3 {
		t.Fatalf("expected cache hit without new request: res=%+v attempts=%d", res, attempts.Load())
	}
}

func TestDownloadExhaustsRetriesOnPersistentTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	cache := t.TempDir()
	dest := filepath.Join(cache, "slow.tar.gz")
	err := downloadWithOptions(server.URL+"/slow.tar.gz", dest, downloadOptions{
		timeout:     60 * time.Millisecond,
		maxAttempts: 3,
		baseDelay:   5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") || !strings.Contains(err.Error(), "network timeout") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("failed download created destination file: %v", statErr)
	}
	if temps, _ := filepath.Glob(filepath.Join(cache, ".pi-download-*")); len(temps) != 0 {
		t.Fatalf("leftover temp files: %v", temps)
	}
}

func TestDownloadRetries503AndFailsFastOn404(t *testing.T) {
	var transientAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flaky.tar.gz":
			if transientAttempts.Add(1) == 1 {
				http.Error(w, "try again", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("ok"))
		case "/missing.tar.gz":
			transientAttempts.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cache := t.TempDir()
	dest := filepath.Join(cache, "flaky.tar.gz")
	if err := downloadWithOptions(server.URL+"/flaky.tar.gz", dest, downloadOptions{
		timeout:     2 * time.Second,
		maxAttempts: 3,
		baseDelay:   5 * time.Millisecond,
	}); err != nil {
		t.Fatalf("expected 503 retry to succeed: %v", err)
	}
	if transientAttempts.Load() != 2 {
		t.Fatalf("503 attempts = %d, want 2", transientAttempts.Load())
	}

	transientAttempts.Store(0)
	err := downloadWithOptions(server.URL+"/missing.tar.gz", filepath.Join(cache, "missing.tar.gz"), downloadOptions{
		timeout:     2 * time.Second,
		maxAttempts: 3,
		baseDelay:   5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got: %v", err)
	}
	if transientAttempts.Load() != 1 {
		t.Fatalf("404 should not retry: attempts = %d", transientAttempts.Load())
	}
}

func TestResolveSourceWithTimeoutCacheHitSkipsNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "should not be contacted", http.StatusInternalServerError)
	}))
	defer server.Close()

	cache := t.TempDir()
	cached := filepath.Join(cache, "bash-5.2.37.tar.gz")
	if err := os.WriteFile(cached, []byte("cached-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	res, err := ResolveSourceWithTimeout(server.URL+"/gnu/bash/bash-5.2.37.tar.gz", t.TempDir(), cache, true, 5*time.Minute, nil, &output)
	if err != nil {
		t.Fatalf("ResolveSourceWithTimeout failed: %v", err)
	}
	if !res.Reused || res.Path != cached || requests.Load() != 0 {
		t.Fatalf("expected cache hit with 0 requests: res=%+v requests=%d", res, requests.Load())
	}
}
