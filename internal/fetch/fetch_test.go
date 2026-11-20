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
