package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTFilterModeDeemphasis      = uint32(0)
	libopusCELTFilterModeCombFilter      = uint32(1)
	libopusCELTFilterModeCombFilterInput = uint32(2)
)

var libopusCELTFilterHelper libopustest.HelperCache

type libopusDeemphasisResult struct {
	mem []float32
	pcm []float32
}

func buildLibopusCELTFilterHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT filter",
		OutputBase:  "gopus_libopus_celt_filter",
		SourceFile:  "libopus_celt_filter_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		RefSources:  []string{"celt/celt_decoder.c", "celt/celt.c"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

func runLibopusCELTFilter(t *testing.T, payload *libopustest.OraclePayload) *libopustest.OracleReader {
	t.Helper()
	binPath, err := libopusCELTFilterHelper.Path(buildLibopusCELTFilterHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT filter", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT filter", "GCFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT filter", err)
	}
	return reader
}

func probeLibopusDeemphasis(t *testing.T, channels int, samples [][]float32, mem []float32) libopusDeemphasisResult {
	t.Helper()
	n := len(samples[0])
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeDeemphasis)
	payload.U32(uint32(channels))
	payload.U32(uint32(n))
	payload.U32(1)
	payload.U32(0)
	payload.Float32(float32(PreemphCoef))
	payload.Float32(0)
	payload.Float32(1)
	payload.Float32(1)
	for i := range channels {
		payload.Float32(mem[i])
	}
	for ch := range channels {
		for _, sample := range samples[ch] {
			payload.Float32(sample)
		}
	}
	reader := runLibopusCELTFilter(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTFilterModeDeemphasis {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTFilterModeDeemphasis)
	}
	count := int(reader.U32())
	out := libopusDeemphasisResult{
		mem: make([]float32, channels),
		pcm: make([]float32, count),
	}
	for i := range out.mem {
		out.mem[i] = reader.Float32()
	}
	reader.ExpectRemaining(count * 4)
	for i := range out.pcm {
		out.pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestApplyDeemphasisAndScaleToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	const n = 67
	samples32 := make([]float32, n)
	for i := range samples32 {
		v := float32(math.Sin(float64(i+1)*0.173)*2100 + math.Cos(float64(i+3)*0.071)*650)
		samples32[i] = v
	}
	initialMem := []float32{float32(-312.75)}
	want := probeLibopusDeemphasis(t, 1, [][]float32{samples32}, initialMem)

	dec := NewDecoder(1)
	dec.preemphState[0] = initialMem[0]
	got := make([]float32, n)
	dec.applyDeemphasisAndScaleToFloat32(got, samples32, 1.0/32768.0)

	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want.pcm[i]) {
			t.Fatalf("pcm[%d]=%08x %0.10g want %08x %0.10g sample=%08x %0.10g",
				i, math.Float32bits(got[i]), got[i], math.Float32bits(want.pcm[i]), want.pcm[i],
				math.Float32bits(samples32[i]), samples32[i])
		}
	}
	if math.Float32bits(dec.preemphState[0]) != math.Float32bits(want.mem[0]) {
		t.Fatalf("mem=%08x want %08x", math.Float32bits(dec.preemphState[0]), math.Float32bits(want.mem[0]))
	}
}

func TestApplyDeemphasisAndScaleToFloat32StereoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	const n = 61
	left, right := makeStereoDeemphasisSamples(n)
	interleaved := make([]float32, n*2)
	for i := range n {
		interleaved[2*i] = left[i]
		interleaved[2*i+1] = right[i]
	}
	initialMem := []float32{float32(-129.5), float32(84.25)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left, right}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = initialMem[0]
	dec.preemphState[1] = initialMem[1]
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleToFloat32(got, interleaved, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func TestApplyDeemphasisAndScaleMonoFloat32ToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	const n = 73
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(math.Sin(float64(i+2)*0.137)*1800 + math.Cos(float64(i+5)*0.191)*900)
	}
	initialMem := []float32{float32(511.25)}
	want := probeLibopusDeemphasis(t, 1, [][]float32{samples}, initialMem)

	dec := NewDecoder(1)
	dec.preemphState[0] = initialMem[0]
	got := make([]float32, n)
	dec.applyDeemphasisAndScaleMonoFloat32ToFloat32(got, samples, 1.0/32768.0)

	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want.pcm[i]) {
			t.Fatalf("pcm[%d]=%08x %0.10g want %08x %0.10g sample=%08x %0.10g",
				i, math.Float32bits(got[i]), got[i], math.Float32bits(want.pcm[i]), want.pcm[i],
				math.Float32bits(samples[i]), samples[i])
		}
	}
	if math.Float32bits(dec.preemphState[0]) != math.Float32bits(want.mem[0]) {
		t.Fatalf("mem=%08x want %08x", math.Float32bits(dec.preemphState[0]), math.Float32bits(want.mem[0]))
	}
}

func TestApplyDeemphasisAndScaleInPlaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	const n = 59
	left, right := makeStereoDeemphasisSamples(n)
	samples := make([]float32, n*2)
	for i := range n {
		samples[2*i] = left[i]
		samples[2*i+1] = right[i]
	}
	initialMem := []float32{float32(91.75), float32(-44.5)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left, right}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = initialMem[0]
	dec.preemphState[1] = initialMem[1]
	dec.applyDeemphasisAndScale(samples, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", samples, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func TestApplyDeemphasisAndScaleStereoPlanarToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 65
	left32, right32 := makeStereoDeemphasisSamples(n)
	initialMem := []float32{float32(277.25), float32(-193.125)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left32, right32}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = initialMem[0]
	dec.preemphState[1] = initialMem[1]
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleStereoPlanarToFloat32(got, left32, right32, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func TestApplyDeemphasisAndScaleStereoPlanarFloat32ToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 71
	left, right := makeStereoDeemphasisSamples(n)
	initialMem := []float32{float32(-71.875), float32(311.5)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left, right}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = initialMem[0]
	dec.preemphState[1] = initialMem[1]
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(got, left, right, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func makeStereoDeemphasisSamples(n int) ([]float32, []float32) {
	left := make([]float32, n)
	right := make([]float32, n)
	for i := range n {
		left[i] = float32(math.Sin(float64(i+4)*0.113)*1900 + math.Cos(float64(i+9)*0.047)*720)
		right[i] = float32(math.Cos(float64(i+6)*0.151)*1600 - math.Sin(float64(i+2)*0.083)*810)
	}
	return left, right
}

func assertCELTFilterFloat32Bits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func assertCELTFilterMemBits(t *testing.T, dec *Decoder, want []float32) {
	t.Helper()
	for i := range want {
		if math.Float32bits(dec.preemphState[i]) != math.Float32bits(want[i]) {
			t.Fatalf("mem[%d]=%08x want %08x", i, math.Float32bits(dec.preemphState[i]), math.Float32bits(want[i]))
		}
	}
}

func probeLibopusCombFilter(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window []float32, buf []float64) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilter, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterInput(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window []float32, buf []float64) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilterInput, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterMode(t *testing.T, mode uint32, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window []float32, buf []float64) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCFI", mode)
	payload.U32(uint32(start))
	payload.U32(uint32(n))
	payload.U32(uint32(t0))
	payload.U32(uint32(t1))
	payload.U32(uint32(tapset0))
	payload.U32(uint32(tapset1))
	payload.U32(uint32(overlap))
	payload.Float32(g0)
	payload.Float32(g1)
	for i := range overlap {
		payload.Float32(window[i])
	}
	for _, sample := range buf {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTFilter(t, payload)
	if gotMode := reader.U32(); gotMode != mode {
		t.Fatalf("helper mode=%d want %d", gotMode, mode)
	}
	count := int(reader.U32())
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCombFilterWithSquareMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start   = combFilterHistory
		n       = 192
		t0      = 37
		t1      = 40
		overlap = Overlap
	)
	windowF32 := GetWindowBufferF32(overlap)
	windowSq := GetWindowSquareBufferF32(overlap)
	buf := make([]float32, start+n+2)
	for i := range buf {
		buf[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
	}
	buf64 := make([]float64, len(buf))
	for i := range buf {
		buf64[i] = float64(buf[i])
	}
	want := probeLibopusCombFilter(t, start, n, t0, t1, 0, 0, overlap, 0.28125, 0.65625, windowF32, buf64)

	hist := make([]celtSig, start)
	for i := range hist {
		hist[i] = celtSig(buf[i])
	}
	got := append([]float32(nil), buf[start:]...)
	combFilterWithSquarePlanarFloat32(got, hist, start, 0, t0, t1, n, 0.28125, 0.65625, 0, 0, windowF32, windowSq, overlap)
	for i := range n {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample[%d]=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestCombFilterWithInputF32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	window32 := GetWindowBufferF32(Overlap)
	cases := []struct {
		name      string
		seed      uint32
		n         int
		t0        int
		t1        int
		g0        float32
		g1        float32
		tapset0   int
		tapset1   int
		overlap   int
		useWindow bool
	}{
		{"changed_filter_overlap", 0xabc101, 480, 151, 143, 0.21875, 0.34375, 0, 1, Overlap, true},
		{"steady_filter_no_overlap", 0xabc102, 960, 320, 320, 0.4375, 0.4375, 2, 2, 0, false},
		{"g1_zero_tail_copy", 0xabc103, 240, 77, 97, 0.3125, 0, 1, 0, 120, true},
		{"short_frame", 0xabc104, 120, 47, 91, -0.125, 0.5625, 2, 0, 64, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := combFilterHistory
			srcSig := makeCELTPLCTestSignal(start+tc.n+2, tc.seed, 1.0)
			src := make([]float64, len(srcSig))
			for i := range srcSig {
				src[i] = float64(srcSig[i])
			}
			overlap := tc.overlap
			var win32 []float32
			if tc.useWindow {
				win32 = window32
			} else {
				overlap = 0
			}

			want := probeLibopusCombFilterInput(t, start, tc.n, tc.t0, tc.t1, tc.tapset0, tc.tapset1, overlap, tc.g0, tc.g1, win32, src)
			got := append([]celtSig(nil), srcSig...)
			combFilterWithInputSig(got, srcSig, start, tc.t0, tc.t1, tc.n, tc.g0, tc.g1, tc.tapset0, tc.tapset1, win32, overlap)
			got32 := make([]float32, tc.n)
			copySigToFloat32(got32, got[start:start+tc.n])
			assertFloat32Bits(t, "comb", got32, want)
		})
	}
}
