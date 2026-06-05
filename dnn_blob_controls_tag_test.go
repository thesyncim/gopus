//go:build gopus_dred || gopus_osce

package gopus

import (
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
)

func TestEncoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeNameCompleteEncoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only encoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if enc.dnnBlob != nil || enc.enc.DNNBlobLoaded() {
		t.Fatal("encoder retained name-only DNN blob")
	}
}

func TestDecoderSetDNNBlobRejectsNameOnlyModelBlob(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	if err := dec.SetDNNBlob(makeNameCompleteDecoderTestDNNBlob()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDNNBlob(name-only decoder blob) error=%v want %v", err, ErrInvalidArgument)
	}
	if dec.dnnBlob != nil || dec.pitchDNNLoaded || dec.plcModelLoaded || dec.farganModelLoaded {
		t.Fatal("decoder retained name-only DNN blob")
	}
}

func TestDecoderSetDNNBlobIgnoresNameOnlyDREDDecoderFamily(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	blob := append([]byte(nil), makeValidDecoderTestDNNBlob()...)
	blob = append(blob, makeNameCompleteDREDDecoderTestDNNBlob()...)

	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob(core blob with name-only DRED decoder family) error=%v want nil", err)
	}
	if dec.dnnBlob == nil || !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder did not retain valid core DNN blob with ignored DRED decoder extras")
	}
	if dec.dredPayloadScannerActive() || dec.dredCachedPayloadActive() {
		t.Fatal("decoder armed standalone DRED payload state from ignored DRED decoder extras")
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("decoder allocated DRED sidecar from ignored DRED decoder extras: %+v", state)
	}
}

func TestEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewTestDecoder(t, 16000, 1)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags not armed from validated blob")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder eagerly allocated DRED sidecar on SetDNNBlob: %+v", dec.dredState())
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("decoder failed to lazily materialize neural concealment runtime")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "lazy materialization")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags cleared by Reset")
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("decoder failed to rematerialize neural concealment runtime after Reset")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "Reset rematerialization")
	}
}

func TestDecoderSetDNNBlobStereoRuntimeRetainedAcrossReset(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags not armed from validated blob")
	}
	if dec.dredState() != nil {
		t.Fatalf("stereo decoder eagerly allocated DRED sidecar on SetDNNBlob: %+v", dec.dredState())
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("stereo decoder failed to lazily materialize neural concealment runtime")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "stereo lazy materialization")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.pitchDNNLoaded || !dec.plcModelLoaded || !dec.farganModelLoaded {
		t.Fatal("decoder retained DNN model flags cleared by Reset")
	}
	if extsupport.DREDRuntime {
		if !dec.dredNeuralConcealmentReady() {
			t.Fatal("stereo decoder failed to rematerialize neural concealment runtime after Reset")
		}
		assertDecoderDREDRuntimeLoadedForTest(t, dec, "stereo Reset rematerialization")
	}
}

func TestMultistreamEncoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}

	enc.Reset()
	if enc.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
}

func TestMultistreamDecoderSetDNNBlobRetainedAcrossReset(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob=nil want non-nil")
	}
	if !dec.dec.PitchDNNLoaded() || !dec.dec.PLCModelLoaded() || !dec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder runtime models not loaded from retained DNN blob")
	}

	dec.Reset()
	if dec.dnnBlob == nil {
		t.Fatal("wrapper dnnBlob cleared by Reset")
	}
	if !dec.dec.PitchDNNLoaded() || !dec.dec.PLCModelLoaded() || !dec.dec.FARGANModelLoaded() {
		t.Fatal("multistream decoder runtime models cleared by Reset")
	}
}

func TestHotPathAllocsDecodePLCDNNReadyAtMostBaseline(t *testing.T) {
	baseline, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	armed, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := armed.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	packet := testCELTPacket()
	baselinePCM := make([]float32, 960)
	armedPCM := make([]float32, 960)

	if _, err := baseline.Decode(packet, baselinePCM); err != nil {
		t.Fatalf("baseline warmup Decode: %v", err)
	}
	if _, err := baseline.Decode(nil, baselinePCM); err != nil {
		t.Fatalf("baseline warmup Decode PLC: %v", err)
	}
	if _, err := armed.Decode(packet, armedPCM); err != nil {
		t.Fatalf("armed warmup Decode: %v", err)
	}
	if _, err := armed.Decode(nil, armedPCM); err != nil {
		t.Fatalf("armed warmup Decode PLC: %v", err)
	}

	baselineAllocs := testing.AllocsPerRun(100, func() {
		if _, err := baseline.Decode(nil, baselinePCM); err != nil {
			t.Fatalf("baseline Decode PLC: %v", err)
		}
	})
	armedAllocs := testing.AllocsPerRun(100, func() {
		if _, err := armed.Decode(nil, armedPCM); err != nil {
			t.Fatalf("armed Decode PLC: %v", err)
		}
	})
	if armedAllocs > baselineAllocs {
		t.Fatalf("Decode(PLC, DNN ready) allocs/op = %.2f, want at most baseline %.2f", armedAllocs, baselineAllocs)
	}
}
