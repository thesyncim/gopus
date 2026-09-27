package libopustest

import (
	"os"
	"syscall"
	"unsafe"
)

var oracleLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func lockOracleBuildFile(f *os.File) error {
	// Request a nonblocking exclusive byte-range lock.
	// The overlapped offset is zero; every builder locks the same first byte.
	var overlapped syscall.Overlapped
	const exclusiveLock = 2
	const failImmediately = 1
	ok, _, err := oracleLockFileEx.Call(f.Fd(), exclusiveLock|failImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		if err == syscall.Errno(33) { // ERROR_LOCK_VIOLATION
			return errOracleBuildLocked
		}
		return err
	}
	return nil
}
