package encoder

import "testing"

func TestEnsureCELTEncoderDisablesLocalLSBQuantization(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetLSBDepth(16)

	enc.ensureCELTEncoder()

	if enc.celtEncoder == nil {
		t.Fatal("ensureCELTEncoder should initialize the CELT encoder")
	}
	if enc.celtEncoder.LSBQuantizationEnabled() {
		t.Fatal("top-level CELT path should not re-quantize LSB depth after Opus ingress processing")
	}
}
