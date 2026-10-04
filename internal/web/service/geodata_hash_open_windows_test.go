package service

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestGeoHashReaderDoesNotBlockNativeAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "geosite.dat")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := openGeodataHashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.Rename(path, filepath.Join(dir, ".geosite.dat.test.bak")); err != nil {
		t.Fatalf("hash reader blocked the core's file swap: %v", err)
	}
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "abc" {
		t.Fatalf("reader switched to replaced file: %s", original)
	}
}
