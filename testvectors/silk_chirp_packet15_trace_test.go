package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestSILKChirpPacket15Trace(t *testing.T) {
	requireTestTier(t, testTierParity)

	cc := struct {
		mode      encoder.Mode
		bandwidth types.Bandwidth
		frameSize int
		channels  int
		bitrate   int
	}{encoder.ModeSILK, types.BandwidthWideband, 1920, 1, 32000}

	fc, ok := findEncoderVariantsFixtureCase(cc.mode, cc.bandwidth, cc.frameSize, cc.channels, cc.bitrate, testsignal.EncoderVariantChirpSweepV1)
	if !ok {
		t.Fatalf("missing fixture for SILK-WB-40ms-mono-32k chirp_sweep_v1")
	}
	totalSamples := fc.SignalFrames * fc.FrameSize * fc.Channels
	signal, err := testsignal.GenerateEncoderSignalVariant(fc.Variant, 48000, totalSamples, fc.Channels)
	if err != nil {
		t.Fatalf("gen signal: %v", err)
	}

	enc := encoder.NewEncoder(48000, fc.Channels)
	enc.SetMode(cc.mode)
	enc.SetBandwidth(cc.bandwidth)
	enc.SetBitrate(fc.Bitrate)
	enc.SetBitrateMode(encoder.ModeCBR)

	trace := &silk.EncoderTrace{
		NLSF:     &silk.NLSFTrace{},
		GainLoop: &silk.GainLoopTrace{},
		NSQ:      &silk.NSQTrace{},
		FramePre: &silk.FrameStateTrace{},
		Frame:    &silk.FrameStateTrace{},
	}
	enc.SetSilkTrace(trace)

	samplesPerFrame := fc.FrameSize * fc.Channels
	for i := 0; i < fc.SignalFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		frame := float32ToFloat64OpusDemoF32(signal[start:end])
		pkt, err := enc.Encode(frame, fc.FrameSize)
		if err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}
		if i < 14 || i > 16 {
			continue
		}
		t.Logf("packet %d len=%d head=%s", i, len(pkt), hexFirstN(pkt, 12))
		t.Logf("  pre: rate=%d target=%d snrQ7=%d bitsExceeded=%d framesPerPacket=%d framesEncoded=%d prevLag=%d prevSignal=%d lastGain=%d",
			trace.FramePre.InputRateBps, trace.FramePre.TargetRateBps, trace.FramePre.SNRDBQ7, trace.FramePre.NBitsExceeded,
			trace.FramePre.NFramesPerPacket, trace.FramePre.NFramesEncoded, trace.FramePre.PrevLag, trace.FramePre.PrevSignalType, trace.FramePre.LastGainIndex)
		t.Logf("  nlsf: interp=%d stage1=%d raw=%v quant=%v interpBase=%.6f interpRes=%v",
			trace.NLSF.InterpIdx, trace.NLSF.Stage1Idx, shortInt16(trace.NLSF.RawNLSFQ15, 16), shortInt16(trace.NLSF.QuantizedNLSFQ15, 16),
			trace.NLSF.InterpBaseResNrg, trace.NLSF.InterpResNrgQ2)
		t.Logf("  gain: before=%v resNrg=%v after=%v gainsUnq=%v lastGainPrev=%d qOff=%d->%d seed=%d->%d",
			shortFloat32(trace.GainLoop.GainsBefore[:], trace.GainLoop.NumSubframes),
			shortFloat32(trace.GainLoop.ResNrgBefore[:], trace.GainLoop.NumSubframes),
			shortFloat32(trace.GainLoop.GainsAfter[:], trace.GainLoop.NumSubframes),
			shortInt32(trace.GainLoop.GainsUnqQ16[:], trace.GainLoop.NumSubframes),
			trace.GainLoop.LastGainIndexPrev, trace.GainLoop.QuantOffsetBefore, trace.GainLoop.QuantOffsetAfter,
			trace.GainLoop.SeedIn, trace.GainLoop.SeedOut)
		t.Logf("  nsq: gainsQ16=%v harm=%v tilt=%v lf=%v interp=%d lambda=%d seed=%d->%d",
			shortInt32(trace.NSQ.GainsQ16, trace.NSQ.NbSubfr),
			shortInt(trace.NSQ.HarmShapeGainQ14, trace.NSQ.NbSubfr),
			shortInt(trace.NSQ.TiltQ14, trace.NSQ.NbSubfr),
			shortInt32(trace.NSQ.LFShpQ14, trace.NSQ.NbSubfr),
			trace.NSQ.NLSFInterpCoefQ2, trace.NSQ.LambdaQ10, trace.NSQ.SeedIn, trace.NSQ.SeedOut)
		for _, iter := range trace.GainLoop.Iterations {
			t.Logf("  iter %d: gainMultQ8=%d gainsID=%d idx=%v gainsQ16=%v bits=%d seed=%d/%d/%d skipped=%v",
				iter.Iter, iter.GainMultQ8, iter.GainsID,
				shortInt8(iter.GainsIndices[:], trace.GainLoop.NumSubframes),
				shortInt32(iter.GainsQ16[:], trace.GainLoop.NumSubframes),
				iter.Bits, iter.SeedIn, iter.SeedAfterNSQ, iter.SeedOut, iter.SkippedNSQ)
		}
	}
}

func shortInt16(in []int16, n int) []int16 {
	if n > len(in) {
		n = len(in)
	}
	out := make([]int16, n)
	copy(out, in[:n])
	return out
}

func shortFloat32(in []float32, n int) []float32 {
	if n > len(in) {
		n = len(in)
	}
	out := make([]float32, n)
	copy(out, in[:n])
	return out
}

func shortInt32[T ~int32](in []T, n int) []T {
	if n > len(in) {
		n = len(in)
	}
	out := make([]T, n)
	copy(out, in[:n])
	return out
}

func shortInt(in []int, n int) []int {
	if n > len(in) {
		n = len(in)
	}
	out := make([]int, n)
	copy(out, in[:n])
	return out
}

func shortInt8(in []int8, n int) []int8 {
	if n > len(in) {
		n = len(in)
	}
	out := make([]int8, n)
	copy(out, in[:n])
	return out
}
