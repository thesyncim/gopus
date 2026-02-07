//go:build cgo_libopus
// +build cgo_libopus

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestRunPrefilterParityAgainstLibopus(t *testing.T) {
	const iters = 300

	rng := rand.New(rand.NewSource(20260207))
	var (
		onMismatch    int
		pitchMismatch int
		qgMismatch    int
		gainMismatch  int
		maxPitchDiff  int
		maxGainDiff   float64
		loggedPitch   int
	)

	for iter := 0; iter < iters; iter++ {
		channels := 1
		if rng.Intn(2) == 1 {
			channels = 2
		}
		frameSizes := []int{120, 240, 480, 960}
		frameSize := frameSizes[rng.Intn(len(frameSizes))]
		complexity := rng.Intn(11)

		enc := NewEncoder(channels)
		prevPeriod := rng.Intn(combFilterMaxPeriod)
		prevGain := rng.Float64() * 0.8
		prevTapset := rng.Intn(3)
		enc.prefilterPeriod = prevPeriod
		enc.prefilterGain = prevGain
		enc.prefilterTapset = prevTapset
		enc.complexity = complexity

		for i := range enc.prefilterMem {
			enc.prefilterMem[i] = (rng.Float64()*2 - 1) * CELTSigScale
		}

		preemph := make([]float64, frameSize*channels)
		for i := range preemph {
			preemph[i] = (rng.Float64()*2 - 1) * CELTSigScale
		}

		pre := make([]float64, channels*(combFilterMaxPeriod+frameSize))
		for ch := 0; ch < channels; ch++ {
			copy(pre[ch*(combFilterMaxPeriod+frameSize):ch*(combFilterMaxPeriod+frameSize)+combFilterMaxPeriod],
				enc.prefilterMem[ch*combFilterMaxPeriod:(ch+1)*combFilterMaxPeriod])
			for i := 0; i < frameSize; i++ {
				pre[ch*(combFilterMaxPeriod+frameSize)+combFilterMaxPeriod+i] = preemph[i*channels+ch]
			}
		}

		prefilterTapset := rng.Intn(3)
		enabled := rng.Intn(2) == 1
		tfEstimate := rng.Float64()
		nbAvailableBytes := 5 + rng.Intn(90)
		toneFreq := -1.0
		if rng.Intn(4) != 0 {
			toneFreq = rng.Float64() * math.Pi
		}
		toneishness := rng.Float64()
		maxPitchRatio := rng.Float64()

		goInput := make([]float64, len(preemph))
		copy(goInput, preemph)
		goRes := enc.runPrefilter(goInput, frameSize, prefilterTapset, enabled, tfEstimate, nbAvailableBytes, toneFreq, toneishness, maxPitchRatio)

		overlap := Overlap
		if overlap > frameSize {
			overlap = frameSize
		}
		window := GetWindowBuffer(Overlap)
		libRes := libopusRunPrefilterRef(pre, channels, frameSize,
			prefilterTapset,
			prevPeriod,
			prevGain,
			prevTapset,
			enabled,
			complexity,
			tfEstimate,
			nbAvailableBytes,
			toneFreq,
			toneishness,
			maxPitchRatio,
			window,
			overlap,
		)

		if goRes.on != libRes.on {
			onMismatch++
		}
		pd := int(math.Abs(float64(goRes.pitch - libRes.pitch)))
		if pd > 1 {
			pitchMismatch++
			if pd > maxPitchDiff {
				maxPitchDiff = pd
			}
			if loggedPitch < 12 {
				t.Logf("pitch-mismatch iter=%d ch=%d fs=%d enabled=%v complexity=%d prevP=%d prevG=%.4f prevTap=%d tap=%d tf=%.4f bytes=%d toneF=%.6f toneish=%.6f maxPR=%.6f goP=%d libP=%d goOn=%v libOn=%v",
					iter, channels, frameSize, enabled, complexity, prevPeriod, prevGain, prevTapset, prefilterTapset,
					tfEstimate, nbAvailableBytes, toneFreq, toneishness, maxPitchRatio,
					goRes.pitch, libRes.pitch, goRes.on, libRes.on)
				loggedPitch++
			}
		}
		if goRes.qg != libRes.qg {
			qgMismatch++
		}
		gd := math.Abs(goRes.gain - libRes.gain)
		if gd > 1e-5 {
			gainMismatch++
			if gd > maxGainDiff {
				maxGainDiff = gd
			}
		}
	}

	t.Logf("iters=%d onMismatch=%d pitchMismatch=%d qgMismatch=%d gainMismatch=%d maxPitchDiff=%d maxGainDiff=%.6f",
		iters, onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)
	if onMismatch != 0 || pitchMismatch != 0 || qgMismatch != 0 || gainMismatch != 0 {
		t.Fatalf("runPrefilter parity mismatch: on=%d pitch=%d qg=%d gain=%d maxPitchDiff=%d maxGainDiff=%.6f",
			onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)
	}
}
