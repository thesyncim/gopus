//go:build gopus_dred && !gopus_qext && !gopus_fixed_point

package celt_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const antiCollapseCheckFixtureQuality = true

var antiCollapseDREDSelectedRefHelper libopustest.HelperCache

func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get test working directory: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	bin, err := antiCollapseDREDSelectedRefHelper.Path(func() (string, error) {
		return libopustest.BuildDREDHelper(repoRoot, "libopus_qext_decode96k_info.c", "gopus_anticollapse_dred_decode", true)
	})
	if err != nil {
		t.Fatalf("build selected DRED-only anti-collapse C decoder: %v", err)
	}
	return decodeAntiCollapseReferencePCM(t, bin, fixture, packets, frameSize, preSkip)
}
