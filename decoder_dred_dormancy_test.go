//go:build gopus_dred || gopus_osce

package gopus

import (
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDecoderCoreDNNBlobDoesNotArmGoodPacketDREDWork(t *testing.T) {
	requireDREDRuntimeForTest(t)

	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("decoder did not retain neural model readiness")
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("core decoder SetDNNBlob armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("core decoder SetDNNBlob armed good-packet DRED marker work")
	}

	packet := testCELTPacket()
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode(good packet) error: %v", err)
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("good packet armed standalone DRED payload scanning")
	}
	if dec.dredGoodPacketMarkerActive() {
		t.Fatal("good packet armed DRED marker work without payload/recovery state")
	}
	if state := dec.dredState(); state != nil {
		t.Fatalf("good packet with core DNN blob woke DRED sidecar: %+v", state)
	}
}

func TestNewDecoderLeavesDREDPayloadBufferDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if s := dec.dredState(); s != nil && len(s.dredData) != 0 {
		t.Fatalf("len(dredData)=%d want 0 before standalone DRED arm", len(s.dredData))
	}

	setValidDREDDecoderBlobForTest(t, dec)
	if got := len(requireDecoderDREDState(t, dec).dredData); got != 0 {
		t.Fatalf("len(dredData)=%d want 0 after standalone DRED arm before any cached payload", got)
	}

	dec.setDREDDecoderBlob(nil)
	if s := dec.dredState(); s != nil && len(s.dredData) != 0 {
		t.Fatalf("len(dredData)=%d want 0 after standalone DRED clear", len(s.dredData))
	}
}

func TestStandaloneDREDArmKeepsRecoveryNeuralAnd48kBridgeDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	setValidDREDDecoderBlobForTest(t, dec)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDPayloadState == nil {
		t.Fatal("standalone DRED arm did not retain payload state")
	}
	if state.decoderDREDRecoveryState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
	}
	if state.decoderDREDNeuralState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated neural state: %+v", state.decoderDREDNeuralState)
	}
	if state.decoderDRED48kBridgeState != nil {
		t.Fatalf("standalone DRED arm eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
	}
}

func TestMainDecoderDNNBlobKeepsRecoveryAndPayloadDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("main decoder DNN blob did not retain neural model readiness")
	}
	if dec.dredNeuralRuntimeLoaded() {
		t.Fatal("main decoder DNN blob eagerly loaded neural runtime")
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated neural recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("main decoder DNN blob eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("16 kHz decoder eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobKeepsRecoveryAndBridgeDormant(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("48 kHz decoder DNN blob did not retain neural model readiness")
	}
	if dec.dredNeuralRuntimeLoaded() {
		t.Fatal("48 kHz decoder DNN blob eagerly loaded neural runtime")
	}
	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated neural recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("48 kHz decoder DNN blob eagerly allocated bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder16kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("good decode eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("good decode eagerly allocated 48k bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if state := dec.dredState(); state != nil {
		if state.decoderDREDPayloadState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated payload state: %+v", state.decoderDREDPayloadState)
		}
		if state.decoderDREDRecoveryState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated recovery state: %+v", state.decoderDREDRecoveryState)
		}
		if state.decoderDREDNeuralState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated neural runtime state: %+v", state.decoderDREDNeuralState)
		}
		if state.decoderDRED48kBridgeState != nil {
			t.Fatalf("48 kHz good decode eagerly allocated bridge state: %+v", state.decoderDRED48kBridgeState)
		}
	}
}

func TestMainDecoder48kDNNBlobGoodSILKHybridDecodeKeepsRecoveryDormantUntilLoss(t *testing.T) {
	tests := []struct {
		name   string
		packet func(*testing.T) []byte
	}{
		{name: "silk", packet: makeValidMono48kSILKPacketForDREDTest},
		{name: "hybrid", packet: func(t *testing.T) []byte {
			return makeValidMonoHybridPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthFullband)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}
			if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
				t.Fatalf("SetDNNBlob error: %v", err)
			}

			pcm := make([]float32, dec.maxPacketSamples)
			if _, err := dec.Decode(tt.packet(t), pcm); err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if state := dec.dredState(); state != nil {
				t.Fatalf("48 kHz %s good decode eagerly allocated DRED sidecar: %+v", tt.name, state)
			}
		})
	}
}

func TestClearingStandaloneDREDPreservesMainNeuralState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	var pcm [2 * lpcnetplc.FrameSize]float32
	for i := range pcm {
		pcm[i] = float32((i%23)-11) / 23
	}
	dec.ensureDREDRecoveryState()
	dec.markDREDUpdatedPCM(pcm[:], len(pcm), ModeSILK)
	before := requireDecoderDREDState(t, dec).dredPLC.Snapshot()

	setValidDREDDecoderBlobForTest(t, dec)
	dec.setDREDDecoderBlob(nil)

	state := requireDecoderDREDState(t, dec)
	if state.decoderDREDPayloadState != nil {
		t.Fatalf("clearing standalone DRED left payload state behind: %+v", state.decoderDREDPayloadState)
	}
	if !dec.dredNeuralModelsLoaded() {
		t.Fatal("clearing standalone DRED dropped main neural model readiness")
	}
	if state.decoderDREDRecoveryState == nil {
		t.Fatalf("clearing standalone DRED dropped retained recovery state: %+v", state)
	}
	after := state.dredPLC.Snapshot()
	if after.AnalysisPos != before.AnalysisPos || after.PredictPos != before.PredictPos || after.Blend != before.Blend {
		t.Fatalf("clearing standalone DRED reset neural PLC history: before=%+v after=%+v", before, after)
	}
}

func TestDecoderResetDropsActivatedDREDRuntimeBackToDormant(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("decoder failed to materialize neural concealment runtime")
	}
	if dec.dredRecoveryState() == nil || dec.dredNeuralState() == nil {
		t.Fatalf("decoder runtime did not materialize fully: %+v", dec.dredState())
	}

	dec.Reset()
	if dec.dnnBlob == nil || !dec.dredNeuralModelsLoaded() {
		t.Fatal("Reset cleared retained decoder DNN control state")
	}
	if dec.dredRecoveryState() != nil {
		t.Fatalf("Reset left DRED recovery runtime live: %+v", dec.dredState())
	}
	if dec.dredNeuralState() != nil {
		t.Fatalf("Reset left DRED neural runtime live: %+v", dec.dredState())
	}
	if dec.dred48kBridgeState() != nil {
		t.Fatalf("Reset left DRED 48k bridge runtime live: %+v", dec.dredState())
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode(good packet) after Reset error: %v", err)
	}
	if dec.dredState() != nil {
		t.Fatalf("good decode after Reset eagerly reawakened DRED sidecar: %+v", dec.dredState())
	}
}

func TestDecoderSetDNNBlobPreservesActive48kBridge(t *testing.T) {
	requireDREDRuntimeForTest(t)

	dec := mustNewTestDecoder(t, 48000, 1)
	blob := makeValidDecoderTestDNNBlob()
	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if !dec.dredNeuralConcealmentReady() {
		t.Fatal("decoder failed to materialize neural concealment runtime")
	}
	bridge := dec.dred48kBridgeState()
	if bridge == nil {
		t.Fatalf("decoder did not materialize 48 kHz bridge: %+v", dec.dredState())
	}

	bridge.dredPLCPCM[0] = 4096
	bridge.dredPLCFill = 37
	bridge.dredPLCPreemphMem = 0.25
	bridge.dredLastNeural = true

	state := requireDecoderDREDState(t, dec)
	var farganPCM [lpcnetplc.FARGANContSamples]float32
	var farganFeatures [lpcnetplc.ContVectors * lpcnetplc.NumFeatures]float32
	for i := range farganPCM {
		farganPCM[i] = float32((i%31)-15) / 19
	}
	for i := range farganFeatures {
		farganFeatures[i] = float32((i%17)-8) / 11
	}
	if n := state.dredFARGAN.PrimeContinuity(farganPCM[:], farganFeatures[:]); n != lpcnetplc.FARGANContSamples {
		t.Fatalf("PrimeContinuity()=%d want %d", n, lpcnetplc.FARGANContSamples)
	}
	beforeFARGAN := state.dredFARGAN.Snapshot()

	// libopus reloads the model in-place for OPUS_SET_DNN_BLOB; it does not
	// reset active PLC/DRED bridge state on a successful reload.
	if err := dec.SetDNNBlob(blob); err != nil {
		t.Fatalf("SetDNNBlob(reload) error: %v", err)
	}
	got := dec.dred48kBridgeState()
	if got != bridge {
		t.Fatalf("SetDNNBlob(reload) replaced 48 kHz bridge: before=%p after=%p", bridge, got)
	}
	if got.dredPLCPCM[0] != 4096 ||
		got.dredPLCFill != 37 ||
		got.dredPLCPreemphMem != 0.25 ||
		!got.dredLastNeural {
		t.Fatalf("SetDNNBlob(reload) reset 48 kHz bridge: %+v", got)
	}
	if afterFARGAN := requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(); afterFARGAN != beforeFARGAN {
		t.Fatalf("SetDNNBlob(reload) reset FARGAN state: before=%+v after=%+v", beforeFARGAN, afterFARGAN)
	}
}

func TestDecoderPrimeDREDCELTEntryHistoryStaysDormantWithoutNeuralConcealment(t *testing.T) {
	packet := makeValidMonoCELTPacketForDREDTest(t)

	dec, err := NewDecoder(DefaultDecoderConfig(16000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.primeDREDCELTEntryHistory(ModeCELT, false); got != 0 {
		t.Fatalf("primeDREDCELTEntryHistory()=%d want 0", got)
	}
	if dec.dredState() != nil {
		t.Fatalf("dred sidecar awakened without neural concealment readiness: %+v", dec.dredState())
	}
}

func TestDecoderLeavesDREDPayloadDormantWithoutDREDModel(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder cached dormant DRED sidecar=%+v want nil", dec.dredState())
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples without model=%d want 0", got)
	}
}

func TestDecoderLeavesDREDStateDormantWithoutAnySidecar(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar=%+v want nil", dec.dredState())
	}

	n, err = dec.Decode(nil, pcm)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode(nil) returned zero samples")
	}
	if dec.dredState() != nil {
		t.Fatalf("decoder awakened dormant DRED sidecar after PLC=%+v want nil", dec.dredState())
	}
}

func TestPublicDecoderSetDNNBlobDoesNotArmDREDDecoderWhenBlobContainsModel(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if err := dec.SetDNNBlob(makeValidDecoderControlWithDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if dec.dredPayloadScannerActive() {
		t.Fatal("public decoder SetDNNBlob armed standalone DRED payload scanning")
	}

	pcm := make([]float32, 960*2)
	if _, err := dec.Decode(extended, pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.dredCachedPayloadActive() {
		t.Fatal("public decoder SetDNNBlob cached DRED payload without standalone decoder state")
	}
	if state := dec.dredState(); state != nil && state.decoderDREDPayloadState != nil {
		t.Fatalf("public decoder SetDNNBlob woke DRED payload sidecar: %+v", state.decoderDREDPayloadState)
	}
}

func TestDecoderLeavesDREDPayloadDormantWhenIgnoringExtensions(t *testing.T) {
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	extended := buildSingleFramePacketWithExtensionsForDREDTest(t, base, []packetExtensionData{
		{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
	})

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	setValidDREDDecoderBlobForTest(t, dec)
	dec.SetIgnoreExtensions(true)

	pcm := make([]float32, 960*2)
	n, err := dec.Decode(extended, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n == 0 {
		t.Fatal("Decode returned zero samples")
	}
	if got := requireDecoderDREDState(t, dec).dredCache; got != (internaldred.Cache{}) {
		t.Fatalf("decoder cached ignored DRED cache=%+v want zero state", got)
	}
	if got := dec.cachedDREDMaxAvailableSamples(960); got != 0 {
		t.Fatalf("cachedDREDMaxAvailableSamples while ignoring=%d want 0", got)
	}
}
