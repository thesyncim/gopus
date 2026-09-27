package libopustest

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestDNNSourceRejectsPartialPublication(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, "tmp_check")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(tmpDir, "opus-1.6.1.tar.gz")
	writeSource := func(names []string) {
		t.Helper()
		f, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		for _, name := range names {
			if err := tw.WriteHeader(&tar.Header{Name: "opus-1.6.1/" + name, Mode: 0o644, Size: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte{'\n'}); err != nil {
				t.Fatal(err)
			}
		}
		for _, close := range []func() error{tw.Close, gz.Close, f.Close} {
			if err := close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// configure alone is not a complete extraction. A failed extraction must
	// not make the next package treat an incomplete tree as ready to build.
	writeSource([]string{"configure"})
	if _, err := ensureDNNSource(root); err == nil {
		t.Fatal("incomplete archive accepted")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "opus-1.6.1-dnnsrc-atomic")); !os.IsNotExist(err) {
		t.Fatalf("incomplete source is visible: %v", err)
	}
	writeSource([]string{"configure", "install-sh", "config.sub", "include/opus.h", "dnn/nnet.c"})
	source, err := ensureDNNSource(root)
	if err != nil {
		t.Fatal(err)
	}
	// A published tree is independent of staging and the tarball.
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if got, err := ensureDNNSource(root); err != nil || got != source {
		t.Fatalf("reuse published source = %q, %v", got, err)
	}
	if err := os.Remove(filepath.Join(source, "config.sub")); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDNNSource(root); err == nil {
		t.Fatal("incomplete published source accepted")
	}
}
