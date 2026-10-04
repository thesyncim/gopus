//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package libopustest

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCHelperBuildCachePropagatesDisabledReuseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oracle-helper")
	if err := os.WriteFile(path, []byte("old helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !usableCHelperBinary(path) {
		t.Fatal("test setup did not create a usable cached helper")
	}

	wantErr := errors.New("live oracle rebuild failed")
	builds := 0
	err := buildCHelperWithCache(path, false, func() error {
		builds++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("build error=%v want %v", err, wantErr)
	}
	if builds != 1 {
		t.Fatalf("build callback calls=%d want 1", builds)
	}
}

func TestCHelperBuildCacheSerializesConcurrentBuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oracle-helper")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var builds atomic.Int32
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- buildCHelperWithCache(path, true, func() error {
				builds.Add(1)
				time.Sleep(20 * time.Millisecond)
				return os.WriteFile(path, []byte("complete helper"), 0o755)
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("helper builds=%d want 1", got)
	}
	if !usableCHelperBinary(path) {
		t.Fatal("cache returned without installing a usable helper")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "complete helper" {
		t.Fatalf("helper contents=%q want complete artifact", data)
	}
}
