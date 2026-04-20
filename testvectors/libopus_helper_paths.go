package testvectors

import (
	"fmt"
	"os"
	"path/filepath"
)

func helperBinaryPath(name, goos, goarch string) string {
	base := fmt.Sprintf("%s_%s_%s", name, goos, goarch)
	if goos == "windows" {
		base += ".exe"
	}
	return filepath.Join(os.TempDir(), base)
}
