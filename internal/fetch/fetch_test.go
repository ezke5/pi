package fetch

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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
