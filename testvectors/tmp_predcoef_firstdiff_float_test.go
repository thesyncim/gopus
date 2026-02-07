//go:build cgo_libopus

package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpPredCoefFirstDiffFloatCapture(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
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

	samplesPerFrame := frameSize * channels
	firstA0 := -1
	firstA1 := -1
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		goSnap := cloneNSQTrace(nsqTrace)
		libSnap, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, i)
		if !ok {
			t.Fatalf("failed to capture lib NSQ inputs at frame %d", i)
		}
		if firstA0 < 0 {
			for k := 0; k < 16; k++ {
				if goSnap.PredCoefQ12[k] != libSnap.PredCoefQ12[k] {
					firstA0 = i
					t.Logf("first a0 diff frame=%d idx=%d go=%d lib=%d", i, k, goSnap.PredCoefQ12[k], libSnap.PredCoefQ12[k])
					break
				}
			}
		}
		if firstA1 < 0 {
			for k := 0; k < 16; k++ {
				gi := 16 + k
				if goSnap.PredCoefQ12[gi] != libSnap.PredCoefQ12[gi] {
					firstA1 = i
					t.Logf("first a1 diff frame=%d idx=%d go=%d lib=%d", i, k, goSnap.PredCoefQ12[gi], libSnap.PredCoefQ12[gi])
					break
				}
			}
		}
		if firstA0 >= 0 && firstA1 >= 0 {
			break
		}
	}
	t.Logf("first a0 diff frame=%d", firstA0)
	t.Logf("first a1 diff frame=%d", firstA1)
}
