//go:build gopus_fixed_point && gopus_osce

package celt_test

import (
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	_, err := buildAntiCollapseDNNReferenceHelper()
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("fixed+OSCE anti-collapse pairing error=%v, want LibopusReferenceConfigError", err)
	}
	t.Fatalf("fixed+OSCE anti-collapse has no matching libopus reference: %v", err)
	return nil, false
}
