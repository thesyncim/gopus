package silk

import (
	"math"
	"testing"
)

func makeStereoTestFrame(frameLength, sampleRate int) (left, right []float32) {
	left = make([]float32, frameLength)
	right = make([]float32, frameLength)
	for i := 0; i < frameLength; i++ {
		tm := float64(i) / float64(sampleRate)
		// Use distinct channels so side stays active and rate-split matters.
		left[i] = 0.35 * float32(math.Sin(2*math.Pi*430.0*tm))
		right[i] = 0.25*float32(math.Sin(2*math.Pi*910.0*tm+0.6)) + 0.10*left[i]
	}
	return left, right
}

func TestEncodeStereoAppliesPerChannelRateSplit(t *testing.T) {
	const (
		bw         = BandwidthWideband
		sampleRate = 16000
		frameLen   = 320 // 20 ms at 16 kHz
		totalRate  = 32000
	)

	left, right := makeStereoTestFrame(frameLen, sampleRate)

	// Compute expected split from the stereo front-end on a fresh encoder.
	calcEnc := NewEncoder(bw)
	leftFrame := stereoFrameWithLookahead(left, 0, frameLen)
	rightFrame := stereoFrameWithLookahead(right, 0, frameLen)
	_, _, _, midOnly, expMidRate, expSideRate, _ := calcEnc.StereoLRToMSWithRates(
		leftFrame, rightFrame, frameLen, sampleRate/1000, totalRate, calcEnc.speechActivityQ8, false,
	)
	if midOnly {
		t.Fatal("expected non-mid-only frame for stereo rate split test")
	}
	if expMidRate <= 0 || expSideRate <= 0 {
		t.Fatalf("invalid expected split: mid=%d side=%d", expMidRate, expSideRate)
	}

	enc := NewEncoder(bw)
	sideEnc := NewEncoder(bw)
	enc.SetBitrate(totalRate)
	sideEnc.SetBitrate(totalRate)

	pkt, err := EncodeStereoWithEncoderVADFlags(enc, sideEnc, left, right, bw, []bool{true})
	if err != nil {
		t.Fatalf("encode stereo failed: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("encode stereo returned empty packet")
	}

	if got := enc.lastControlTargetRateBps; got != expMidRate {
		t.Fatalf("mid bitrate mismatch: got %d want %d", got, expMidRate)
	}
	if got := sideEnc.lastControlTargetRateBps; got != expSideRate {
		t.Fatalf("side bitrate mismatch: got %d want %d", got, expSideRate)
	}
	if sideEnc.lastControlTargetRateBps == totalRate {
		t.Fatalf("side bitrate was not split (still total rate %d)", totalRate)
	}
}

func TestEncodeStereoAppliesPerChannelRateSplitWithVADCarryover(t *testing.T) {
	const (
		bw         = BandwidthWideband
		sampleRate = 16000
		frameLen   = 320 // 20 ms at 16 kHz
		totalRate  = 32000
	)

	left, right := makeStereoTestFrame(frameLen, sampleRate)

	calcEnc := NewEncoder(bw)
	calcEnc.SetVADState(200, 0, [4]int{})
	leftFrame := stereoFrameWithLookahead(left, 0, frameLen)
	rightFrame := stereoFrameWithLookahead(right, 0, frameLen)
	_, _, _, midOnly, expMidRate, expSideRate, _ := calcEnc.StereoLRToMSWithRates(
		leftFrame, rightFrame, frameLen, sampleRate/1000, totalRate, calcEnc.speechActivityQ8, false,
	)
	if midOnly {
		t.Fatal("expected non-mid-only frame for stereo rate split test with VAD carryover")
	}

	enc := NewEncoder(bw)
	sideEnc := NewEncoder(bw)
	enc.SetBitrate(totalRate)
	sideEnc.SetBitrate(totalRate)
	enc.SetVADState(200, 0, [4]int{})

	pkt, err := EncodeStereoWithEncoderVADFlags(enc, sideEnc, left, right, bw, []bool{true})
	if err != nil {
		t.Fatalf("encode stereo failed: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("encode stereo returned empty packet")
	}

	if got := enc.lastControlTargetRateBps; got != expMidRate {
		t.Fatalf("mid bitrate mismatch with VAD carryover: got %d want %d", got, expMidRate)
	}
	if got := sideEnc.lastControlTargetRateBps; got != expSideRate {
		t.Fatalf("side bitrate mismatch with VAD carryover: got %d want %d", got, expSideRate)
	}
}
