//go:build cgo_libopus

package testvectors

import "testing"

func TestOpusNSQStateCaptureMatchesHashSnapshot(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
		numFrames  = 8
	)

	samples := generateEncoderTestSignal(frameSize*numFrames*channels, channels)
	frames := []int{0, 1, 3, 7}
	for _, frameIdx := range frames {
		stateSnap, ok := captureLibopusOpusSilkStateBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIdx)
		if !ok {
			t.Fatalf("frame %d: failed to capture opus silk state snapshot", frameIdx)
		}
		nsqSnap, ok := captureLibopusOpusNSQStateBeforeFrame(samples, sampleRate, channels, bitrate, frameSize, frameIdx)
		if !ok {
			t.Fatalf("frame %d: failed to capture opus NSQ state snapshot", frameIdx)
		}

		if got := hashInt16Slice(nsqSnap.XQ); got != stateSnap.NSQXQHash {
			t.Fatalf("frame %d: nsq xq hash mismatch got=%d want=%d", frameIdx, got, stateSnap.NSQXQHash)
		}
		if got := hashInt32Slice(nsqSnap.SLTPShpQ14); got != stateSnap.NSQSLTPShpHash {
			t.Fatalf("frame %d: nsq sLTP_shp hash mismatch got=%d want=%d", frameIdx, got, stateSnap.NSQSLTPShpHash)
		}
		if got := hashInt32Slice(nsqSnap.SLPCQ14); got != stateSnap.NSQSLPCHash {
			t.Fatalf("frame %d: nsq sLPC hash mismatch got=%d want=%d", frameIdx, got, stateSnap.NSQSLPCHash)
		}
		if got := hashInt32Slice(nsqSnap.SAR2Q14); got != stateSnap.NSQSAR2Hash {
			t.Fatalf("frame %d: nsq sAR2 hash mismatch got=%d want=%d", frameIdx, got, stateSnap.NSQSAR2Hash)
		}

		if nsqSnap.LagPrev != stateSnap.NSQLagPrev || nsqSnap.SLTPBufIdx != stateSnap.NSQSLTPBufIdx || nsqSnap.SLTPShpBufIdx != stateSnap.NSQSLTPShpBufIdx {
			t.Fatalf("frame %d: nsq idx mismatch lagPrev %d/%d sLTPBufIdx %d/%d sLTPShpBufIdx %d/%d",
				frameIdx,
				nsqSnap.LagPrev, stateSnap.NSQLagPrev,
				nsqSnap.SLTPBufIdx, stateSnap.NSQSLTPBufIdx,
				nsqSnap.SLTPShpBufIdx, stateSnap.NSQSLTPShpBufIdx,
			)
		}
		if nsqSnap.PrevGainQ16 != stateSnap.NSQPrevGainQ16 || nsqSnap.RandSeed != stateSnap.NSQRandSeed || nsqSnap.RewhiteFlag != stateSnap.NSQRewhiteFlag {
			t.Fatalf("frame %d: nsq scalar mismatch prevGain %d/%d randSeed %d/%d rewhite %d/%d",
				frameIdx,
				nsqSnap.PrevGainQ16, stateSnap.NSQPrevGainQ16,
				nsqSnap.RandSeed, stateSnap.NSQRandSeed,
				nsqSnap.RewhiteFlag, stateSnap.NSQRewhiteFlag,
			)
		}
	}
}
