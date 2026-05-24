package testvectors

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func platformFixturePath(generic string) string {
	ext := filepath.Ext(generic)
	path := strings.TrimSuffix(generic, ext) + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return generic
}
