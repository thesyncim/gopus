//go:build cgo_libopus

package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestTmpNSQStateFirstDiff(t *testing.T) {
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
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		if _, err := goEnc.Encode(pcm, frameSize); err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}

		libState, ok := captureLibopusNSQState(original, sampleRate, bitrate, frameSize, i)
		if !ok {
			t.Fatalf("failed to capture libopus NSQ state at frame %d", i)
		}
		if idx, gv, lv, ok := firstInt16Diff(nsqTrace.NSQXQ, libState.XQ); ok {
			t.Logf("first NSQ xq state diff at frame=%d idx=%d go=%d lib=%d", i, idx, gv, lv)
			if idx2, gv2, lv2, ok2 := firstInt32Diff(nsqTrace.NSQSLTPShpQ14, libState.SLTPShpQ14); ok2 {
				t.Logf("first NSQ sLTP_shp state diff at frame=%d idx=%d go=%d lib=%d", i, idx2, gv2, lv2)
			}
			if idx3, gv3, lv3, ok3 := firstInt32Diff(nsqTrace.NSQLPCQ14, libState.SLPCQ14); ok3 {
				t.Logf("first NSQ sLPC state diff at frame=%d idx=%d go=%d lib=%d", i, idx3, gv3, lv3)
			}
			if idx4, gv4, lv4, ok4 := firstInt32Diff(nsqTrace.NSQAR2Q14, libState.SAR2Q14); ok4 {
				t.Logf("first NSQ sAR2 state diff at frame=%d idx=%d go=%d lib=%d", i, idx4, gv4, lv4)
			}
			return
		}
	}

	t.Log("no NSQ state diffs found")
}

