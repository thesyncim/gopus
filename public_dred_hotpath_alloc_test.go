//go:build gopus_dred || gopus_osce

package gopus

import (
	"reflect"
	"testing"
)

func publicEncoderDREDRuntimeLoadedForTest(t *testing.T, enc *Encoder) bool {
	t.Helper()
	if enc == nil || enc.enc == nil {
		return false
	}
	dred := reflect.ValueOf(enc.enc).Elem().FieldByName("dred")
	if !dred.IsValid() || dred.IsNil() {
		return false
	}
	runtime := dred.Elem().FieldByName("runtime")
	return runtime.IsValid() && !runtime.IsNil()
}

func TestPublicDREDEncoderEncodeWithDormantModelStaysZeroAlloc(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationRestrictedSilk})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetDNNBlob(makeValidEncoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if !enc.enc.DREDModelLoaded() {
		t.Fatal("core encoder did not retain DRED-capable model")
	}
	if enc.enc.DREDReady() {
		t.Fatal("core encoder reports DRED ready before duration is armed")
	}

	pcm := testSineFrame(960)
	packet := make([]byte, 4000)
	for i := 0; i < 5; i++ {
		if n, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		} else if n == 0 {
			t.Fatal("warmup Encode returned empty packet")
		}
	}
	if publicEncoderDREDRuntimeLoadedForTest(t, enc) {
		t.Fatal("public Encode woke DRED runtime before duration was armed")
	}

	allocs := testing.AllocsPerRun(200, func() {
		if n, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		} else if n == 0 {
			t.Fatal("Encode returned empty packet")
		}
	})
	if allocs != 0 {
		t.Fatalf("public Encode with dormant DRED model allocs/op = %.2f, want 0", allocs)
	}
	if publicEncoderDREDRuntimeLoadedForTest(t, enc) {
		t.Fatal("allocation guard woke DRED runtime before duration was armed")
	}
}

func TestPublicDREDDecoderDecodeWithCoreModelsStaysZeroAlloc(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5): %v", err)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("decoder did not retain neural model readiness")
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("core decoder DNN blob armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("core decoder DNN blob armed good-packet DRED marker work")
	}

	packet := testCELTPacket()
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("warmup Decode woke DRED sidecar: %+v", state)
	}
	// celt_decode_lost selects FRAME_PLC_NEURAL for a lost CELT frame once the
	// PLC model is loaded, so the neural concealment state wakes while the DRED
	// payload state stays asleep: no DRED payload was ever received.
	if _, err := dec.Decode(nil, pcm); err != nil {
		t.Fatalf("warmup Decode(nil): %v", err)
	}
	state := dec.dredState()
	if state == nil || state.decoderDREDNeuralState == nil {
		t.Fatalf("warmup Decode(nil) did not run neural concealment: %+v", state)
	}
	if state.decoderDREDPayloadState != nil {
		t.Fatalf("warmup Decode(nil) woke DRED payload state without a DRED payload: %+v", state)
	}
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup recovery Decode: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if _, err := dec.Decode(nil, pcm); err != nil {
			t.Fatalf("Decode(nil): %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("public Decode and loss concealment with core DNN models allocs/op = %.2f, want 0", allocs)
	}
	if state := dec.dredState(); state == nil || state.decoderDREDPayloadState != nil {
		t.Fatalf("allocation guard woke DRED payload state: %+v", state)
	}
}
