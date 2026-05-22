package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTIMDCTModeLong      = uint32(0)
	libopusCELTIMDCTModeTransient = uint32(1)
	libopusCELTIMDCTModeFFT       = uint32(2)
	libopusCELTIMDCTModeForward   = uint32(3)
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
	for i := 0; i < overlap; i++ {
		payload.Float32(prevOverlap[i])
	}
	for i := 0; i < frameSize; i++ {
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

func imdctOverlapWithPrevScratchF32LegacyBufferCopy(out []float64, spectrum []float64, prevOverlap []float64, overlap int, scratch *imdctScratchF32) {
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

	var fftIn []complex64
	var fftTmp []kissCpx
	var buf []float32
	var outF32 []float32
	if scratch == nil {
		fftIn = make([]complex64, n4)
		fftTmp = make([]kissCpx, n4)
		buf = make([]float32, n2)
		outF32 = make([]float32, needed)
	} else {
		fftIn = ensureComplex64Slice(&scratch.fftIn, n4)
		fftTmp = ensureKissCpxSlice(&scratch.fftTmp, n4)
		buf = ensureFloat32Slice(&scratch.buf, n2)
		outF32 = ensureFloat32Slice(&scratch.out, needed)
	}

	if start+n2 < needed {
		clear(outF32[start+n2 : needed])
	}

	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := min(len(prevOverlap), overlap)
		for i := 0; i < copyLen; i++ {
			outF32[i] = float32(prevOverlap[i])
		}
		if copyLen < overlap {
			clear(outF32[copyLen:overlap])
		}
	} else if overlap > 0 {
		clear(outF32[:overlap])
	}

	imdctPreRotateF32(fftIn, spectrum, trig, n2, n4)
	kissFFT32ToInterleaved(buf, fftIn, fftTmp)
	imdctPostRotateF32(buf, trig, n2, n4)
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
	for i := 0; i < needed; i++ {
		out[i] = float64(outF32[i])
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
				spectrum := make([]float64, tc.frameSize)
				prevOverlap := make([]float64, tc.overlap)
				for i := range spectrum {
					sine := math.Sin(float64(i+seed*11) * 0.063)
					cosine := math.Cos(float64((i+1)*(seed+5)) * 0.017)
					step := float64((i*13+seed*29)%23-11) / 28.0
					spectrum[i] = 0.6*sine + 0.25*cosine + step
				}
				for i := range prevOverlap {
					sine := math.Sin(float64(i+seed*3) * 0.041)
					step := float64((i*7+seed*19)%17-8) / 20.0
					prevOverlap[i] = 0.7*sine + step
				}

				got := make([]float64, tc.frameSize+tc.overlap)
				want := make([]float64, tc.frameSize+tc.overlap)
				imdctOverlapWithPrevScratchF32(got, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})
				imdctOverlapWithPrevScratchF32LegacyBufferCopy(want, spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})

				for i := range want {
					if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
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

				got := imdctOverlapWithPrevScratchF32Output(spectrum, prevOverlap, tc.overlap, &imdctScratchF32{})
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

				out := make([]float64, tc.frameSize+tc.overlap)
				copy(out[:tc.overlap], prevOverlap)
				shortSize := tc.frameSize / tc.shortBlocks
				shortCoeffs := make([]float64, shortSize)
				var scratch imdctScratchF32
				for b := 0; b < tc.shortBlocks; b++ {
					idx := b
					for i := 0; i < shortSize; i++ {
						shortCoeffs[i] = spectrum[idx]
						idx += tc.shortBlocks
					}
					imdctInPlaceScratchF32(shortCoeffs, out, b*shortSize, tc.overlap, &scratch)
				}

				got := make([]float32, len(out))
				for i := range out {
					got[i] = float32(out[i])
				}
				want := probeLibopusCELTIMDCT(t, libopusCELTIMDCTModeTransient, tc.frameSize, tc.overlap, tc.shortBlocks, spectrumF32, prevOverlapF32)
				assertFloat32Bits(t, "transient imdct", got, want)
			})
		}
	}
}

func TestMDCTForwardOverlapF32MatchesLibopusC(t *testing.T) {
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

				coeffs := make([]float64, tc.frameSize)
				mdctForwardOverlapF32Scratch(input, tc.overlap, coeffs, nil, nil, nil, nil)
				got := make([]float32, len(coeffs))
				for i := range coeffs {
					got[i] = float32(coeffs[i])
				}

				want := probeLibopusCELTMDCTForward(t, tc.frameSize, tc.overlap, inputF32)
				assertFloat32Bits(t, "forward mdct", got, want)
			})
		}
	}
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

func assertFloat32Bits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %.9g want %08x %.9g ulp=%d",
				label, i, math.Float32bits(got[i]), got[i],
				math.Float32bits(want[i]), want[i], ulpDiffFloat32(got[i], want[i]))
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
