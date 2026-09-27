//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package libopustest

import (
	"os"
	"syscall"
)

func lockOracleBuildFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == syscall.EWOULDBLOCK {
			return errOracleBuildLocked
		}
		if err != syscall.EINTR {
			return err
		}
	}
}
