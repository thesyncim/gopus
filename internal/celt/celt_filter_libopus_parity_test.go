package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	libopusCELTFilterModeDeemphasis      = uint32(0)
	libopusCELTFilterModeCombFilter      = uint32(1)
	libopusCELTFilterModeCombFilterInput = uint32(2)
)

var libopusCELTFilterHelper libopustest.HelperCache
var libopusCELTFilterScalarHelper libopustest.HelperCache

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
		SIMDRef:     true,
		// celt.c calls comb_filter_const through the host libopus feature
		// macros. On an x86 SIMD build that resolves through
		// COMB_FILTER_CONST_IMPL to comb_filter_const_sse; compile the matching
		// implementation and map alongside the two codec translation units.
		// Both files compile to empty/scalar sections when x86 SIMD is disabled.
		RefSources: []string{
			"celt/celt_decoder.c",
			"celt/celt.c",
			"celt/x86/pitch_sse.c",
			"celt/x86/x86_celt_map.c",
		},
		Libs:      []string{"-lm"},
		DeadStrip: true,
	})
}

func buildLibopusCELTFilterScalarHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:          "CELT filter scalar",
		OutputBase:     "gopus_libopus_celt_filter_scalar",
		SourceFile:     "libopus_celt_filter_info.c",
		CFlags:         []string{"-DHAVE_CONFIG_H", "-DRESYNTH", "-O3", "-DNDEBUG"},
		RefIncludes:    []string{"src", "celt", "silk", "silk/float"},
		ForceScalarRef: true,
		RefSources: []string{
			"celt/celt_decoder.c",
			"celt/celt.c",
		},
		Libs:      []string{"-lm"},
		DeadStrip: true,
	})
}

func runLibopusCELTFilter(t *testing.T, payload *libopustest.OraclePayload, simdKernel bool) *libopustest.OracleReader {
	t.Helper()
	helper := &libopusCELTFilterScalarHelper
	builder := buildLibopusCELTFilterScalarHelper
	if simdKernel {
		helper = &libopusCELTFilterHelper
		builder = buildLibopusCELTFilterHelper
	}
	binPath, err := helper.Path(builder)
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
	return probeLibopusDeemphasisWithOptions(t, channels, samples, mem, 1, nil)
}

func probeLibopusDeemphasisWithOptions(t *testing.T, channels int, samples [][]float32, mem []float32, downsample int, accum []float32) libopusDeemphasisResult {
	t.Helper()
	n := len(samples[0])
	if channels < 1 || channels > 2 || len(samples) != channels || len(mem) != channels || downsample < 1 || downsample > n {
		t.Fatalf("invalid deemphasis probe dimensions: channels=%d samples=%d mem=%d downsample=%d n=%d", channels, len(samples), len(mem), downsample, n)
	}
	count := channels * (n / downsample)
	accumFlag := uint32(0)
	if accum != nil {
		if len(accum) != count {
			t.Fatalf("accum length=%d want %d", len(accum), count)
		}
		accumFlag = 1
	}
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeDeemphasis)
	payload.U32(uint32(channels))
	payload.U32(uint32(n))
	payload.U32(uint32(downsample))
	payload.U32(accumFlag)
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
	if accumFlag != 0 {
		payload.Float32s(accum...)
	}
	variant := requirePairedCELTOracleMode(t)
	reader := runLibopusCELTFilter(t, payload, variant == libopustooling.LibopusReferenceSIMD)
	if gotMode := reader.U32(); gotMode != libopusCELTFilterModeDeemphasis {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTFilterModeDeemphasis)
	}
	count = int(reader.U32())
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
	requirePairedCELTOracleMode(t)

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
	requirePairedCELTOracleMode(t)

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
	requirePairedCELTOracleMode(t)

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
	requirePairedCELTOracleMode(t)

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
	requirePairedCELTOracleMode(t)

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
	requirePairedCELTOracleMode(t)

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

func TestDeemphasisSilenceTransitionsAndDownsampleStateMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels=%d/transition", channels), func(t *testing.T) {
			dec := NewDecoder(channels)
			var oracleMem = make([]float32, channels)
			for _, segment := range []struct {
				name    string
				length  int
				silence bool
			}{{"initial-silence", 17, true}, {"signal", 67, false}, {"silence-with-state", 23, true}, {"signal-again", 72, false}} {
				t.Run(segment.name, func(t *testing.T) {
					planes := make([][]float32, channels)
					for ch := range channels {
						planes[ch] = make([]float32, segment.length)
						if !segment.silence {
							for i := range segment.length {
								planes[ch][i] = float32(math.Sin(float64(i+2+ch*7)*0.137)*1800 + math.Cos(float64(i+5+ch*11)*0.191)*900)
							}
						}
					}
					want := probeLibopusDeemphasis(t, channels, planes, oracleMem)
					interleaved := interleaveDeemphasisPlanes(planes)
					dec.applyDeemphasisAndScale(interleaved, 1.0/32768.0)
					assertCELTFilterFloat32Bits(t, "pcm", interleaved, want.pcm)
					assertCELTFilterMemBits(t, dec, want.mem)
					copy(oracleMem, want.mem)
				})
			}
		})

		t.Run(fmt.Sprintf("channels=%d/downsample-state", channels), func(t *testing.T) {
			const (
				n          = 30
				downsample = 3
				outFrames  = n / downsample
			)
			planes := make([][]float32, channels)
			for ch := range channels {
				planes[ch] = make([]float32, n)
				for i := range n {
					planes[ch][i] = float32(math.Sin(float64(i+3+ch*5)*0.089)*950 + math.Cos(float64(i+1+ch*13)*0.127)*430)
				}
			}
			initialMem := make([]float32, channels)
			for ch := range channels {
				initialMem[ch] = float32(41.25 - float64(ch)*83.5)
			}
			want := probeLibopusDeemphasisWithOptions(t, channels, planes, initialMem, downsample, nil)
			dec := NewDecoder(channels)
			copy(dec.preemphState[:], initialMem)
			got := make([]float32, outFrames*channels)
			interleaved := interleaveDeemphasisPlanes(planes)
			dec.applyDeemphasisAndScaleDownsampleToFloat32(got, interleaved, downsample, 1.0/32768.0)
			assertCELTFilterFloat32Bits(t, "downsample pcm", got, want.pcm)
			assertCELTFilterMemBits(t, dec, want.mem)

			accum := make([]float32, len(got))
			for i := range accum {
				accum[i] = float32(0.125*float64(i+1) - 0.75)
			}
			wantAccum := probeLibopusDeemphasisWithOptions(t, channels, planes, initialMem, downsample, accum)
			dec = NewDecoder(channels)
			copy(dec.preemphState[:], initialMem)
			gotAccum := make([]float32, len(accum))
			dec.applyDeemphasisAndScaleDownsampleToFloat32(gotAccum, interleaved, downsample, 1.0/32768.0)
			for i := range gotAccum {
				gotAccum[i] += accum[i]
			}
			assertCELTFilterFloat32Bits(t, "downsample accumulated pcm", gotAccum, wantAccum.pcm)
			assertCELTFilterMemBits(t, dec, wantAccum.mem)
		})
	}
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

func interleaveDeemphasisPlanes(planes [][]float32) []float32 {
	n := len(planes[0])
	interleaved := make([]float32, n*len(planes))
	for i := range n {
		for ch := range planes {
			interleaved[i*len(planes)+ch] = planes[ch][i]
		}
	}
	return interleaved
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

func probeLibopusCombFilter(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilter, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterInput(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
	t.Helper()
	return probeLibopusCombFilterMode(t, libopusCELTFilterModeCombFilterInput, start, n, t0, t1, tapset0, tapset1, overlap, g0, g1, window, buf)
}

func probeLibopusCombFilterMode(t *testing.T, mode uint32, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window, buf []float32) []float32 {
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
		payload.Float32(sample)
	}
	reader := runLibopusCELTFilter(t, payload, celtFilterOracleUsesSIMD(mode))
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

// celtFilterOracleUsesSIMD pairs the comb-filter oracles with the libopus
// build for the Go tier. libopus routes both the postfilter and the
// prefilter/PLC comb through comb_filter(), whose constant-gain body x86 SIMD
// builds bind to comb_filter_const_sse; ARM builds have no NEON comb kernel, so
// their SIMD helper still compiles comb_filter_const_c.
func celtFilterOracleUsesSIMD(mode uint32) bool {
	isComb := mode == libopusCELTFilterModeCombFilter || mode == libopusCELTFilterModeCombFilterInput
	return isComb && (combUsesNeon || combUsesSSE)
}

func TestCELTFilterOracleDispatchIdentity(t *testing.T) {
	const n = 12
	dst := make([]float32, n)
	delay := make([]float32, n)
	_, _, _, _, dispatched := combFilterConstDispatch(dst, delay, 0.25, 0.5, 0.75, 0, 0, 0, 0)
	if dispatched != combUsesNeon {
		t.Fatalf("comb filter SIMD dispatch=%t, build selection=%t", dispatched, combUsesNeon)
	}
	// combFilterConstFloat32* and combFilterWithInputSig both follow
	// combUsesSSE, so both comb oracles pair with the SIMD reference exactly
	// when the Go build selects SSE or NEON.
	for _, mode := range []uint32{libopusCELTFilterModeCombFilter, libopusCELTFilterModeCombFilterInput} {
		if oracleSIMD := celtFilterOracleUsesSIMD(mode); oracleSIMD != (dispatched || combUsesSSE) {
			t.Fatalf("comb mode %d oracle SIMD=%t, Go SSE/NEON selection=%t", mode, oracleSIMD, dispatched || combUsesSSE)
		}
	}
	requirePairedCELTOracleMode(t)
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
	want := probeLibopusCombFilter(t, start, n, t0, t1, 0, 0, overlap, 0.28125, 0.65625, windowF32, buf)

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

func TestCombFilterConstantBodyHistorySeamMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start = combFilterHistory
		n     = 192
		gain  = float32(0.65625)
	)
	window := GetWindowBufferF32(Overlap)
	windowSq := GetWindowSquareBufferF32(Overlap)
	buf := make([]float32, start+n+2)
	for i := range buf {
		buf[i] = float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170)
	}
	hist := make([]celtSig, start)
	for i := range hist {
		hist[i] = celtSig(buf[i])
	}
	// Equal parameters suppress the overlap ramp. The constant body crosses
	// the stored-history seam at period-2, at each residue modulo four.
	for _, period := range []int{73, 74, 75, 76, 117, 118, 119, 120} {
		t.Run(fmt.Sprintf("period=%d/seam_mod4=%d", period, (period-2)&3), func(t *testing.T) {
			want := probeLibopusCombFilter(t, start, n, period, period, 0, 0, Overlap, gain, gain, window, buf)
			got := append([]float32(nil), buf[start:]...)
			combFilterWithSquarePlanarFloat32(got, hist, start, 0, period, period, n,
				gain, gain, 0, 0, window, windowSq, Overlap)
			for i := range n {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("sample[%d]=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
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
			overlap := tc.overlap
			var win32 []float32
			if tc.useWindow {
				win32 = window32
			} else {
				overlap = 0
			}

			want := probeLibopusCombFilterInput(t, start, tc.n, tc.t0, tc.t1, tc.tapset0, tc.tapset1, overlap, tc.g0, tc.g1, win32, srcSig)
			got := append([]celtSig(nil), srcSig...)
			combFilterWithInputSig(got, srcSig, start, tc.t0, tc.t1, tc.n, tc.g0, tc.g1, tc.tapset0, tc.tapset1, win32, overlap)
			got32 := make([]float32, tc.n)
			copySigToFloat32(got32, got[start:start+tc.n])
			assertFloat32Bits(t, "comb", got32, want)
		})
	}
}
