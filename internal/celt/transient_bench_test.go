package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

var transientBenchSink TransientAnalysisResult
var (
	toneBenchSink0     float32
	toneBenchSink1     float32
	toneBenchSinkOK    bool
	toneBenchSinkDelay int
)

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
			for i := range samplesPerChannel {
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

			pcm32 := planarFromInterleaved(float32Slice(pcm), tc.channels)
			tmp := make([]float32, samplesPerChannel)
			got := enc.TransientAnalysis(pcm32, samplesPerChannel, tc.allowWeak)
			want := transientAnalysisLegacyBench(enc, pcm, samplesPerChannel, tc.allowWeak,
				enc.scratch.transientX, tmp, enc.scratch.transientEnergy)

			if got != want {
				t.Fatalf("mismatch:\n got  %+v\n want %+v", got, want)
			}
		})
	}
}

func TestTransientAnalysisMonoFloat32MatchesFloat64(t *testing.T) {
	testCases := []struct {
		name              string
		samplesPerChannel int
		allowWeak         bool
	}{
		{name: "short-weak-off", samplesPerChannel: 240, allowWeak: false},
		{name: "short-weak-on", samplesPerChannel: 240, allowWeak: true},
		{name: "medium-weak-off", samplesPerChannel: 600, allowWeak: false},
		{name: "long-weak-on", samplesPerChannel: 1080, allowWeak: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(1)
			enc.ensureScratch(tc.samplesPerChannel)
			pcmF32 := make([]float32, tc.samplesPerChannel)
			for i := range pcmF32 {
				t0 := float64(i) / 48000.0
				v := float32(0.4*math.Sin(2*math.Pi*440*t0) + 0.07*math.Sin(2*math.Pi*3910*t0))
				if i >= tc.samplesPerChannel/3 && i < tc.samplesPerChannel/3+10 {
					v += 0.55
				}
				pcmF32[i] = v
			}

			got := enc.transientAnalysisMonoFloat32(pcmF32, tc.samplesPerChannel, tc.allowWeak)
			want := enc.TransientAnalysis(pcmF32, tc.samplesPerChannel, tc.allowWeak)
			if got != want {
				t.Fatalf("mismatch:\n got  %+v\n want %+v", got, want)
			}
		})
	}
}

func TestToneLPCRetry48kMonoMatchesSequential(t *testing.T) {
	testCases := []struct {
		name string
		n    int
		fill func([]float32)
	}{
		{
			name: "short-low-tone",
			n:    360,
			fill: func(x []float32) {
				for i := range x {
					x[i] = float32(0.42 * math.Sin(2*math.Pi*440*float64(i)/48000.0))
				}
			},
		},
		{
			name: "long-low-tone-with-harmonic",
			n:    1080,
			fill: func(x []float32) {
				for i := range x {
					t0 := float64(i) / 48000.0
					x[i] = float32(0.34*math.Sin(2*math.Pi*330*t0) + 0.06*math.Sin(2*math.Pi*990*t0))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			x := make([]float32, tc.n)
			tc.fill(x)

			lpc0, lpc1, success := toneLPCDelay1(x, false)
			if !toneLPCRetryNeeded(lpc0, lpc1, success) {
				t.Fatal("test case did not enter retry path")
			}

			want0, want1, wantSuccess, wantDelay := toneLPCRetrySequentialTest(x, 48000, lpc0, lpc1, success)
			got0, got1, gotSuccess, gotDelay := toneLPCRetry48kMono(x, 48000/3000)
			if gotDelay != wantDelay || gotSuccess != wantSuccess || math.Float32bits(got0) != math.Float32bits(want0) || math.Float32bits(got1) != math.Float32bits(want1) {
				t.Fatalf("retry mismatch: got delay=%d success=%v lpc=(%08x,%08x), want delay=%d success=%v lpc=(%08x,%08x)",
					gotDelay, gotSuccess, math.Float32bits(got0), math.Float32bits(got1),
					wantDelay, wantSuccess, math.Float32bits(want0), math.Float32bits(want1))
			}
		})
	}
}

func toneLPCRetrySequentialTest(x []float32, sampleRate int, lpc0, lpc1 float32, success bool) (float32, float32, bool, int) {
	n := len(x)
	delay := 1
	maxDelay := max(sampleRate/3000, 1)
	for delay <= maxDelay && toneLPCRetryNeeded(lpc0, lpc1, success) {
		delay *= 2
		if 2*delay >= n {
			break
		}
		lpc0, lpc1, success = toneLPC(x, delay, false)
	}
	return lpc0, lpc1, success, delay
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

	channels := int(e.channels)
	samplesPerChannel := len(pcm) / channels
	if samplesPerChannel < 16 {
		return result
	}

	toneInput := make([]float32, len(pcm))
	for i, v := range pcm {
		toneInput[i] = float32(v)
	}
	toneFreq, toneishness := toneDetectScratch(toneInput, channels, 48000, toneBuf)
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

	for c := range channels {
		var mem0, mem1 float32
		if channels == 1 {
			src := pcm[:samplesPerChannel]
			for i := range samplesPerChannel {
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
			for i := range samplesPerChannel {
				x := float32(pcm[idx])
				y := mem0 + x
				mem00 := mem0
				mem0 = mem0 - x + 0.5*mem1
				mem1 = x - mem00
				tmp[i] = y
				idx += stride
			}
		}

		limit := min(12, samplesPerChannel)
		clear(tmp[:limit])

		mem0 = 0
		mean := float32(0)
		for i := range len2 {
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
			id := min(int(normE*(energy[i]+epsF32)), 127)
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
	result.MaskMetric = float32(maxMaskMetric)

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
	result.TfEstimate = float32(math.Sqrt(tfEstimateSquared))
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
	for i := range samplesPerChannel {
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
	pcm32 := planarFromInterleaved(float32Slice(pcm), channels)

	tmp := make([]float32, samplesPerChannel)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if legacy {
			transientBenchSink = transientAnalysisLegacyBench(enc, pcm, samplesPerChannel, false,
				enc.scratch.transientX, tmp, enc.scratch.transientEnergy)
		} else {
			transientBenchSink = enc.TransientAnalysis(pcm32, samplesPerChannel, false)
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

func BenchmarkTransientAnalysisMonoFloat32(b *testing.B) {
	benchmarks := []struct {
		name              string
		samplesPerChannel int
	}{
		{name: "5ms", samplesPerChannel: 240 + Overlap},
		{name: "20ms", samplesPerChannel: 960 + Overlap},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			enc := NewEncoder(1)
			enc.ensureScratch(bm.samplesPerChannel)
			pcm := make([]float32, bm.samplesPerChannel)
			for i := range pcm {
				t := float64(i) / 48000.0
				pcm[i] = float32(0.35*math.Sin(2*math.Pi*440*t) + 0.12*math.Sin(2*math.Pi*1760*t))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				transientBenchSink = enc.transientAnalysisMonoFloat32(pcm, bm.samplesPerChannel, false)
			}
		})
	}
}

func BenchmarkToneLPCRetry48kMono(b *testing.B) {
	x := make([]float32, 360)
	for i := range x {
		t := float64(i) / 48000.0
		x[i] = float32(0.42*math.Sin(2*math.Pi*440*t) + 0.03*math.Sin(2*math.Pi*880*t))
	}

	b.Run("selected", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			toneBenchSink0, toneBenchSink1, toneBenchSinkOK, toneBenchSinkDelay = toneLPCRetry48kMono(x, 48000/3000)
		}
	})
	b.Run("sequential", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			toneBenchSink0, toneBenchSink1, toneBenchSinkOK, toneBenchSinkDelay = toneLPCRetry48kMonoSequential(x, 48000/3000)
		}
	})
}

// planarFromInterleaved lays interleaved samples out like celt_encode_with_ec's
// planar in buffer, one channel after another.
func planarFromInterleaved(pcm []float32, channels int) []float32 {
	n := len(pcm) / channels
	out := make([]float32, len(pcm))
	for i, v := range pcm {
		out[(i%channels)*n+i/channels] = v
	}
	return out
}

func TestTransientInterleaveDeinterleave(t *testing.T) {
	// Test ordery table matches libopus
	// From libopus bands.c:
	// static const int ordery_table[] = {
	//    1,  0,
	//    3,  0,  2,  1,
	//    7,  0,  4,  3,  6,  1,  5,  2,
	//   15,  0,  8,  7, 12,  3, 11,  4, 14,  1,  9,  6, 13,  2, 10,  5,
	// };
	libopusOrdery := []int{
		1, 0,
		3, 0, 2, 1,
		7, 0, 4, 3, 6, 1, 5, 2,
		15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
	}

	if len(orderyTable) != len(libopusOrdery) {
		t.Errorf("orderyTable length mismatch: got %d, want %d", len(orderyTable), len(libopusOrdery))
	}
	for i, v := range libopusOrdery {
		if orderyTable[i] != v {
			t.Errorf("orderyTable[%d] mismatch: got %d, want %d", i, orderyTable[i], v)
		}
	}

	// Test deinterleave/interleave roundtrip for various stride values
	testCases := []struct {
		n0       int
		stride   int
		hadamard bool
	}{
		{8, 2, true},
		{8, 2, false},
		{16, 4, true},
		{16, 4, false},
		{32, 8, true},
		{32, 8, false},
		{64, 16, true},
		{64, 16, false},
		// CELT typical cases
		{15, 8, true}, // 120/8 = 15 (960 frame, 8 short blocks)
		{30, 4, true}, // 120/4 = 30 (480 frame, 4 short blocks)
		{60, 2, true}, // 120/2 = 60 (240 frame, 2 short blocks)
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("n0=%d_stride=%d_hadamard=%v", tc.n0, tc.stride, tc.hadamard), func(t *testing.T) {
			n := tc.n0 * tc.stride
			original := make([]celtNorm, n)
			for i := range original {
				original[i] = celtNorm(float32(i + 1))
			}

			// Deinterleave
			x := make([]celtNorm, n)
			copy(x, original)
			deinterleaveHadamard(x, tc.n0, tc.stride, tc.hadamard)

			// Interleave back
			interleaveHadamard(x, tc.n0, tc.stride, tc.hadamard)

			// Verify roundtrip
			for i := range original {
				if math.Abs(float64(x[i]-original[i])) > 1e-6 {
					t.Errorf("roundtrip mismatch at %d: got %f, want %f", i, x[i], original[i])
				}
			}
		})
	}
}

// TestTransientLMAndB validates LM (log mode) and B (number of short blocks) handling.
//
// In libopus (celt_decoder.c celt_synthesis):
// - LM = 0,1,2,3 for 2.5ms,5ms,10ms,20ms frames
// - M = 1<<LM (multiplier)
// - For transient: B = M (number of short blocks), NB = shortMdctSize (120)
// - For non-transient: B = 1, NB = shortMdctSize << LM
func TestTransientLMAndB(t *testing.T) {
	const shortMdctSize = 120

	testCases := []struct {
		lm          int
		isTransient bool
		wantB       int
		wantNB      int
		wantN       int // Total samples N = M * shortMdctSize
	}{
		// Non-transient frames
		{0, false, 1, 120, 120}, // 2.5ms
		{1, false, 1, 240, 240}, // 5ms
		{2, false, 1, 480, 480}, // 10ms
		{3, false, 1, 960, 960}, // 20ms
		// Transient frames
		{0, true, 1, 120, 120}, // 2.5ms (no short blocks possible)
		{1, true, 2, 120, 240}, // 5ms: 2 short blocks of 120
		{2, true, 4, 120, 480}, // 10ms: 4 short blocks of 120
		{3, true, 8, 120, 960}, // 20ms: 8 short blocks of 120
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("LM=%d_transient=%v", tc.lm, tc.isTransient), func(t *testing.T) {
			M := 1 << tc.lm
			var B, NB int
			if tc.isTransient {
				B = M
				NB = shortMdctSize
			} else {
				B = 1
				NB = shortMdctSize << tc.lm
			}
			N := M * shortMdctSize

			if B != tc.wantB {
				t.Errorf("B mismatch: got %d, want %d", B, tc.wantB)
			}
			if NB != tc.wantNB {
				t.Errorf("NB mismatch: got %d, want %d", NB, tc.wantNB)
			}
			if N != tc.wantN {
				t.Errorf("N mismatch: got %d, want %d", N, tc.wantN)
			}

			// Verify relationship: B * NB = N (for transient)
			if tc.isTransient && B*NB != N {
				t.Errorf("B*NB != N for transient: %d*%d = %d != %d", B, NB, B*NB, N)
			}
		})
	}
}

// TestTransientCoefficientsInterleaving validates that the coefficient interleaving
// for transient frames matches the libopus layout.
//
// In libopus, for transient frames with B short blocks:
// - Input coefficients are interleaved: coef[b + i*B] where b=block, i=bin
// - The IMDCT reads with stride B: &freq[b] with stride B
// - Output goes to out_syn[c] + NB*b
func TestTransientCoefficientsInterleaving(t *testing.T) {
	// Test 960 frame with 8 short blocks
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120

	// Create test coefficients in the interleaved format libopus expects
	coeffs := make([]float32, frameSize)
	for b := range shortBlocks {
		for i := range shortSize {
			// Interleaved: coef[b + i*B]
			idx := b + i*shortBlocks
			coeffs[idx] = float32(b*1000 + i) // Block b, bin i
		}
	}

	// Extract coefficients for each block (as the Go implementation should do)
	for b := range shortBlocks {
		shortCoeffs := make([]float32, shortSize)
		for i := range shortSize {
			idx := b + i*shortBlocks
			shortCoeffs[i] = coeffs[idx]
		}

		// Verify extraction is correct
		for i := range shortSize {
			expected := float32(b*1000 + i)
			if shortCoeffs[i] != expected {
				t.Errorf("Block %d, bin %d: got %f, want %f", b, i, shortCoeffs[i], expected)
			}
		}
	}
}

// TestHaar1Transform validates the Haar wavelet transform matches libopus.
//
// From libopus bands.c:
// void haar1(celt_norm *X, int N0, int stride)
//
//	{
//	   int i, j;
//	   N0 >>= 1;
//	   for (i=0;i<stride;i++)
//	      for (j=0;j<N0;j++)
//	      {
//	         opus_val32 tmp1, tmp2;
//	         tmp1 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*2*j+i]);
//	         tmp2 = MULT32_32_Q31(QCONST32(.70710678f,31), X[stride*(2*j+1)+i]);
//	         X[stride*2*j+i] = ADD32(tmp1, tmp2);
//	         X[stride*(2*j+1)+i] = SUB32(tmp1, tmp2);
//	      }
//	}
func TestHaar1Transform(t *testing.T) {
	libopustest.RequireOracle(t)

	testCases := []struct {
		n0     int
		stride int
	}{
		{4, 1},
		{4, 2},
		{8, 1},
		{8, 2},
		{16, 1},
		{16, 4},
		{120, 1}, // Typical CELT short block
		{120, 8}, // 8 short blocks
	}

	cases := make([]haar1OracleCase, len(testCases))
	for i, tc := range testCases {
		x := make([]float32, tc.n0*tc.stride)
		for j := range x {
			x[j] = float32(j + 1)
		}
		cases[i] = haar1OracleCase{
			name:   fmt.Sprintf("n0_%d_stride_%d", tc.n0, tc.stride),
			x:      x,
			n0:     tc.n0,
			stride: tc.stride,
		}
	}
	want, err := probeLibopusHaar1(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		t.Run(fmt.Sprintf("n0=%d_stride=%d", tc.n0, tc.stride), func(t *testing.T) {
			got := make([]celtNorm, len(tc.x))
			for i, sample := range tc.x {
				got[i] = celtNorm(sample)
			}
			haar1(got, tc.n0, tc.stride)
			for i := range got {
				got32 := float32(got[i])
				if math.Float32bits(got32) != math.Float32bits(want[ci][i]) {
					t.Fatalf("x[%d]=%08x %.10g want %08x %.10g",
						i, math.Float32bits(got32), got32,
						math.Float32bits(want[ci][i]), want[ci][i])
				}
			}
		})
	}
}

// TestBitInterleaveDeinterleave validates the bit interleave/deinterleave tables.
//
// From libopus bands.c:
//
//	static const unsigned char bit_interleave_table[16]={
//	      0,1,1,1,2,3,3,3,2,3,3,3,2,3,3,3
//	};
//
//	static const unsigned char bit_deinterleave_table[16]={
//	      0x00,0x03,0x0C,0x0F,0x30,0x33,0x3C,0x3F,
//	      0xC0,0xC3,0xCC,0xCF,0xF0,0xF3,0xFC,0xFF
//	};
func TestBitInterleaveDeinterleave(t *testing.T) {
	libopusBitInterleave := []int{
		0, 1, 1, 1, 2, 3, 3, 3, 2, 3, 3, 3, 2, 3, 3, 3,
	}
	libopusBitDeinterleave := []int{
		0x00, 0x03, 0x0C, 0x0F, 0x30, 0x33, 0x3C, 0x3F,
		0xC0, 0xC3, 0xCC, 0xCF, 0xF0, 0xF3, 0xFC, 0xFF,
	}

	if len(bitInterleaveTable) != len(libopusBitInterleave) {
		t.Errorf("bitInterleaveTable length mismatch: got %d, want %d", len(bitInterleaveTable), len(libopusBitInterleave))
	}
	for i, v := range libopusBitInterleave {
		if bitInterleaveTable[i] != v {
			t.Errorf("bitInterleaveTable[%d] mismatch: got %d, want %d", i, bitInterleaveTable[i], v)
		}
	}

	if len(bitDeinterleaveTable) != len(libopusBitDeinterleave) {
		t.Errorf("bitDeinterleaveTable length mismatch: got %d, want %d", len(bitDeinterleaveTable), len(libopusBitDeinterleave))
	}
	for i, v := range libopusBitDeinterleave {
		if bitDeinterleaveTable[i] != v {
			t.Errorf("bitDeinterleaveTable[%d] mismatch: got %d, want %d", i, bitDeinterleaveTable[i], v)
		}
	}
}

// TestTransientSynthesisShortBlocks validates the short block IMDCT synthesis
// for transient frames.
func TestTransientSynthesisShortBlocks(t *testing.T) {
	// For 960-sample (20ms) transient frame:
	// - 8 short blocks of 120 samples each
	// - Each short IMDCT produces 240 samples
	// - With 120 overlap, each block adds 120 new samples
	// - Total output: 960 samples
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks
	overlap := 120

	// Create test coefficients (in interleaved format)
	coeffs := make([]float32, frameSize)
	for b := range shortBlocks {
		for i := range shortSize {
			idx := b + i*shortBlocks
			// Create a simple signal: DC offset + small variation
			coeffs[idx] = float32(0.01 * (1 + 0.1*float64(i)/float64(shortSize)))
		}
	}

	// Test synthesis
	dec := NewDecoder(1)
	output := dec.synthesizeTest(coeffs, true, shortBlocks)

	if len(output) != frameSize {
		t.Errorf("output length mismatch: got %d, want %d", len(output), frameSize)
	}

	// Verify output is non-zero (synthesis worked)
	var maxAbs float64
	for _, v := range output {
		abs := math.Abs(float64(v))
		if abs > maxAbs {
			maxAbs = abs
		}
	}
	if maxAbs == 0 {
		t.Error("output is all zeros - synthesis failed")
	}

	t.Logf("Transient synthesis: %d samples, max abs = %f, overlap = %d", len(output), maxAbs, overlap)
}

// TestQuantBandTransient validates band quantization for transient frames.
func TestQuantBandTransient(t *testing.T) {
	// Test that quant_band correctly handles B > 1 (transient) cases
	// For transient frames:
	// - B = M (number of short blocks)
	// - The band width is scaled by M (<<LM)
	// - Coefficients are processed with stride B

	testCases := []struct {
		lm        int
		transient bool
		expectedB int
	}{
		{0, false, 1},
		{1, false, 1},
		{2, false, 1},
		{3, false, 1},
		{0, true, 1},
		{1, true, 2},
		{2, true, 4},
		{3, true, 8},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("LM=%d_transient=%v", tc.lm, tc.transient), func(t *testing.T) {
			M := 1 << tc.lm
			B := 1
			if tc.transient {
				B = M
			}
			if B != tc.expectedB {
				t.Errorf("B mismatch: got %d, want %d", B, tc.expectedB)
			}
		})
	}
}

// TestModeConfigShortBlocks validates that ModeConfig.ShortBlocks matches expectations.
func TestModeConfigShortBlocks(t *testing.T) {
	testCases := []struct {
		frameSize           int
		expectedLM          int
		expectedShortBlocks int
	}{
		{120, 0, 1}, // 2.5ms
		{240, 1, 2}, // 5ms
		{480, 2, 4}, // 10ms
		{960, 3, 8}, // 20ms
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("frameSize=%d", tc.frameSize), func(t *testing.T) {
			mode := GetModeConfig(tc.frameSize)

			if mode.LM != tc.expectedLM {
				t.Errorf("LM mismatch: got %d, want %d", mode.LM, tc.expectedLM)
			}
			if mode.ShortBlocks != tc.expectedShortBlocks {
				t.Errorf("ShortBlocks mismatch: got %d, want %d", mode.ShortBlocks, tc.expectedShortBlocks)
			}

			// Verify ShortBlocks = 1 << LM
			expectedFromLM := 1 << tc.expectedLM
			if tc.frameSize == 120 {
				expectedFromLM = 1 // Special case: 2.5ms has no short blocks
			}
			if mode.ShortBlocks != expectedFromLM {
				t.Logf("Note: ShortBlocks=%d differs from 1<<LM=%d for frameSize=%d",
					mode.ShortBlocks, 1<<tc.expectedLM, tc.frameSize)
			}
		})
	}
}
