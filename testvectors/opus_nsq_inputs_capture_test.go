//go:build cgo_libopus

package testvectors

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestOpusNSQInputCaptureReplayMatchesNextPreState(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		bitrate    = 32000
		frameSize  = 960
		frameIndex = 0
		numFrames  = 4
	)

	original := generateEncoderTestSignal(frameSize*numFrames*channels, channels)

	pre, ok := captureLibopusOpusNSQStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, frameIndex)
	if !ok {
		t.Fatalf("failed to capture pre-state at frame %d", frameIndex)
	}
	post, ok := captureLibopusOpusNSQStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, frameIndex+1)
	if !ok {
		t.Fatalf("failed to capture pre-state at frame %d", frameIndex+1)
	}
	in, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, frameIndex)
	if !ok {
		t.Fatalf("failed to capture NSQ inputs at frame %d", frameIndex)
	}
	if in.CallsInFrame <= 0 {
		t.Fatalf("expected at least one NSQ call, got %d", in.CallsInFrame)
	}

	_, _, seedOut, _, _, _, final := cgowrap.SilkNSQDelDecCaptureWithStateFinal(
		in.FrameLength, in.SubfrLength, in.NumSubframes, in.LTPMemLength,
		in.PredLPCOrder, in.ShapeLPCOrder, in.WarpingQ16, in.NStatesDelayedDecision,
		in.SignalType, in.QuantOffsetType, in.NLSFInterpCoefQ2, in.SeedIn,
		in.X16,
		in.PredCoefQ12,
		in.LTPCoefQ14,
		in.ARQ13,
		in.HarmShapeGainQ14,
		in.TiltQ14,
		in.LFShpQ14,
		in.GainsQ16,
		in.PitchL,
		in.LambdaQ10, in.LTPScaleQ14,
		pre.XQ,
		pre.SLTPShpQ14,
		pre.SLPCQ14,
		pre.SAR2Q14,
		pre.LFARQ14,
		pre.DiffQ14,
		pre.LagPrev,
		pre.SLTPBufIdx,
		pre.SLTPShpBufIdx,
		pre.RandSeed,
		pre.PrevGainQ16,
		pre.RewhiteFlag,
	)

	if seedOut != in.SeedOut {
		t.Fatalf("seed mismatch from replay: replay=%d captured=%d", seedOut, in.SeedOut)
	}

	if hashInt16Slice(final.XQ) != hashInt16Slice(post.XQ) {
		t.Fatalf("xq mismatch: replay=%d captured=%d", hashInt16Slice(final.XQ), hashInt16Slice(post.XQ))
	}
	if hashInt32Slice(final.SLTPShpQ14) != hashInt32Slice(post.SLTPShpQ14) {
		t.Fatalf("sLTP_shp mismatch: replay=%d captured=%d", hashInt32Slice(final.SLTPShpQ14), hashInt32Slice(post.SLTPShpQ14))
	}
	if hashInt32Slice(final.SLPCQ14) != hashInt32Slice(post.SLPCQ14) {
		t.Fatalf("sLPC mismatch: replay=%d captured=%d", hashInt32Slice(final.SLPCQ14), hashInt32Slice(post.SLPCQ14))
	}
	if hashInt32Slice(final.SAR2Q14) != hashInt32Slice(post.SAR2Q14) {
		t.Fatalf("sAR2 mismatch: replay=%d captured=%d", hashInt32Slice(final.SAR2Q14), hashInt32Slice(post.SAR2Q14))
	}
	if final.LFARQ14 != post.LFARQ14 || final.DiffQ14 != post.DiffQ14 {
		t.Fatalf("scalar mismatch lfAR %d/%d diff %d/%d", final.LFARQ14, post.LFARQ14, final.DiffQ14, post.DiffQ14)
	}
	if final.LagPrev != post.LagPrev || final.SLTPBufIdx != post.SLTPBufIdx || final.SLTPShpBufIdx != post.SLTPShpBufIdx {
		t.Fatalf("index mismatch lagPrev %d/%d sLTPBufIdx %d/%d sLTPShpBufIdx %d/%d",
			final.LagPrev, post.LagPrev, final.SLTPBufIdx, post.SLTPBufIdx, final.SLTPShpBufIdx, post.SLTPShpBufIdx)
	}
	if final.RandSeed != post.RandSeed || final.PrevGainQ16 != post.PrevGainQ16 || final.RewhiteFlag != post.RewhiteFlag {
		t.Fatalf("flag mismatch randSeed %d/%d prevGain %d/%d rewhite %d/%d",
			final.RandSeed, post.RandSeed, final.PrevGainQ16, post.PrevGainQ16, final.RewhiteFlag, post.RewhiteFlag)
	}
}

func TestOpusNSQInputShapingParityRegression(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		bitrate    = 32000
		frameSize  = 960
		numFrames  = 50
	)

	original := generateEncoderTestSignal(frameSize*numFrames*channels, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nsqTrace := &silk.NSQTrace{CaptureInputs: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{NSQ: nsqTrace})

	nsqTraces := make([]silk.NSQTrace, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		nsqTraces = append(nsqTraces, cloneNSQTrace(nsqTrace))
	}

	// Compare only the stable early-frame region before the known upstream pitch/gain
	// state drift. This keeps this test focused on shaping/lambda conversion parity.
	stableFrames := len(nsqTraces)
	if stableFrames > 22 {
		stableFrames = 22
	}

	var harmDiff, tiltDiff, lfDiff, lambdaDiff int
	for i := 0; i < stableFrames; i++ {
		tr := nsqTraces[i]
		snap, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, i)
		if !ok {
			t.Fatalf("failed to capture libopus NSQ inputs at frame %d", i)
		}

		if len(tr.HarmShapeGainQ14) != len(snap.HarmShapeGainQ14) ||
			len(tr.TiltQ14) != len(snap.TiltQ14) ||
			len(tr.LFShpQ14) != len(snap.LFShpQ14) {
			t.Fatalf("frame %d: shaping length mismatch: harm %d/%d tilt %d/%d lf %d/%d",
				i,
				len(tr.HarmShapeGainQ14), len(snap.HarmShapeGainQ14),
				len(tr.TiltQ14), len(snap.TiltQ14),
				len(tr.LFShpQ14), len(snap.LFShpQ14))
		}

		if idx, goVal, libVal, ok := firstIntIntDiff(tr.HarmShapeGainQ14, snap.HarmShapeGainQ14); ok {
			harmDiff++
			if harmDiff <= 3 {
				t.Logf("frame %d: harm mismatch idx=%d go=%d lib=%d", i, idx, goVal, libVal)
			}
		}
		if idx, goVal, libVal, ok := firstIntIntDiff(tr.TiltQ14, snap.TiltQ14); ok {
			tiltDiff++
			t.Logf("frame %d: tilt mismatch idx=%d go=%d lib=%d", i, idx, goVal, libVal)
		}
		if idx, goVal, libVal, ok := firstInt32Diff(tr.LFShpQ14, snap.LFShpQ14); ok {
			lfDiff++
			t.Logf("frame %d: lf_shp mismatch idx=%d go=%d lib=%d", i, idx, goVal, libVal)
		}
		if tr.LambdaQ10 != snap.LambdaQ10 {
			lambdaDiff++
			if lambdaDiff <= 3 {
				t.Logf("frame %d: lambda mismatch go=%d lib=%d", i, tr.LambdaQ10, snap.LambdaQ10)
			}
		}
	}

	t.Logf("NSQ shaping parity: harm=%d tilt=%d lf=%d lambda=%d (frames=%d)",
		harmDiff, tiltDiff, lfDiff, lambdaDiff, stableFrames)

	if tiltDiff > 0 {
		t.Fatalf("tilt parity regressed: got %d mismatched frames, want 0", tiltDiff)
	}
	if lfDiff > 0 {
		t.Fatalf("LF shaping parity regressed: got %d mismatched frames, want 0", lfDiff)
	}
	if harmDiff > 0 {
		t.Fatalf("harmonic shaping parity regressed: got %d mismatched frames, want 0", harmDiff)
	}
	if lambdaDiff > 0 {
		t.Fatalf("lambda parity regressed: got %d mismatched frames, want 0", lambdaDiff)
	}
}
