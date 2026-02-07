//go:build cgo_libopus

package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpFrame28NSQCompareFloat(t *testing.T) {
	const (
		sampleRate  = 48000
		channels    = 1
		frameSize   = 960
		bitrate     = 32000
		targetFrame = 28
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

	lib, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, targetFrame)
	if !ok {
		t.Fatalf("failed to capture lib NSQ inputs for frame %d", targetFrame)
	}

	if idx, gv, lv, ok := firstInt16Diff(snap.InputQ0, lib.X16); ok {
		t.Logf("x16 first diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("x16: identical")
	}
	if idx, gv, lv, ok := firstInt16Diff(snap.PredCoefQ12, lib.PredCoefQ12); ok {
		t.Logf("pred first diff idx=%d go=%d lib=%d", idx, gv, lv)
	} else {
		t.Log("pred: identical")
	}
}
