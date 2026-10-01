package libopustest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var errOracleBuildLocked = errors.New("oracle build lock is held")

// lockOracleBuild serializes source publication and archive construction across
// test processes. The lock file persists: unlinking it could let two processes
// lock different inodes at the same path. Closing the file releases the lock,
// including when the operating system terminates the owning test process.
func lockOracleBuild(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		err = lockOracleBuildFile(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errOracleBuildLocked) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock oracle build %s: %w", path, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return f, nil
}
