package testvectors

import "runtime"

func useLinuxAMD64Fixture() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
}
