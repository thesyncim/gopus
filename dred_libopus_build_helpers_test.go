//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"fmt"
	"os"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func buildLibopusDREDHelper(sourceFile, outputBase string, includeInternal bool) (string, error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return libopustest.BuildDREDHelper(repoRoot, sourceFile, outputBase, includeInternal)
}
