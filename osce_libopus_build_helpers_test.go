//go:build gopus_extra_controls

package gopus

import (
	"fmt"
	"os"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func buildLibopusOSCEHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildOSCEHelper(repoRoot, sourceFile, outputBase, includeInternal)
}

func cachedLibopusOSCEHelperPath(cache *libopustest.HelperCache, sourceFile, outputBase string, includeInternal bool) (string, error) {
	return cache.Path(func() (string, error) {
		return buildLibopusOSCEHelper(sourceFile, outputBase, includeInternal)
	})
}
