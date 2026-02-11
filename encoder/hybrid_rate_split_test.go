package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func makeHybridStereoPCM16k(samples int) []float32 {
	pcm := make([]float32, samples*2)
	const fs = 16000.0
	for i := 0; i < samples; i++ {
		tm := float64(i) / fs
		left := 0.36*math.Sin(2*math.Pi*360.0*tm) + 0.14*math.Sin(2*math.Pi*920.0*tm)
		// Near-mono but not identical, so stereo split remains active.
		right := 0.82*left + 0.06*math.Sin(2*math.Pi*1350.0*tm+0.35)
		pcm[2*i] = float32(left)
		pcm[2*i+1] = float32(right)
	}
	return pcm
}

func TestHybridStereoAppliesSilkRateSplit(t *testing.T) {
	const (
		silkSamples = 320 // 20 ms at 16 kHz
		totalRate   = 30000
	)

	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)
	enc.SetSignalType(types.SignalVoice)

	enc.ensureSILKEncoder()
	enc.ensureSILKSideEncoder()

	// Attach shared range encoder so encodeSILKHybridStereo runs its full path.
	var re rangecoding.Encoder
	re.Init(make([]byte, 2048))
	enc.silkEncoder.SetRangeEncoder(&re)
	enc.silkSideEncoder.SetRangeEncoder(&re)

	midTrace := &silk.EncoderTrace{Frame: &silk.FrameStateTrace{}}
	sideTrace := &silk.EncoderTrace{Frame: &silk.FrameStateTrace{}}
	enc.silkEncoder.SetTrace(midTrace)
	enc.silkSideEncoder.SetTrace(sideTrace)

	pcm := makeHybridStereoPCM16k(silkSamples)

	// Build the same left/right + lookahead slices used internally.
	left := make([]float32, silkSamples+2)
	right := make([]float32, silkSamples+2)
	for i := 0; i < silkSamples; i++ {
		left[i] = pcm[2*i]
		right[i] = pcm[2*i+1]
	}
	left[silkSamples] = left[silkSamples-1]
	left[silkSamples+1] = left[silkSamples-1]
	right[silkSamples] = right[silkSamples-1]
	right[silkSamples+1] = right[silkSamples-1]

	// Compute expected rates from the same stereo split function.
	calcEnc := silk.NewEncoder(silk.BandwidthWideband)
	_, _, _, _, expMidRate, expSideRate, _ := calcEnc.StereoLRToMSWithRates(
		left, right, silkSamples, 16, totalRate, enc.lastVADActivityQ8, false,
	)
	if expMidRate <= 0 || expSideRate <= 0 {
		t.Fatalf("invalid expected split rates: mid=%d side=%d", expMidRate, expSideRate)
	}

	enc.encodeSILKHybridStereo(pcm, nil, silkSamples, totalRate)

	midRate := midTrace.Frame.InputRateBps
	sideRate := sideTrace.Frame.InputRateBps
	if midRate != expMidRate || sideRate != expSideRate {
		t.Fatalf("unexpected hybrid SILK split: got mid=%d side=%d want mid=%d side=%d",
			midRate, sideRate, expMidRate, expSideRate)
	}
}
