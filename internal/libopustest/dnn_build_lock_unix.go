//go:build unix

package libopustest

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDNNBuild serializes the shared clean-source preparation and DNN libopus
// builds under tmp_check across test processes: go test runs packages in
// parallel, and each package that needs a DRED or OSCE reference prepares the
// same source tree.
func lockDNNBuild(repoRoot string) (unlock func(), err error) {
	dir := filepath.Join(repoRoot, "tmp_check")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".dnn-build.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open DNN build lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock DNN build: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
