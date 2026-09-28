//go:build gopus_dred || gopus_osce

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const (
	libopusDecoderDREDFECHistoryInputMagic  = "GDFI"
	libopusDecoderDREDFECHistoryOutputMagic = "GDFO"
)

var libopusDecoderDREDFECHistoryHelper libopustest.HelperCache

func getLibopusDecoderDREDFECHistoryHelperPath() (string, error) {
	return cachedLibopusDREDHelperPath(
		&libopusDecoderDREDFECHistoryHelper,
		"libopus_decoder_dred_fec_history_info.c",
		"gopus_libopus_decoder_dred_fec_history",
		true,
	)
}

func probeLibopusDecoderDREDFECHistory(seed, fec []byte, frameSize int, model []byte) (seedSamples, fecSamples int, pcm [lpcnetplc.PLCBufSize]float32, err error) {
	binPath, err := getLibopusDecoderDREDFECHistoryHelperPath()
	if err != nil {
		return 0, 0, pcm, err
	}
	payload := libopustest.NewOraclePayloadVersion(
		libopusDecoderDREDFECHistoryInputMagic,
		1,
		uint32(len(seed)),
		uint32(len(fec)),
		uint32(frameSize),
		uint32(len(model)),
	)
	payload.Raw(seed)
	payload.Raw(fec)
	payload.Raw(model)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "decoder DRED FEC history", libopusDecoderDREDFECHistoryOutputMagic)
	if err != nil {
		return 0, 0, pcm, err
	}
	seedSamples = int(reader.I32())
	fecSamples = int(reader.I32())
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return 0, 0, pcm, err
	}
	return seedSamples, fecSamples, pcm, nil
}

func decodeDREDFECForHistory(t *testing.T, model, seed, fec []byte, requestSamples int) (*Decoder, int, []float32) {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetDNNBlob(model); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	if !dec.ensureDREDNeuralConcealmentRuntime() {
		t.Fatal("DRED PLC runtime did not load")
	}
	if err := dec.SetComplexity(0); err != nil {
		t.Fatalf("SetComplexity(0): %v", err)
	}

	primePCM := make([]float32, dec.maxPacketSamples)
	primeSamples, err := dec.Decode(seed, primePCM)
	if err != nil {
		t.Fatalf("Decode prime: %v", err)
	}
	fecPCM := make([]float32, requestSamples)
	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC: %v", err)
	}
	if fecSamples != requestSamples {
		t.Fatalf("DecodeWithFEC samples=%d want %d", fecSamples, requestSamples)
	}
	if primeSamples <= 0 {
		t.Fatalf("Decode prime samples=%d", primeSamples)
	}
	return dec, fecSamples, fecPCM[:fecSamples]
}

func assertDecoderDREDFECHistoryMatchesLibopus(t *testing.T, name string, seed, fec []byte, requestSamples int) {
	t.Helper()
	model := requireLibopusDecoderNeuralModelBlob(t)
	wantSeedSamples, wantFECSamples, wantHistory, err := probeLibopusDecoderDREDFECHistory(seed, fec, requestSamples, model)
	if err != nil {
		libopustest.HelperUnavailable(t, name+" decoder DRED FEC history", err)
	}
	wantSequence, err := libopustest.ProbeDecodeSequence(48000, 1, []libopustest.DecodeDiffCase{
		{Packet: seed, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(requestSamples), DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, name+" decode sequence", err)
	}
	if len(wantSequence) != 2 || int(wantSequence[0].Code) != wantSeedSamples || int(wantSequence[1].Code) != wantFECSamples {
		t.Fatalf("%s C sequence results=%v, history helper counts=(%d,%d)", name, wantSequence, wantSeedSamples, wantFECSamples)
	}

	dec, gotFECSamples, gotPCM := decodeDREDFECForHistory(t, model, seed, fec, requestSamples)
	if gotFECSamples != wantFECSamples {
		t.Fatalf("%s FEC samples=%d want %d", name, gotFECSamples, wantFECSamples)
	}
	if gotRange, wantRange := dec.FinalRange(), wantSequence[1].FinalRange; gotRange != wantRange {
		t.Fatalf("%s FEC range=%08x want %08x", name, gotRange, wantRange)
	}
	wantPCM := wantSequence[1].Float32()
	if len(gotPCM) != len(wantPCM) {
		t.Fatalf("%s FEC PCM length=%d want %d", name, len(gotPCM), len(wantPCM))
	}
	for i := range wantPCM {
		if gotBits, wantBits := math.Float32bits(gotPCM[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
			t.Fatalf("%s FEC PCM[%d]=%08x want %08x", name, i, gotBits, wantBits)
		}
	}

	state := dec.dredRecoveryState()
	if state == nil {
		t.Fatalf("%s has no DRED recovery state", name)
	}
	gotHistory := state.dredPLC.Snapshot().PCM
	for i := range wantHistory {
		if gotBits, wantBits := math.Float32bits(gotHistory[i]), math.Float32bits(wantHistory[i]); gotBits != wantBits {
			t.Fatalf("%s LPCNet history[%d]=%08x want C %08x", name, i, gotBits, wantBits)
		}
	}

	// Warm reusable packet and decoder scratch, then lock the history hook path
	// to the steady-state zero-allocation contract.
	output := make([]float32, requestSamples)
	decode := func() {
		if _, err := dec.DecodeWithFEC(fec, output, true); err != nil {
			panic(err)
		}
	}
	decode()
	decode()
	if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
		t.Fatalf("%s warmed DecodeWithFEC allocations=%g want 0", name, allocs)
	}
}

func TestDecoderDREDFECPLCUpdatesMatchLibopusHistory(t *testing.T) {
	libopustest.RequireOracle(t)
	seed := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	noLBRR := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	if packetHasInBandFEC(t, noLBRR) {
		t.Fatal("no-LBRR prefix fixture unexpectedly carries LBRR")
	}
	t.Run("plc_prefix_before_fec", func(t *testing.T) {
		assertDecoderDREDFECHistoryMatchesLibopus(t, "PLC prefix", seed, noLBRR, 1920)
	})

	var partial []byte
	for _, packet := range encodeFECBurstyStreamForTest(t, BandwidthWideband, 24000, 1, 1920, 40) {
		if isPartialMultiFrameLBRR(t, packet) {
			partial = packet
			break
		}
	}
	if len(partial) == 0 {
		t.Fatal("partial LBRR fixture not found")
	}
	t.Run("missing_lbrr_subframe", func(t *testing.T) {
		assertDecoderDREDFECHistoryMatchesLibopus(t, "missing LBRR subframe", seed, partial, 1920)
	})
}
