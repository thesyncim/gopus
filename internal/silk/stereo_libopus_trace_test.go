//go:build gopus_silk_trace && !gopus_fixed_point

package silk

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSILKPacket0MidFrameCoreTraceOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		bitRate       = 48000
		maxBits       = 1500
		payloadSizeMs = 20
	)
	signal := chirpSweepWB20msStereo48kPacket0Signal(t)
	want, err := probeLibopusSILKPacket0Wrapper(signal, bitRate, maxBits, true, payloadSizeMs, 0)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk stereo packet0 wrapper", err)
	}
	f := prepareSILKPacket0MidFrameCoreOracle(t, signal, bitRate, maxBits, payloadSizeMs, want)

	var afterIndices encodeFrameTrace
	var afterPulses encodeFrameTrace
	var haveAfterIndices bool
	var haveAfterPulses bool
	withEncodeFrameTraceHook(func(_ *Encoder, trace encodeFrameTrace) {
		if trace.iter != 0 {
			return
		}
		switch trace.stage {
		case encodeFrameTraceAfterIndices:
			afterIndices = trace
			haveAfterIndices = true
		case encodeFrameTraceAfterPulses:
			trace.pulses = append([]int8(nil), trace.pulses...)
			afterPulses = trace
			haveAfterPulses = true
		}
	}, func() {
		_ = f.mid.encodeFrame(f.re, f.condCoding, f.maxBits, f.useCBR)
	})
	if !haveAfterIndices {
		t.Fatalf("missing packet-0 mid after-indices trace")
	}
	if !haveAfterPulses {
		t.Fatalf("missing packet-0 mid after-pulses trace")
	}
	checkTraceGains(t, afterPulses, want)
	checkTraceIndices(t, afterPulses.indices, want)
	checkIndexTrace(t, afterIndices.indexTrace, want)
	if int32(afterIndices.tell) != want.midManualTellIndices {
		t.Fatalf("packet-0 mid after indices tell=%d want %d", afterIndices.tell, want.midManualTellIndices)
	}
	if int32(afterIndices.rng) != want.midManualRangeIndices {
		t.Fatalf("packet-0 mid after indices range=%d want %d", afterIndices.rng, want.midManualRangeIndices)
	}
	gotAbs, gotHash, gotBlocks := summarizeTracePulses(afterPulses.pulses)
	if gotAbs != want.midPulseAbsSum {
		t.Fatalf("packet-0 mid pulse abs sum=%d want %d", gotAbs, want.midPulseAbsSum)
	}
	if gotHash != uint32(want.midPulseHash) {
		t.Fatalf("packet-0 mid pulse hash=%08x want %08x", gotHash, uint32(want.midPulseHash))
	}
	if int32(len(gotBlocks)) != want.midPulseBlockCount {
		t.Fatalf("packet-0 mid pulse block count=%d want %d", len(gotBlocks), want.midPulseBlockCount)
	}
	for i := 0; i < len(gotBlocks) && i < len(want.midPulseBlockAbsSum); i++ {
		if gotBlocks[i] != want.midPulseBlockAbsSum[i] {
			t.Fatalf("packet-0 mid pulse block %d abs sum=%d want %d", i, gotBlocks[i], want.midPulseBlockAbsSum[i])
		}
	}
	if want.midManualTellPulses != want.midTellAfterFrame || want.midManualRangePulses != want.midRangeAfterFrame {
		t.Fatalf("libopus packet-0 manual replay tell/range=%d/%d want final %d/%d",
			want.midManualTellPulses, want.midManualRangePulses, want.midTellAfterFrame, want.midRangeAfterFrame)
	}
	if int32(afterPulses.tell) != want.midManualTellPulses {
		t.Fatalf("packet-0 mid after pulses tell=%d want %d", afterPulses.tell, want.midManualTellPulses)
	}
	if int32(afterPulses.rng) != want.midManualRangePulses {
		t.Fatalf("packet-0 mid after pulses range=%d want %d", afterPulses.rng, want.midManualRangePulses)
	}
}

func checkIndexTrace(t testing.TB, got [encodeFrameIndexTracePointCount]encodeFrameRangeTrace, want libopusSILKPacket0WrapperRecord) {
	t.Helper()
	names := [...]string{"type", "gains", "nlsf", "pitch", "ltp", "seed"}
	for i := range got {
		if int32(got[i].tell) != want.midIndexTraceTell[i] {
			t.Fatalf("packet-0 mid after %s tell=%d want %d", names[i], got[i].tell, want.midIndexTraceTell[i])
		}
		if int32(got[i].rng) != want.midIndexTraceRange[i] {
			t.Fatalf("packet-0 mid after %s range=%d want %d", names[i], got[i].rng, want.midIndexTraceRange[i])
		}
	}
}

func checkTraceGains(t testing.TB, got encodeFrameTrace, want libopusSILKPacket0WrapperRecord) {
	t.Helper()
	if want.midGainTraceValid == 0 {
		t.Fatalf("libopus packet-0 mid gain trace was not captured")
	}
	if got.pitchAutoCorr0Bits != want.midPitchAutoCorr0Bits {
		t.Fatalf("packet-0 mid pitch auto_corr[0] bits=%08x want %08x", uint32(got.pitchAutoCorr0Bits), uint32(want.midPitchAutoCorr0Bits))
	}
	if got.pitchResNrgBits != want.midPitchResNrgBits {
		t.Fatalf("packet-0 mid pitch res_nrg bits=%08x want %08x", uint32(got.pitchResNrgBits), uint32(want.midPitchResNrgBits))
	}
	if got.predGainBits != want.midPredGainBits {
		t.Fatalf("packet-0 mid predGain bits=%08x want %08x", uint32(got.predGainBits), uint32(want.midPredGainBits))
	}
	for i := range got.gainsPreQ16 {
		if got.gainsPreQ16[i] != want.midGainsPreQ16[i] {
			t.Fatalf("packet-0 mid gains before process[%d]=%d want %d (got %v want %v)",
				i, got.gainsPreQ16[i], want.midGainsPreQ16[i], got.gainsPreQ16, want.midGainsPreQ16)
		}
	}
	for i := range got.resNrgBits {
		if got.resNrgBits[i] != want.midResNrgBits[i] {
			t.Fatalf("packet-0 mid ResNrg bits[%d]=%08x want %08x (got %08x want %08x)",
				i, uint32(got.resNrgBits[i]), uint32(want.midResNrgBits[i]), got.resNrgBits, want.midResNrgBits)
		}
	}
	for i := range got.gainsUnqQ16 {
		if got.gainsUnqQ16[i] != want.midGainsUnqQ16[i] {
			t.Fatalf("packet-0 mid GainsUnqQ16[%d]=%d want %d (got %v want %v)",
				i, got.gainsUnqQ16[i], want.midGainsUnqQ16[i], got.gainsUnqQ16, want.midGainsUnqQ16)
		}
	}
	for i := range got.gainsQuantQ16 {
		if got.gainsQuantQ16[i] != want.midGainsQuantQ16[i] {
			t.Fatalf("packet-0 mid quantized gains Q16[%d]=%d want %d (got %v want %v)",
				i, got.gainsQuantQ16[i], want.midGainsQuantQ16[i], got.gainsQuantQ16, want.midGainsQuantQ16)
		}
	}
}

func checkTraceIndices(t testing.TB, got sideInfoIndices, want libopusSILKPacket0WrapperRecord) {
	t.Helper()
	if int32(got.signalType) != want.midSignalType {
		t.Fatalf("packet-0 mid signalType=%d want %d", got.signalType, want.midSignalType)
	}
	if int32(got.quantOffsetType) != want.midQuantOffsetType {
		t.Fatalf("packet-0 mid quantOffsetType=%d want %d", got.quantOffsetType, want.midQuantOffsetType)
	}
	if int32(got.Seed) != want.midSeed {
		t.Fatalf("packet-0 mid seed=%d want %d", got.Seed, want.midSeed)
	}
	if int32(got.NLSFInterpCoefQ2) != want.midNLSFInterpCoefQ2 {
		t.Fatalf("packet-0 mid NLSFInterpCoefQ2=%d want %d", got.NLSFInterpCoefQ2, want.midNLSFInterpCoefQ2)
	}
	for i := range got.GainsIndices {
		if int32(got.GainsIndices[i]) != want.midGainsIndices[i] {
			t.Fatalf("packet-0 mid GainsIndices[%d]=%d want %d (got %v want %v)",
				i, got.GainsIndices[i], want.midGainsIndices[i], got.GainsIndices, want.midGainsIndices)
		}
	}
	for i := range got.NLSFIndices {
		if int32(got.NLSFIndices[i]) != want.midNLSFIndices[i] {
			t.Fatalf("packet-0 mid NLSFIndices[%d]=%d want %d", i, got.NLSFIndices[i], want.midNLSFIndices[i])
		}
	}
}

func summarizeTracePulses(pulses []int8) (int32, uint32, []int32) {
	const fnvPrime = uint32(16777619)
	hash := uint32(2166136261)
	var total int32
	blocks := make([]int32, (len(pulses)+shellCodecFrameLength-1)/shellCodecFrameLength)
	for i, p := range pulses {
		hash ^= uint32(uint8(p))
		hash *= fnvPrime
		v := int32(p)
		if v < 0 {
			v = -v
		}
		total += v
		blocks[i/shellCodecFrameLength] += v
	}
	return total, hash, blocks
}
