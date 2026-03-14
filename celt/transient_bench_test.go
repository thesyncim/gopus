package celt

import (
	"math"
	"testing"
)

var transientBenchSink TransientAnalysisResult

func TestTransientAnalysisMatchesLegacy(t *testing.T) {
	testCases := []struct {
		name      string
		channels  int
		frameSize int
		allowWeak bool
	}{
		{name: "mono-weak-off", channels: 1, frameSize: 960, allowWeak: false},
		{name: "mono-weak-on", channels: 1, frameSize: 960, allowWeak: true},
		{name: "stereo-weak-off", channels: 2, frameSize: 960, allowWeak: false},
		{name: "stereo-weak-on", channels: 2, frameSize: 960, allowWeak: true},
		{name: "stereo-short", channels: 2, frameSize: 480, allowWeak: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(tc.channels)
			enc.ensureScratch(tc.frameSize)
			samplesPerChannel := tc.frameSize + Overlap
			pcm := make([]float64, samplesPerChannel*tc.channels)
			for i := 0; i < samplesPerChannel; i++ {
				t0 := float64(i) / 48000.0
				left := 0.35*math.Sin(2*math.Pi*440*t0) + 0.12*math.Sin(2*math.Pi*1760*t0)
				right := 0.28*math.Sin(2*math.Pi*523.25*t0) + 0.08*math.Sin(2*math.Pi*1046.5*t0)
				if i >= 240 && i < 252 {
					left += 0.6
				}
				if i >= 300 && i < 312 {
					right += 0.45
				}
				if tc.channels == 1 {
					pcm[i] = left
				} else {
					pcm[2*i] = left
					pcm[2*i+1] = right
				}
			}

			tmp := make([]float32, samplesPerChannel)
			got := enc.TransientAnalysis(pcm, samplesPerChannel, tc.allowWeak)
			want := transientAnalysisLegacyBench(enc, pcm, samplesPerChannel, tc.allowWeak,
				enc.scratch.transientX, tmp, enc.scratch.transientEnergy)

			if got != want {
				t.Fatalf("mismatch:\n got  %+v\n want %+v", got, want)
			}
		})
	}
}

func transientAnalysisLegacyBench(e *Encoder, pcm []float64, frameSize int, allowWeakTransients bool,
	toneBuf []float32, tmpBuf []float32, energyBuf []float32) TransientAnalysisResult {
	result := TransientAnalysisResult{
		TfEstimate:  0.0,
		TfChannel:   0,
		ToneFreq:    -1,
		Toneishness: 0,
	}

	if len(pcm) == 0 || frameSize <= 0 {
		return result
	}

	channels := e.channels
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}

	toneFreq, toneishness := toneDetectScratch(pcm, channels, 48000, toneBuf)
	result.ToneFreq = toneFreq
	result.Toneishness = toneishness

	forwardDecay := float32(0.0625)
	forwardRetain := float32(1.0) - forwardDecay
	if allowWeakTransients {
		forwardDecay = 0.03125
		forwardRetain = float32(1.0) - forwardDecay
	}

	var maxMaskMetric int
	tfChannel := 0

	tmp := tmpBuf[:samplesPerChannel]
	len2 := samplesPerChannel / 2
	energy := energyBuf[:len2]

	for c := 0; c < channels; c++ {
		var mem0, mem1 float32
		if channels == 1 {
			src := pcm[:samplesPerChannel]
			for i := 0; i < samplesPerChannel; i++ {
				x := float32(src[i])
				y := mem0 + x
				mem00 := mem0
				mem0 = mem0 - x + 0.5*mem1
				mem1 = x - mem00
				tmp[i] = y
			}
		} else {
			stride := channels
			idx := c
			for i := 0; i < samplesPerChannel; i++ {
				x := float32(pcm[idx])
				y := mem0 + x
				mem00 := mem0
				mem0 = mem0 - x + 0.5*mem1
				mem1 = x - mem00
				tmp[i] = y
				idx += stride
			}
		}

		limit := 12
		if limit > samplesPerChannel {
			limit = samplesPerChannel
		}
		clear(tmp[:limit])

		mem0 = 0
		mean := float32(0)
		for i := 0; i < len2; i++ {
			j := i << 1
			t0 := tmp[j]
			t1 := tmp[j+1]
			pair := t0*t0 + t1*t1
			mean += pair
			mem0 = pair + forwardRetain*mem0
			energy[i] = forwardDecay * mem0
		}

		var maxE float32
		mem0 = 0
		for i := len2 - 1; i >= 0; i-- {
			mem0 = energy[i] + 0.875*mem0
			ei := float32(0.125) * mem0
			energy[i] = ei
			if ei > maxE {
				maxE = ei
			}
		}

		meanGeom := math.Sqrt(float64(mean * maxE * float32(0.5*float64(len2))))
		const epsilon = 1e-15
		normE := float32(float64(64*len2) / (meanGeom + epsilon))

		const epsF32 = float32(1e-15)
		var unmask int
		for i := 12; i < len2-5; i += 4 {
			id := int(normE * (energy[i] + epsF32))
			if id > 127 {
				id = 127
			}
			unmask += transientInvTable[id]
		}

		maskMetric := 0
		if len2 > 17 {
			maskMetric = 64 * unmask * 4 / (6 * (len2 - 17))
		}
		if maskMetric > maxMaskMetric {
			tfChannel = c
			maxMaskMetric = maskMetric
		}
	}

	result.TfChannel = tfChannel
	result.IsTransient = maxMaskMetric > 200
	if result.Toneishness > 0.98 && result.ToneFreq >= 0 && result.ToneFreq < 0.026 {
		result.IsTransient = false
		maxMaskMetric = 0
	}
	if allowWeakTransients && result.IsTransient && maxMaskMetric < 600 {
		result.IsTransient = false
		result.WeakTransient = true
	}
	result.MaskMetric = float64(maxMaskMetric)

	tfMax := math.Sqrt(27*float64(maxMaskMetric)) - 42
	if tfMax < 0 {
		tfMax = 0
	}
	if tfMax > 163 {
		tfMax = 163
	}
	tfEstimateSquared := 0.0069*tfMax - 0.139
	if tfEstimateSquared < 0 {
		tfEstimateSquared = 0
	}
	result.TfEstimate = math.Sqrt(tfEstimateSquared)
	if result.TfEstimate > 1.0 {
		result.TfEstimate = 1.0
	}

	return result
}

func benchmarkTransientAnalysis(b *testing.B, legacy bool) {
	benchmarkTransientAnalysisChannels(b, 1, legacy)
}

func benchmarkTransientAnalysisChannels(b *testing.B, channels int, legacy bool) {
	const frameSize = 960
	enc := NewEncoder(channels)
	enc.ensureScratch(frameSize)
	samplesPerChannel := frameSize + Overlap

	pcm := make([]float64, samplesPerChannel*channels)
	for i := 0; i < samplesPerChannel; i++ {
		t := float64(i) / 48000.0
		left := 0.35*math.Sin(2*math.Pi*440*t) + 0.12*math.Sin(2*math.Pi*1760*t)
		right := 0.28*math.Sin(2*math.Pi*523.25*t) + 0.08*math.Sin(2*math.Pi*1046.5*t)
		if i >= 240 && i < 252 {
			left += 0.6
		}
		if i >= 300 && i < 312 {
			right += 0.45
		}
		if channels == 1 {
			pcm[i] = left
		} else {
			pcm[2*i] = left
			pcm[2*i+1] = right
		}
	}

	tmp := make([]float32, samplesPerChannel)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if legacy {
			transientBenchSink = transientAnalysisLegacyBench(enc, pcm, samplesPerChannel, false,
				enc.scratch.transientX, tmp, enc.scratch.transientEnergy)
		} else {
			transientBenchSink = enc.TransientAnalysis(pcm, samplesPerChannel, false)
		}
	}
}

func BenchmarkTransientAnalysisCurrent(b *testing.B) {
	benchmarkTransientAnalysis(b, false)
}

func BenchmarkTransientAnalysisLegacy(b *testing.B) {
	benchmarkTransientAnalysis(b, true)
}

func BenchmarkTransientAnalysisCurrentStereo(b *testing.B) {
	benchmarkTransientAnalysisChannels(b, 2, false)
}

func BenchmarkTransientAnalysisLegacyStereo(b *testing.B) {
	benchmarkTransientAnalysisChannels(b, 2, true)
}
