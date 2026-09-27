//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !windows

package libopustest

import (
	"fmt"
	"os"
	"runtime"
)

func lockOracleBuildFile(_ *os.File) error {
	return fmt.Errorf("native oracle build locking is unsupported on %s", runtime.GOOS)
}
