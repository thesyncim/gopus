//go:build !gopus_unsupported_controls && !gopus_dred && !gopus_qext
// +build !gopus_unsupported_controls,!gopus_dred,!gopus_qext

package gopus

import "testing"

func TestDefaultBuildQuarantinesUnsupportedControls(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(enc).(unsupportedDREDControl); ok {
		t.Fatal("Encoder unexpectedly exposes DRED control in the default build")
	}
	if _, ok := any(enc).(qextEncoderControl); ok {
		t.Fatal("Encoder unexpectedly exposes QEXT control in the default build")
	}

	dec := newMonoTestDecoder(t)
	if _, ok := any(dec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("Decoder unexpectedly exposes OSCE BWE control in the default build")
	}

	msEnc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)
	if _, ok := any(msEnc).(unsupportedDREDControl); ok {
		t.Fatal("MultistreamEncoder unexpectedly exposes DRED control in the default build")
	}
	if _, ok := any(msEnc).(qextEncoderControl); ok {
		t.Fatal("MultistreamEncoder unexpectedly exposes QEXT control in the default build")
	}

	msDec := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if _, ok := any(msDec).(unsupportedOSCEBWEControl); ok {
		t.Fatal("MultistreamDecoder unexpectedly exposes OSCE BWE control in the default build")
	}
}

func TestDefaultBuildDNNBlobKeepsDREDRuntimeDormant(t *testing.T) {
	baseline := mustNewTestDecoder(t, 48000, 1)
	armed := mustNewTestDecoder(t, 48000, 1)
	if err := armed.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !armed.dredNeuralModelsLoaded() {
		t.Fatal("SetDNNBlob did not retain decoder neural model readiness")
	}
	if armed.dredState() != nil {
		t.Fatalf("SetDNNBlob eagerly allocated DRED sidecar in default build: %+v", armed.dredState())
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
