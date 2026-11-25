//go:build !gopus_fixed_point

package gopus

import (
	"testing"
	"unsafe"
)

func TestDefaultBuildDecoderFixedFieldsAreZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(decoderFixedFields{}); got != 0 {
		t.Fatalf("decoderFixedFields size = %d, want 0", got)
	}
}

const robustFixedPointDecode = false

// celtIntegerPLCActive is false in the default (float) build, where CELT-only
// packet-loss concealment uses the float32 PLC and the int16/int24 wrappers are
// the float32 output quantized -- so the float-equality and float-oracle PLC
// assertions for CELT are exercised.
const celtIntegerPLCActive = false

// hybridIntegerPLCActive is false in the default (float) build, where Hybrid
// packet-loss concealment uses the float32 PLC and the int16/int24 wrappers are
// the float32 output quantized -- so the float-equality and float-oracle PLC
// assertions for Hybrid are exercised.
const hybridIntegerPLCActive = false

// decodeInt24TracksFloat32Skip is false in the default (float) build, where
// DecodeInt24 derives int24 from the same float32 decode as Decode.
const decodeInt24TracksFloat32Skip = false
