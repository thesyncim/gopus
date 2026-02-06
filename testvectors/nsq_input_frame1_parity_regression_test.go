//go:build cgo_libopus

package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestNSQInputFrame1ParityRegression(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		bitrate    = 32000
		frameSize  = 960
		numFrames  = 4
		frameIndex = 1
	)

	original := generateEncoderTestSignal(frameSize*numFrames*channels, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nsqTrace := &silk.NSQTrace{CaptureInputs: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{NSQ: nsqTrace})

	samplesPerFrame := frameSize * channels
	var tr silk.NSQTrace
	for i := 0; i <= frameIndex; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		tr = cloneNSQTrace(nsqTrace)
	}

	libIn, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, frameIndex)
	if !ok {
		t.Fatalf("failed to capture libopus NSQ inputs at frame %d", frameIndex)
	}

	if tr.FrameLength != libIn.FrameLength || tr.SubfrLength != libIn.SubfrLength || tr.NbSubfr != libIn.NumSubframes {
		t.Fatalf("frame params mismatch: go frame=%d subfr=%d nb=%d lib frame=%d subfr=%d nb=%d",
			tr.FrameLength, tr.SubfrLength, tr.NbSubfr, libIn.FrameLength, libIn.SubfrLength, libIn.NumSubframes)
	}
	if idx, goVal, libVal, ok := firstInt16Diff(tr.InputQ0[:libIn.FrameLength], libIn.X16); ok {
		t.Fatalf("x16 mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstInt16Diff(tr.PredCoefQ12, libIn.PredCoefQ12); ok {
		t.Fatalf("pred mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstInt16Diff(tr.LTPCoefQ14, libIn.LTPCoefQ14); ok {
		t.Fatalf("ltp mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstInt16Diff(tr.ARShpQ13, libIn.ARQ13); ok {
		t.Fatalf("ar mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstIntIntDiff(tr.HarmShapeGainQ14, libIn.HarmShapeGainQ14); ok {
		t.Fatalf("harm mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstIntIntDiff(tr.TiltQ14, libIn.TiltQ14); ok {
		t.Fatalf("tilt mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstInt32Diff(tr.LFShpQ14, libIn.LFShpQ14); ok {
		t.Fatalf("lf mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstInt32Diff(tr.GainsQ16, libIn.GainsQ16); ok {
		t.Fatalf("gains mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if idx, goVal, libVal, ok := firstIntIntDiff(tr.PitchL, libIn.PitchL); ok {
		t.Fatalf("pitch mismatch idx=%d go=%d lib=%d", idx, goVal, libVal)
	}
	if tr.SignalType != libIn.SignalType ||
		tr.QuantOffsetType != libIn.QuantOffsetType ||
		tr.NLSFInterpCoefQ2 != libIn.NLSFInterpCoefQ2 ||
		tr.SeedIn != libIn.SeedIn ||
		tr.LambdaQ10 != libIn.LambdaQ10 ||
		tr.LTPScaleQ14 != libIn.LTPScaleQ14 ||
		tr.WarpingQ16 != libIn.WarpingQ16 ||
		tr.NStatesDelayedDecision != libIn.NStatesDelayedDecision {
		t.Fatalf("scalar mismatch: signal %d/%d qOff %d/%d interp %d/%d seedIn %d/%d lambda %d/%d ltpScale %d/%d warping %d/%d nStates %d/%d",
			tr.SignalType, libIn.SignalType,
			tr.QuantOffsetType, libIn.QuantOffsetType,
			tr.NLSFInterpCoefQ2, libIn.NLSFInterpCoefQ2,
			tr.SeedIn, libIn.SeedIn,
			tr.LambdaQ10, libIn.LambdaQ10,
			tr.LTPScaleQ14, libIn.LTPScaleQ14,
			tr.WarpingQ16, libIn.WarpingQ16,
			tr.NStatesDelayedDecision, libIn.NStatesDelayedDecision,
		)
	}
}
