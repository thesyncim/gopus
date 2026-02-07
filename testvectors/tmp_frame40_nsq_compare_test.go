//go:build cgo_libopus

package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpFrame40NSQInputCompare(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
		targetFrame = 40
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nsqTrace := &silk.NSQTrace{CaptureInputs: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{NSQ: nsqTrace})

	var snap silk.NSQTrace
	samplesPerFrame := frameSize * channels
	for i := 0; i <= targetFrame; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		if i == targetFrame {
			snap = cloneNSQTrace(nsqTrace)
		}
	}

	samplesInt16 := make([]int16, len(original))
	for i, s := range original {
		v := int32(math.RoundToEven(float64(s * 32768.0)))
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		samplesInt16[i] = int16(v)
	}
	lib, ok := captureLibopusOpusNSQInputsAtFrameInt16(samplesInt16, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture libopus NSQ inputs for frame %d", targetFrame)
	}
	libFloat, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture libopus FLOAT NSQ inputs for frame %d", targetFrame)
	}
	libState, ok := captureLibopusNSQState(original, sampleRate, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture libopus NSQ state for frame %d", targetFrame)
	}
	if msg := compareNSQTraceWithLibopus(snap); msg != "" {
		t.Logf("nsq compare: %s", msg)
	} else {
		t.Log("nsq compare: identical")
	}

	if idx, gv, lv, ok := firstInt16Diff(snap.PredCoefQ12, lib.PredCoefQ12); ok {
		t.Logf("predCoef diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("predCoef: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.LTPCoefQ14, lib.LTPCoefQ14); ok {
		t.Logf("ltpCoef diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("ltpCoef: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.ARShpQ13, lib.ARQ13); ok {
		t.Logf("AR diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("AR: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.InputQ0, lib.X16); ok {
		t.Logf("x16 diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("x16: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.InputQ0, libFloat.X16); ok {
		t.Logf("x16(float-capture) diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("x16(float-capture): identical")
	}
	if idx, gv, lv, ok := firstInt32Diff(snap.GainsQ16, lib.GainsQ16); ok {
		t.Logf("gainsQ16 diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("gainsQ16: identical")
	}
	pitchDiff := false
	pitchMinLen := len(snap.PitchL)
	if len(lib.PitchL) < pitchMinLen {
		pitchMinLen = len(lib.PitchL)
	}
	for i := 0; i < pitchMinLen; i++ {
		if snap.PitchL[i] != lib.PitchL[i] {
			t.Logf("pitchL diff idx=%d go=%d lib=%d", i, snap.PitchL[i], lib.PitchL[i])
			pitchDiff = true
			break
		}
	}
	if !pitchDiff {
		t.Log("pitchL: identical")
	}
	if snap.SignalType != lib.SignalType || snap.QuantOffsetType != lib.QuantOffsetType ||
		snap.NLSFInterpCoefQ2 != lib.NLSFInterpCoefQ2 || snap.SeedIn != lib.SeedIn ||
		snap.LambdaQ10 != lib.LambdaQ10 || snap.LTPScaleQ14 != lib.LTPScaleQ14 {
		t.Logf("scalars diff: sig go=%d lib=%d qOff go=%d lib=%d interp go=%d lib=%d seed go=%d lib=%d lambda go=%d lib=%d ltpScale go=%d lib=%d",
			snap.SignalType, lib.SignalType,
			snap.QuantOffsetType, lib.QuantOffsetType,
			snap.NLSFInterpCoefQ2, lib.NLSFInterpCoefQ2,
			snap.SeedIn, lib.SeedIn,
			snap.LambdaQ10, lib.LambdaQ10,
			snap.LTPScaleQ14, lib.LTPScaleQ14,
		)
	} else {
		t.Log("scalars: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.NSQXQ, libState.XQ); ok {
		t.Logf("pre-state xq diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("pre-state xq: identical")
	}
	if idx, gv, lv, ok := firstInt32Diff(snap.NSQSLTPShpQ14, libState.SLTPShpQ14); ok {
		t.Logf("pre-state sLTP_shp diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("pre-state sLTP_shp: identical")
	}
	if idx, gv, lv, ok := firstInt32Diff(snap.NSQLPCQ14, libState.SLPCQ14); ok {
		t.Logf("pre-state sLPC diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("pre-state sLPC: identical")
	}
	if idx, gv, lv, ok := firstInt32Diff(snap.NSQAR2Q14, libState.SAR2Q14); ok {
		t.Logf("pre-state sAR2 diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("pre-state sAR2: identical")
	}
}
