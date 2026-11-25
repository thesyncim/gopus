package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

const (
	libopusCELTIMDCTModeLong         = uint32(0)
	libopusCELTIMDCTModeTransient    = uint32(1)
	libopusCELTIMDCTModeFFT          = uint32(2)
	libopusCELTIMDCTModeForward      = uint32(3)
	libopusCELTIMDCTModeForwardShort = uint32(4)
)

var libopusCELTIMDCTHelper libopustest.HelperCache

func buildLibopusCELTIMDCTHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT IMDCT",
		OutputBase:  "gopus_libopus_celt_imdct",
		SourceFile:  "libopus_celt_imdct_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		RefSources:  []string{"celt/mdct.c", "celt/kiss_fft.c", "celt/modes.c"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusCELTIMDCT(t *testing.T, mode uint32, frameSize, overlap, shortBlocks int, spectrum, prevOverlap []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCII", mode, uint32(frameSize), uint32(overlap), uint32(shortBlocks))
	for i := range overlap {
		payload.Float32(prevOverlap[i])
	}
	for i := range frameSize {
		payload.Float32(spectrum[i])
	}

	binPath, err := libopusCELTIMDCTHelper.Path(buildLibopusCELTIMDCTHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT IMDCT", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT IMDCT", "GCIO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT IMDCT", err)
	}
	if gotMode := reader.U32(); gotMode != mode {
		t.Fatalf("helper mode=%d want %d", gotMode, mode)
	}
	count := int(reader.U32())
	out := make([]float32, count)
	reader.ExpectRemaining(count * 4)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func imdctOverlapWithPrevScratchF32LegacyBufferCopy(out []float32, spectrum []float32, prevOverlap []float32, overlap int, scratch *imdctScratchF32) {
	n2 := len(spectrum)
	if n2 == 0 {
		return
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	needed := n2 + overlap
	start := overlap / 2
	if len(out) < needed {
		return
	}

	trig := getMDCTTrigF32(n)

	var fftIn []float32
	var fftTmp []float32
	var buf []float32
	var outF32 []float32
	if scratch == nil {
		fftIn = make([]float32, 2*n4)
		fftTmp = make([]float32, 2*n4)
		buf = make([]float32, n2)
		outF32 = make([]float32, needed)
	} else {
		fftIn = ensureFloat32Slice(&scratch.fftIn, 2*n4)
		fftTmp = ensureFloat32Slice(&scratch.fftTmp, 2*n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
		outF32 = ensureFloat32Slice(&scratch.out, needed)
	}

	if start+n2 < needed {
		clear(outF32[start+n2 : needed])
	}

	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := min(len(prevOverlap), overlap)
		for i := range copyLen {
			outF32[i] = float32(prevOverlap[i])
		}
		if copyLen < overlap {
			clear(outF32[copyLen:overlap])
		}
	} else if overlap > 0 {
		clear(outF32[:overlap])
	}

	imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
	fftOut := kissFFT32InterleavedToScratch(fftIn, fftTmp, nil)
	copy(buf, fftOut)
	imdctPostRotateF32InterleavedLibopusPolicy(buf, trig, n2, n4)
	copy(outF32[start:start+n2], buf)

	if overlap > 0 {
		windowF32 := GetWindowBufferF32(overlap)
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		limit := overlap / 2
		i := 0
		for ; i+1 < limit; i += 2 {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = mdctMulSubMix(x2, x1, windowF32[wp2], windowF32[wp1])
			outF32[xp1] = mdctMulAddMix(x2, x1, windowF32[wp1], windowF32[wp2])
			yp1++
			xp1--
			wp1++
			wp2--

			x1 = outF32[xp1]
			x2 = outF32[yp1]
			outF32[yp1] = mdctMulSubMix(x2, x1, windowF32[wp2], windowF32[wp1])
			outF32[xp1] = mdctMulAddMix(x2, x1, windowF32[wp1], windowF32[wp2])
			yp1++
			xp1--
			wp1++
			wp2--
		}
		for ; i < limit; i++ {
			x1 := outF32[xp1]
			x2 := outF32[yp1]
			outF32[yp1] = mdctMulSubMix(x2, x1, windowF32[wp2], windowF32[wp1])
			outF32[xp1] = mdctMulAddMix(x2, x1, windowF32[wp1], windowF32[wp2])
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	if needed > 0 {
		out = out[:needed:needed]
		outF32 = outF32[:needed:needed]
	}
	copy(out[:needed], outF32[:needed])
}

func imdctPostRotateF32InterleavedLibopusPolicy(buf []float32, trig []float32, n2, n4 int) {
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}

	_ = buf[n2-1]
	_ = trig[n2-1]

	yp0 := 0
	yp1 := n2 - 2
	for i := range limit {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := mdctMulAddMix(re, im, t0, t1)
		yi := mdctMulSubMix(re, im, t1, t0)

		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = mdctMulAddMix(re2, im2, t0, t1)
		yi = mdctMulSubMix(re2, im2, t1, t0)
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}

func TestIMDCTOverlapWithPrevScratchF32MatchesLegacyBufferCopy(t *testing.T) {
	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 120, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 960, overlap: 120},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 4; seed++ {
			t.Run(fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed), func(t *testing.T) {
				spectrum := make([]float32, tc.frameSize)
				prevOverlap := make([]float32, tc.overlap)
				for i := range spectrum {
					sine := math.Sin(float64(i+seed*11) * 0.063)
					cosine := math.Cos(float64((i+1)*(seed+5)) * 0.017)
					step := float64((i*13+seed*29)%23-11) / 28.0
					spectrum[i] = float32(0.6*sine + 0.25*cosine + step)
				}
				for i := range prevOverlap {
					sine := math.Sin(float64(i+seed*3) * 0.041)
					step := float64((i*7+seed*19)%17-8) / 20.0
					prevOverlap[i] = float32(0.7*sine + step)
				}

				got := make([]float32, tc.frameSize+tc.overlap)
				want := make([]float32, tc.frameSize+tc.overlap)
				imdctOverlapWithPrevScratchF32(got, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})
				imdctOverlapWithPrevScratchF32LegacyBufferCopy(want, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})

				for i := range want {
					if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
						t.Fatalf("sample %d mismatch: got %.9g want %.9g", i, got[i], want[i])
					}
				}
			})
		}
	}
}

func TestIMDCTOverlapWithPrevScratchF32MatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 120, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 960, overlap: 120},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 3; seed++ {
			t.Run(fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed), func(t *testing.T) {
				spectrum := make([]float64, tc.frameSize)
				spectrumF32 := make([]float32, tc.frameSize)
				prevOverlap := make([]float64, tc.overlap)
				prevOverlapF32 := make([]float32, tc.overlap)
				fillIMDCTOracleInput(spectrum, spectrumF32, prevOverlap, prevOverlapF32, seed)

				got := imdctOverlapWithPrevScratchF32Output32(spectrumF32, prevOverlapF32, tc.overlap, &imdctScratchF32{})
				want := probeLibopusCELTIMDCT(t, libopusCELTIMDCTModeLong, tc.frameSize, tc.overlap, 0, spectrumF32, prevOverlapF32)
				assertFloat32Bits(t, "imdct", got, want)
			})
		}
	}
}

func TestIMDCTTransientInPlaceScratchF32MatchesLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	testCases := []struct {
		frameSize   int
		overlap     int
		shortBlocks int
	}{
		{frameSize: 240, overlap: 120, shortBlocks: 2},
		{frameSize: 480, overlap: 120, shortBlocks: 4},
		{frameSize: 960, overlap: 120, shortBlocks: 8},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 3; seed++ {
			t.Run(fmt.Sprintf("frame=%d/blocks=%d/seed=%d", tc.frameSize, tc.shortBlocks, seed), func(t *testing.T) {
				spectrum := make([]float64, tc.frameSize)
				spectrumF32 := make([]float32, tc.frameSize)
				prevOverlap := make([]float64, tc.overlap)
				prevOverlapF32 := make([]float32, tc.overlap)
				fillIMDCTOracleInput(spectrum, spectrumF32, prevOverlap, prevOverlapF32, seed+9)

				out := make([]float32, tc.frameSize+tc.overlap)
				copy(out[:tc.overlap], prevOverlapF32)
				shortSize := tc.frameSize / tc.shortBlocks
				shortCoeffs := make([]float32, shortSize)
				var scratch imdctScratchF32
				for b := 0; b < tc.shortBlocks; b++ {
					idx := b
					for i := range shortSize {
						shortCoeffs[i] = spectrumF32[idx]
						idx += tc.shortBlocks
					}
					imdctInPlaceScratchF32Spectrum(shortCoeffs, out, b*shortSize, tc.overlap, &scratch)
				}

				got := make([]float32, len(out))
				copy(got, out)
				want := probeLibopusCELTIMDCT(t, libopusCELTIMDCTModeTransient, tc.frameSize, tc.overlap, tc.shortBlocks, spectrumF32, prevOverlapF32)
				assertFloat32Bits(t, "transient imdct", got, want)
			})
		}
	}
}

func TestMDCTForwardOverlapF32MatchesLibopusC(t *testing.T) {
	if mdctQEXTScalePlacement {
		// Under gopus_qext the forward MDCT folds the 1/nfft FFT scale into the
		// post-rotation twiddles (ENABLE_QEXT clt_mdct_forward()), so it does not
		// match this non-QEXT oracle. The QEXT forward MDCT is covered by
		// TestHD96kMDCTMatchesLibopusQEXT against the QEXT-built libopus.
		t.Skip("forward MDCT uses ENABLE_QEXT scale placement; covered by TestHD96kMDCTMatchesLibopusQEXT")
	}
	libopustest.RequireOracle(t)

	testCases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 120, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 960, overlap: 120},
	}

	for _, tc := range testCases {
		for seed := 1; seed <= 3; seed++ {
			t.Run(fmt.Sprintf("frame=%d/seed=%d", tc.frameSize, seed), func(t *testing.T) {
				input := make([]float64, tc.frameSize+tc.overlap)
				inputF32 := make([]float32, tc.frameSize+tc.overlap)
				fillMDCTForwardOracleInput(input, inputF32, seed)

				coeffs := make([]float32, tc.frameSize)
				mdctForwardOverlapF32Scratch(inputF32, tc.overlap, coeffs, nil, nil, nil, nil, nil)
				got := make([]float32, len(coeffs))
				copy(got, coeffs)

				want := probeLibopusCELTMDCTForward(t, tc.frameSize, tc.overlap, inputF32)
				assertFloat32Bits(t, "forward mdct", got, want)
			})
		}
	}
}

// TestMDCTForwardShortBlocksMatchesLibopusC pins the transient (short-block)
// forward MDCT to compute_mdcts(): one clt_mdct_forward() per block at shift
// maxLM, interleaved with stride B, at unit and CELT signal scale.
func TestMDCTForwardShortBlocksMatchesLibopusC(t *testing.T) {
	if mdctQEXTScalePlacement {
		t.Skip("forward MDCT uses ENABLE_QEXT scale placement; covered by TestHD96kMDCTMatchesLibopusQEXT")
	}
	libopustest.RequireOracle(t)

	for _, tc := range []struct{ frameSize, shortBlocks int }{
		{240, 2}, {480, 4}, {960, 8},
	} {
		for _, scale := range []float32{1, 32768} {
			for seed := 1; seed <= 3; seed++ {
				t.Run(fmt.Sprintf("frame=%d/blocks=%d/scale=%g/seed=%d", tc.frameSize, tc.shortBlocks, scale, seed), func(t *testing.T) {
					const overlap = 120
					input := make([]float64, tc.frameSize+overlap)
					inputF32 := make([]float32, tc.frameSize+overlap)
					fillMDCTForwardOracleInput(input, inputF32, seed)
					for i := range inputF32 {
						inputF32[i] *= scale
					}

					var scratch encoderScratch
					got := append([]float32(nil), mdctForwardShortOverlapScratchF32Coeffs(inputF32, overlap, tc.shortBlocks, &scratch)...)

					payload := libopustest.NewOraclePayload("GCII", libopusCELTIMDCTModeForwardShort, uint32(tc.frameSize), uint32(overlap), uint32(tc.shortBlocks))
					for _, v := range inputF32 {
						payload.Float32(v)
					}
					binPath, err := libopusCELTIMDCTHelper.Path(buildLibopusCELTIMDCTHelper)
					if err != nil {
						libopustest.HelperUnavailable(t, "CELT short-block forward MDCT", err)
					}
					reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT short-block forward MDCT", "GCIO")
					if err != nil {
						libopustest.HelperUnavailable(t, "CELT short-block forward MDCT", err)
					}
					if gotMode := reader.U32(); gotMode != libopusCELTIMDCTModeForwardShort {
						t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTIMDCTModeForwardShort)
					}
					count := int(reader.U32())
					reader.ExpectRemaining(count * 4)
					want := make([]float32, count)
					for i := range want {
						want[i] = reader.Float32()
					}
					if err := reader.ExpectConsumed(); err != nil {
						t.Fatal(err)
					}
					assertFloat32Bits(t, "short-block forward mdct", got, want)
				})
			}
		}
	}
}

func TestMDCTForwardOverlapF32CELTSignalScaleMatchesLibopusC(t *testing.T) {
	if mdctQEXTScalePlacement {
		// Under gopus_qext the forward MDCT folds the 1/nfft FFT scale into the
		// post-rotation twiddles (ENABLE_QEXT clt_mdct_forward()), so it does not
		// match this non-QEXT oracle. The QEXT forward MDCT is covered by
		// TestHD96kMDCTMatchesLibopusQEXT against the QEXT-built libopus.
		t.Skip("forward MDCT uses ENABLE_QEXT scale placement; covered by TestHD96kMDCTMatchesLibopusQEXT")
	}
	libopustest.RequireOracle(t)

	const (
		frameSize = 960
		overlap   = 120
	)
	inputF32 := make([]float32, frameSize+overlap)
	for i := range inputF32 {
		sine := math.Sin(float64(i+11) * 0.041)
		chirp := math.Sin(float64(i*i+17*i) * 0.000071)
		step := float64((i*37)%19-9) * 180.0
		inputF32[i] = float32(16000.0*sine + 4500.0*chirp + step)
	}

	coeffs := make([]float32, frameSize)
	mdctForwardOverlapF32Scratch(inputF32, overlap, coeffs, nil, nil, nil, nil, nil)
	got := make([]float32, len(coeffs))
	copy(got, coeffs)

	want := probeLibopusCELTMDCTForward(t, frameSize, overlap, inputF32)
	assertFloat32Bits(t, "forward mdct celt-scale", got, want)
}

func probeLibopusCELTMDCTForward(t *testing.T, frameSize, overlap int, input []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCII", libopusCELTIMDCTModeForward, uint32(frameSize), uint32(overlap), uint32(0))
	for i := 0; i < frameSize+overlap; i++ {
		payload.Float32(input[i])
	}

	binPath, err := libopusCELTIMDCTHelper.Path(buildLibopusCELTIMDCTHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT forward MDCT", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT forward MDCT", "GCIO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT forward MDCT", err)
	}
	if gotMode := reader.U32(); gotMode != libopusCELTIMDCTModeForward {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTIMDCTModeForward)
	}
	count := int(reader.U32())
	out := make([]float32, count)
	reader.ExpectRemaining(count * 4)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fillIMDCTOracleInput(spectrum []float64, spectrumF32 []float32, prevOverlap []float64, prevOverlapF32 []float32, seed int) {
	for i := range spectrum {
		sine := math.Sin(float64(i+seed*11) * 0.063)
		cosine := math.Cos(float64((i+1)*(seed+5)) * 0.017)
		step := float64((i*13+seed*29)%23-11) / 28.0
		v := float32(0.6*sine + 0.25*cosine + step)
		spectrum[i] = float64(v)
		spectrumF32[i] = v
	}
	for i := range prevOverlap {
		sine := math.Sin(float64(i+seed*3) * 0.041)
		step := float64((i*7+seed*19)%17-8) / 20.0
		v := float32(0.7*sine + step)
		prevOverlap[i] = float64(v)
		prevOverlapF32[i] = v
	}
}

func fillMDCTForwardOracleInput(input []float64, inputF32 []float32, seed int) {
	for i := range input {
		sine := math.Sin(float64(i+seed*13) * 0.057)
		cosine := math.Cos(float64((i+3)*(seed+7)) * 0.023)
		step := float64((i*17+seed*31)%29-14) / 31.0
		v := float32(0.52*sine + 0.31*cosine + step)
		input[i] = float64(v)
		inputF32[i] = v
	}
}

// assertFloat32Bits requires every element to match the paired libopus
// reference bit-for-bit. The oracles it guards pair each Go tier with the
// libopus build for the same instruction set, so any difference is a parity
// defect rather than tolerated drift.
func assertFloat32Bits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	assertFloat32Close(t, label, got, want, 0, 0)
}

func assertFloat32Close(t *testing.T, label string, got, want []float32, maxULP uint32, maxAbs float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		ulp := ulpDiffFloat32(got[i], want[i])
		abs := math.Abs(float64(got[i] - want[i]))
		if ulp > maxULP && abs > maxAbs {
			t.Fatalf("%s[%d]=%08x %.9g want %08x %.9g ulp=%d max=%d abs=%g max_abs=%g",
				label, i, math.Float32bits(got[i]), got[i],
				math.Float32bits(want[i]), want[i], ulp, maxULP, abs, maxAbs)
		}
	}
}

func ulpDiffFloat32(a, b float32) uint32 {
	ab := math.Float32bits(a)
	bb := math.Float32bits(b)
	if ab == bb {
		return 0
	}
	if (ab >> 31) != (bb >> 31) {
		return ^uint32(0)
	}
	if ab > bb {
		return ab - bb
	}
	return bb - ab
}

func TestIMDCTPreRotateFFTMatchesSplitPath(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1dc7))
	for _, n := range []int{240, 480, 960, 1920} {
		n2, n4 := n/2, n/4
		trig := getMDCTTrigF32(n)
		st := getKissFFTState(n4)
		for iter := 0; iter < 20; iter++ {
			spectrum := make([]float32, n2)
			for i := range spectrum {
				spectrum[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(8))))
			}
			fftIn := make([]float32, 2*n4)
			got := imdctPreRotateFFT(fftIn, make([]float32, 2*n4), spectrum, trig, n2, n4, st)

			wantIn := make([]float32, 2*n4)
			imdctPreRotateF32Spectrum(wantIn, spectrum, trig, n2, n4)
			want := kissFFT32InterleavedToScratch(wantIn, make([]float32, 2*n4), st)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("n=%d iter %d: out[%d]=%v want %v", n, iter, i, got[i], want[i])
				}
			}
		}
	}
}

// TestIMDCTPostRotateScalarMatchesReference checks the scalar post-rotation
// against the clt_mdct_backward_c() loop written with its yp0/yp1 cursors.
func TestIMDCTPostRotateScalarMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9057))
	for _, n4 := range []int{1, 2, 3, 7, 30, 60, 120, 240, 480} {
		n2 := 2 * n4
		trig := make([]float32, n2)
		for i := range trig {
			trig[i] = float32(rng.Float64()*2 - 1)
		}
		fft := make([]float32, 2*n4)
		for i := range fft {
			fft[i] = float32(rng.NormFloat64() * 1e3)
		}
		got := make([]float32, n2)
		imdctPostRotateF32FromKissScalar(got, fft, trig, n2, n4)

		want := make([]float32, n2)
		yp0, yp1 := 0, n2-2
		for i := range (n4 + 1) >> 1 {
			k := n4 - 1 - i
			re, im := fft[2*i+1], fft[2*i]
			t0, t1 := trig[i], trig[n4+i]
			yr := mdctMulAddMix(re, im, t0, t1)
			yi := mdctMulSubMix(re, im, t1, t0)
			re2, im2 := fft[2*k+1], fft[2*k]
			want[yp0] = yr
			want[yp1+1] = yi
			t0, t1 = trig[n4-i-1], trig[n2-i-1]
			want[yp1] = mdctMulAddMix(re2, im2, t0, t1)
			want[yp0+1] = mdctMulSubMix(re2, im2, t1, t0)
			yp0 += 2
			yp1 -= 2
		}
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("n4=%d: buf[%d]=%v want %v", n4, i, got[i], want[i])
			}
		}
	}
}

func imdctPreRotateF32Ref(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}

	_ = spectrum[n2-1]
	_ = trig[n2-1]
	out := fftIn[:n4*2]
	_ = out[n4*2-1]

	i := 0
	for ; i+1 < n4; i += 2 {
		x10 := spectrum[2*i]
		x20 := spectrum[n2-1-2*i]
		t00 := trig[i]
		t10 := trig[n4+i]
		b0 := 2 * i
		out[b0] = x10*t00 - x20*t10
		out[b0+1] = x20*t00 + x10*t10

		i1 := i + 1
		x11 := spectrum[2*i1]
		x21 := spectrum[n2-1-2*i1]
		t01 := trig[i1]
		t11 := trig[n4+i1]
		b1 := 2 * i1
		out[b1] = x11*t01 - x21*t11
		out[b1+1] = x21*t01 + x11*t11
	}

	if i < n4 {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		b := 2 * i
		out[b] = x1*t0 - x2*t1
		out[b+1] = x2*t0 + x1*t1
	}
}

func imdctPostRotateF32Ref(buf []float32, trig []float32, n2, n4 int) {
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}

	_ = buf[n2-1]
	_ = trig[n2-1]

	yp0 := 0
	yp1 := n2 - 2
	for i := range limit {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}

func imdctPostRotateF32FromKissRef(buf []float32, fft []float32, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < 2*n4 {
		return
	}
	copy(buf[:2*n4], fft[:2*n4])
	imdctPostRotateF32Ref(buf, trig, n2, n4)
}

func TestIMDCTRotateDispatchMatchesReference(t *testing.T) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float32, n2)
	trig := make([]float32, n2)
	for i := range spectrum {
		spectrum[i] = float32((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}

	gotFFT := make([]float32, 2*n4)
	wantFFT := make([]float32, 2*n4)
	imdctPreRotateF32Spectrum(gotFFT, spectrum, trig, n2, n4)
	imdctPreRotateF32Ref(wantFFT, spectrum, trig, n2, n4)
	if !reflect.DeepEqual(gotFFT, wantFFT) {
		t.Fatalf("imdctPreRotateF32 mismatch")
	}

	gotBuf := make([]float32, n2)
	wantBuf := make([]float32, n2)
	copy(gotBuf, gotFFT)
	copy(wantBuf, wantFFT)
	imdctPostRotateF32(gotBuf, trig, n2, n4)
	imdctPostRotateF32Ref(wantBuf, trig, n2, n4)
	if !reflect.DeepEqual(gotBuf, wantBuf) {
		t.Fatalf("imdctPostRotateF32 mismatch")
	}
}

func TestIMDCTPostRotateF32FromKissMatchesReference(t *testing.T) {
	for _, n2 := range []int{10, 120} {
		n4 := n2 / 2
		trig := make([]float32, n2)
		fft := make([]float32, 2*n4)
		for i := range trig {
			trig[i] = float32((i%19)-9) * 0.03125
		}
		for i := range n4 {
			fft[2*i] = float32((i%17)-8) * 0.0625
			fft[2*i+1] = float32((i%23)-11) * -0.03125
		}

		got := make([]float32, n2)
		want := make([]float32, n2)
		imdctPostRotateF32FromKiss(got, fft, trig, n2, n4)
		imdctPostRotateF32FromKissRef(want, fft, trig, n2, n4)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("n2=%d imdctPostRotateF32FromKiss mismatch", n2)
		}
	}
}

func BenchmarkIMDCTPreRotateCurrent(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float32, n2)
	trig := make([]float32, n2)
	fftIn := make([]float32, 2*n4)
	for i := range spectrum {
		spectrum[i] = float32((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
	}
}

func BenchmarkIMDCTPreRotateReference(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float32, n2)
	trig := make([]float32, n2)
	fftIn := make([]float32, 2*n4)
	for i := range spectrum {
		spectrum[i] = float32((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPreRotateF32Ref(fftIn, spectrum, trig, n2, n4)
	}
}

func BenchmarkIMDCTPostRotateCurrent(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	buf := make([]float32, n2)
	trig := make([]float32, n2)
	for i := range buf {
		buf[i] = float32((i%23)-11) * 0.03125
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPostRotateF32(buf, trig, n2, n4)
	}
}

func BenchmarkIMDCTPostRotateReference(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	buf := make([]float32, n2)
	trig := make([]float32, n2)
	for i := range buf {
		buf[i] = float32((i%23)-11) * 0.03125
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPostRotateF32Ref(buf, trig, n2, n4)
	}
}

func pocFFTInput240() []complex64 {
	const n = 240
	in := make([]complex64, n)
	for i := range in {
		r := float32(math.Sin(float64(i)*0.079)*0.9 + math.Cos(float64(i+3)*0.031)*0.2)
		im := float32(math.Cos(float64(i)*0.053)*0.7 - math.Sin(float64(i+11)*0.017)*0.3)
		in[i] = complex(r, im)
	}
	return in
}

// BenchmarkPOCFFT240 times the production complex FFT at the decode bench's
// transform size (n4=240). Its implementation follows GOARCH and build
// constraints; GOEXPERIMENT=simd enables eligible Go SIMD kernels.
func BenchmarkPOCFFT240(b *testing.B) {
	in := pocFFTInput240()
	scratch := make([]float32, 2*len(in))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kissFFT32ToScratch(in, scratch, nil)
	}
}

// BenchmarkPOCIMDCT480 times the full IMDCT used by the 48 kHz 20 ms mono decode
// (pre-rotate + FFT + post-rotate + TDAC), matching synthesizeMonoLongToFloat32:
// spectrum n2=480, overlap=120. Run with GOEXPERIMENT=simd to measure eligible
// Go SIMD kernels, or with the ordinary/nosimd build to measure scalar Go.
func BenchmarkPOCIMDCT480(b *testing.B) {
	const (
		n2      = 480
		overlap = 120
	)
	spectrum := make([]float32, n2)
	for i := range spectrum {
		spectrum[i] = float32(math.Sin(float64(i)*0.017)*0.8 + math.Cos(float64(i+5)*0.013)*0.3)
	}
	prev := make([]float32, overlap)
	var scratch imdctScratchF32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imdctOverlapWithPrevScratchF32Output32(spectrum, prev, overlap, &scratch)
	}
}

func imdctTDACWindowScalarRef(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	for i := range count {
		x1 := xsrc[xSrc0-i]
		x2 := out[yOut0+i]
		w1 := window[i]
		w2 := window[wBwd0-i]
		var yOut, xOut float32
		if mdctUseFMALikeMixEnabled {
			yOut = float32(math.FMA(float64(x2), float64(w2), -float64(x1*w1)))
			xOut = float32(math.FMA(float64(x2), float64(w1), float64(x1*w2)))
		} else {
			yOut = x2*w2 - x1*w1
			xOut = x2*w1 + x1*w2
		}
		out[yOut0+i] = yOut
		out[xOut0-i] = xOut
	}
}

func TestIMDCTTDACWindowFMA32MatchesScalar(t *testing.T) {
	for _, overlap := range []int{2, 4, 6, 8, 16, 30, 60, 120, 240} {
		count := overlap / 2
		window := make([]float32, overlap)
		for i := range window {
			window[i] = float32((i*3%53)-26) * 0.018
		}

		// In-place variant: out region [blockStart, blockStart+overlap),
		// x1 from a separate buf indexed by xp1-start.
		blockStart := 3
		start := blockStart + overlap/2
		n := blockStart + overlap + 5
		buf := make([]float32, n)
		for i := range buf {
			buf[i] = float32((i*7%89)-44) * 0.011
		}
		got := make([]float32, n)
		want := make([]float32, n)
		for i := range got {
			got[i] = float32((i*5%71)-35) * 0.009
			want[i] = got[i]
		}
		yp1 := blockStart
		xp1 := blockStart + overlap - 1
		imdctTDACWindow(got, buf, window, yp1, xp1, xp1-start, overlap-1, count)
		imdctTDACWindowScalarRef(want, buf, window, yp1, xp1, xp1-start, overlap-1, count)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("inplace overlap=%d idx=%d: got %v want %v", overlap, i, got[i], want[i])
			}
		}

		// In-buffer variant: xsrc == out, xSrc0 == xOut0.
		got2 := make([]float32, overlap)
		want2 := make([]float32, overlap)
		for i := range got2 {
			got2[i] = float32((i*11%67)-33) * 0.013
			want2[i] = got2[i]
		}
		imdctTDACWindow(got2, got2, window, 0, overlap-1, overlap-1, overlap-1, count)
		imdctTDACWindowScalarRef(want2, want2, window, 0, overlap-1, overlap-1, overlap-1, count)
		for i := range want2 {
			if math.Float32bits(got2[i]) != math.Float32bits(want2[i]) {
				t.Fatalf("inbuf overlap=%d idx=%d: got %v want %v", overlap, i, got2[i], want2[i])
			}
		}
	}
}

func BenchmarkIMDCTTDACWindow(b *testing.B) {
	overlap := 120
	count := overlap / 2
	window := make([]float32, overlap)
	for i := range window {
		window[i] = float32((i*3%53)-26) * 0.018
	}
	out := make([]float32, overlap)
	for i := range out {
		out[i] = float32((i*5%71)-35) * 0.009
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctTDACWindow(out, out, window, 0, overlap-1, overlap-1, overlap-1, count)
	}
}
