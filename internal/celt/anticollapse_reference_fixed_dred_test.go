//go:build gopus_fixed_point && gopus_dred && !gopus_osce

package celt_test

import (
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	// BuildCHelper validates DRED-QEXT pairing before compiling or linking.
	// Pinned libopus has no FIXED_POINT + ENABLE_DRED reference archive.
	return libopustest.CHelperConfig{
		Label:       "fixed-DRED anti-collapse decode",
		OutputBase:  "gopus_anticollapse_fixed_dred_decode",
		SourceFile:  "libopus_qext_decode96k_info.c",
		DREDQEXTRef: true,
	}
}

func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	_, err := libopustest.BuildCHelper(antiCollapseSelectedRefConfig())
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("fixed+DRED anti-collapse pairing error=%v, want LibopusReferenceConfigError", err)
	}
	t.Fatalf("fixed+DRED anti-collapse has no matching libopus reference: %v", err)
	return nil, false
}
