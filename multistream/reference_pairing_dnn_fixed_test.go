//go:build gopus_fixed_point && (gopus_dred || gopus_osce)

package multistream

import (
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestMultistreamReferenceFeaturePairing(t *testing.T) {
	_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
		Label:      "unsupported fixed-point DNN feature pairing",
		OutputBase: "gopus_multistream_fixed_dnn_pairing",
		SourceFile: "libopus_qext_decode96k_info.c",
	})
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("fixed-point DNN pairing error=%v, want LibopusReferenceConfigError", err)
	}
}
