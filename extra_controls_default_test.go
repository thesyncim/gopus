//go:build !gopus_osce && !gopus_dred && !gopus_qext

package gopus

import (
	"errors"
	"testing"
)

// TestDefaultBuildSetDNNBlobIsNoOp pins the default-build USE_WEIGHTS_FILE
// contract: libopus compiles its DNN/model loaders only behind
// ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC, so the default gopus build reports
// no DNN-blob support and every SetDNNBlob entry point is a zero-cost no-op
// that returns ErrOptionalExtensionUnavailable without retaining state.
func TestDefaultBuildSetDNNBlobIsNoOp(t *testing.T) {
	if SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatal("default build reports DNN blob support")
	}

	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("Encoder.SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if enc.dnnBlob != nil {
		t.Fatal("Encoder.SetDNNBlob retained a blob in the default build")
	}

	dec := newMonoTestDecoder(t)
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("Decoder.SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if dec.dnnBlob != nil || dec.dredNeuralModelsLoaded() {
		t.Fatal("Decoder.SetDNNBlob loaded models in the default build")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if err := msEnc.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("MultistreamEncoder.SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if msEnc.dnnBlob != nil {
		t.Fatal("MultistreamEncoder.SetDNNBlob retained a blob in the default build")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if err := msDec.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("MultistreamDecoder.SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if msDec.dnnBlob != nil {
		t.Fatal("MultistreamDecoder.SetDNNBlob retained a blob in the default build")
	}
}

func TestDefaultBuildHidesExtraControls(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(enc).(extraDREDControl); ok {
		t.Fatal("Encoder unexpectedly exposes DRED control in the default build")
	}
	if _, ok := any(enc).(qextEncoderControl); ok {
		t.Fatal("Encoder unexpectedly exposes QEXT control in the default build")
	}

	dec := newMonoTestDecoder(t)
	if _, ok := any(dec).(extraOSCEBWEControl); ok {
		t.Fatal("Decoder unexpectedly exposes OSCE BWE control in the default build")
	}
	if _, ok := any(dec).(extraOSCELACEControl); ok {
		t.Fatal("Decoder unexpectedly exposes OSCE LACE control in the default build")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(msEnc).(extraDREDControl); ok {
		t.Fatal("MultistreamEncoder unexpectedly exposes DRED control in the default build")
	}
	if _, ok := any(msEnc).(qextEncoderControl); ok {
		t.Fatal("MultistreamEncoder unexpectedly exposes QEXT control in the default build")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if _, ok := any(msDec).(extraOSCEBWEControl); ok {
		t.Fatal("MultistreamDecoder unexpectedly exposes OSCE BWE control in the default build")
	}
	if _, ok := any(msDec).(extraOSCELACEControl); ok {
		t.Fatal("MultistreamDecoder unexpectedly exposes OSCE LACE control in the default build")
	}
}

// TestDefaultBuildDNNBlobKeepsDREDRuntimeDormant asserts the default-build
// USE_WEIGHTS_FILE gating: libopus only compiles its DNN/model loaders behind
// ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC, so a default gopus build exposes
// SetDNNBlob as a zero-cost no-op that loads no model, arms no DRED runtime,
// and reports the optional extension as unavailable.
func TestDefaultBuildDNNBlobKeepsDREDRuntimeDormant(t *testing.T) {
	baseline := mustNewTestDecoder(t, 48000, 1)
	armed := mustNewTestDecoder(t, 48000, 1)
	if err := armed.SetDNNBlob(makeValidDecoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if armed.dredNeuralModelsLoaded() {
		t.Fatal("default build SetDNNBlob loaded decoder neural models")
	}
	if armed.dredState() != nil {
		t.Fatalf("default build SetDNNBlob allocated DRED sidecar: %+v", armed.dredState())
	}

	packet := testCELTPacket()
	baselinePCM := make([]float32, baseline.maxPacketSamples)
	armedPCM := make([]float32, armed.maxPacketSamples)
	if _, err := baseline.Decode(packet, baselinePCM); err != nil {
		t.Fatalf("baseline Decode error: %v", err)
	}
	if _, err := baseline.Decode(nil, baselinePCM); err != nil {
		t.Fatalf("baseline Decode(nil) error: %v", err)
	}
	if _, err := armed.Decode(packet, armedPCM); err != nil {
		t.Fatalf("armed Decode error: %v", err)
	}
	if _, err := armed.Decode(nil, armedPCM); err != nil {
		t.Fatalf("armed Decode(nil) error: %v", err)
	}
	if armed.dredState() != nil {
		t.Fatalf("default build Decode woke DRED sidecar: %+v", armed.dredState())
	}

	baselineAllocs := testing.AllocsPerRun(100, func() {
		if _, err := baseline.Decode(nil, baselinePCM); err != nil {
			t.Fatalf("baseline Decode(nil): %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(100, func() {
		if _, err := armed.Decode(nil, armedPCM); err != nil {
			t.Fatalf("armed Decode(nil): %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("default build Decode(nil) after SetDNNBlob allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
	if armed.dredState() != nil {
		t.Fatalf("default build allocation guard woke DRED sidecar: %+v", armed.dredState())
	}
}

// TestDefaultBuildEncoderDNNBlobKeepsDREDDormant asserts the encoder side of
// the default-build USE_WEIGHTS_FILE gate: SetDNNBlob is a zero-cost no-op that
// retains no blob, loads no model, and leaves encode allocations unchanged.
func TestDefaultBuildEncoderDNNBlobKeepsDREDDormant(t *testing.T) {
	baseline := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	armed := mustNewTestEncoder(t, 48000, 1, ApplicationAudio)
	if err := armed.SetDNNBlob(makeValidEncoderTestDNNBlob()); !errors.Is(err, ErrOptionalExtensionUnavailable) {
		t.Fatalf("default SetDNNBlob error=%v want=%v", err, ErrOptionalExtensionUnavailable)
	}
	if armed.dnnBlob != nil {
		t.Fatal("default build SetDNNBlob retained encoder dnn blob handle")
	}

	pcm := make([]float32, 960)
	baselinePacket := make([]byte, 4000)
	armedPacket := make([]byte, 4000)
	if _, err := baseline.Encode(pcm, baselinePacket); err != nil {
		t.Fatalf("baseline Encode error: %v", err)
	}
	if _, err := armed.Encode(pcm, armedPacket); err != nil {
		t.Fatalf("armed Encode error: %v", err)
	}

	baselineAllocs := testing.AllocsPerRun(50, func() {
		if _, err := baseline.Encode(pcm, baselinePacket); err != nil {
			t.Fatalf("baseline Encode: %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(50, func() {
		if _, err := armed.Encode(pcm, armedPacket); err != nil {
			t.Fatalf("armed Encode: %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("default build Encode after SetDNNBlob allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
}
