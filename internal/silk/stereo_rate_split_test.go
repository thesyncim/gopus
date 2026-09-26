package silk

import (
	"math"
	"testing"
)

func makeStereoTestFrame(frameLength, sampleRate int) (left, right []float32) {
	left = make([]float32, frameLength)
	right = make([]float32, frameLength)
	for i := range frameLength {
		tm := float64(i) / float64(sampleRate)
		// Use distinct channels so side stays active and rate-split matters.
		left[i] = 0.35 * float32(math.Sin(2*math.Pi*430.0*tm))
		right[i] = 0.25*float32(math.Sin(2*math.Pi*910.0*tm+0.6)) + 0.10*left[i]
	}
	return left, right
}

// TestEncodeStereoAppliesPerChannelRateSplit checks that each channel's
// silk_control_SNR runs at its share of the silk_stereo_LR_to_MS rate split
// (silk/enc_API.c), and that the split follows the previous frame's speech
// activity.
func TestEncodeStereoAppliesPerChannelRateSplit(t *testing.T) {
	const (
		bw         = BandwidthWideband
		sampleRate = 16000
		frameLen   = 320 // 20 ms at 16 kHz
		totalRate  = 32000
	)
	left, right := makeStereoTestFrame(frameLen, sampleRate)
	pcm := interleaveStereo(left, right)

	var mids [2]int32
	for i, prevActivityQ8 := range []int32{0, 200} {
		p := newTestPacketEncoder(bw, 2)
		p.ctl.BitRate = totalRate
		p.enc.state[0].speechActivityQ8 = prevActivityQ8
		if pkt := p.encodeInto(t, pcm, 1); len(pkt) == 0 {
			t.Fatal("encode stereo returned empty packet")
		}
		if p.enc.stereo.midOnlyFlags[0] != 0 {
			t.Fatalf("activity %d: expected a frame with side coding", prevActivityQ8)
		}
		mid := p.enc.state[0].targetRateBps
		side := p.enc.state[1].targetRateBps
		if mid <= 0 || side <= 0 {
			t.Fatalf("activity %d: invalid split mid=%d side=%d", prevActivityQ8, mid, side)
		}
		// The first packet targets the full rate; the split shares it less
		// 600 bps for the stereo parameters of a 20 ms frame.
		if mid+side != totalRate-600 {
			t.Fatalf("activity %d: mid %d + side %d != %d", prevActivityQ8, mid, side, totalRate-600)
		}
		mids[i] = mid
	}
	if mids[0] == mids[1] {
		t.Fatalf("rate split ignores the previous speech activity: mid=%d for both", mids[0])
	}
}
