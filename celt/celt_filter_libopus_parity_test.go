package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTFilterModeDeemphasis = uint32(0)
	libopusCELTFilterModeCombFilter = uint32(1)
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
	for i := 0; i < channels; i++ {
		payload.Float32(mem[i])
	}
	for ch := 0; ch < channels; ch++ {
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

	const n = 67
	samples := make([]float64, n)
	samples32 := make([]float32, n)
	for i := range samples {
		v := float32(math.Sin(float64(i+1)*0.173)*2100 + math.Cos(float64(i+3)*0.071)*650)
		samples[i] = float64(v)
		samples32[i] = v
	}
	initialMem := []float32{float32(-312.75)}
	want := probeLibopusDeemphasis(t, 1, [][]float32{samples32}, initialMem)

	dec := NewDecoder(1)
	dec.preemphState[0] = float64(initialMem[0])
	got := make([]float32, n)
	dec.applyDeemphasisAndScaleToFloat32(got, samples, 1.0/32768.0)

	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want.pcm[i]) {
			t.Fatalf("pcm[%d]=%08x %0.10g want %08x %0.10g sample=%08x %0.10g",
				i, math.Float32bits(got[i]), got[i], math.Float32bits(want.pcm[i]), want.pcm[i],
				math.Float32bits(samples32[i]), samples32[i])
		}
	}
	if math.Float32bits(float32(dec.preemphState[0])) != math.Float32bits(want.mem[0]) {
		t.Fatalf("mem=%08x want %08x", math.Float32bits(float32(dec.preemphState[0])), math.Float32bits(want.mem[0]))
	}
}

func TestApplyDeemphasisAndScaleToFloat32StereoMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 61
	left, right := makeStereoDeemphasisSamples(n)
	interleaved := make([]float64, n*2)
	for i := 0; i < n; i++ {
		interleaved[2*i] = float64(left[i])
		interleaved[2*i+1] = float64(right[i])
	}
	initialMem := []float32{float32(-129.5), float32(84.25)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left, right}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = float64(initialMem[0])
	dec.preemphState[1] = float64(initialMem[1])
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleToFloat32(got, interleaved, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func TestApplyDeemphasisAndScaleMonoFloat32ToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 73
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(math.Sin(float64(i+2)*0.137)*1800 + math.Cos(float64(i+5)*0.191)*900)
	}
	initialMem := []float32{float32(511.25)}
	want := probeLibopusDeemphasis(t, 1, [][]float32{samples}, initialMem)

	dec := NewDecoder(1)
	dec.preemphState[0] = float64(initialMem[0])
	got := make([]float32, n)
	dec.applyDeemphasisAndScaleMonoFloat32ToFloat32(got, samples, 1.0/32768.0)

	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want.pcm[i]) {
			t.Fatalf("pcm[%d]=%08x %0.10g want %08x %0.10g sample=%08x %0.10g",
				i, math.Float32bits(got[i]), got[i], math.Float32bits(want.pcm[i]), want.pcm[i],
				math.Float32bits(samples[i]), samples[i])
		}
	}
	if math.Float32bits(float32(dec.preemphState[0])) != math.Float32bits(want.mem[0]) {
		t.Fatalf("mem=%08x want %08x", math.Float32bits(float32(dec.preemphState[0])), math.Float32bits(want.mem[0]))
	}
}

func TestApplyDeemphasisAndScaleInPlaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 59
	left, right := makeStereoDeemphasisSamples(n)
	samples := make([]float64, n*2)
	for i := 0; i < n; i++ {
		samples[2*i] = float64(left[i])
		samples[2*i+1] = float64(right[i])
	}
	initialMem := []float32{float32(91.75), float32(-44.5)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left, right}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = float64(initialMem[0])
	dec.preemphState[1] = float64(initialMem[1])
	dec.applyDeemphasisAndScale(samples, 1.0/32768.0)

	got := make([]float32, len(samples))
	for i := range samples {
		got[i] = float32(samples[i])
	}
	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func TestApplyDeemphasisAndScaleStereoPlanarToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 65
	left32, right32 := makeStereoDeemphasisSamples(n)
	left := make([]float64, n)
	right := make([]float64, n)
	for i := 0; i < n; i++ {
		left[i] = float64(left32[i])
		right[i] = float64(right32[i])
	}
	initialMem := []float32{float32(277.25), float32(-193.125)}
	want := probeLibopusDeemphasis(t, 2, [][]float32{left32, right32}, initialMem)

	dec := NewDecoder(2)
	dec.preemphState[0] = float64(initialMem[0])
	dec.preemphState[1] = float64(initialMem[1])
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleStereoPlanarToFloat32(got, left, right, 1.0/32768.0)

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
	dec.preemphState[0] = float64(initialMem[0])
	dec.preemphState[1] = float64(initialMem[1])
	got := make([]float32, n*2)
	dec.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(got, left, right, 1.0/32768.0)

	assertCELTFilterFloat32Bits(t, "pcm", got, want.pcm)
	assertCELTFilterMemBits(t, dec, want.mem)
}

func makeStereoDeemphasisSamples(n int) ([]float32, []float32) {
	left := make([]float32, n)
	right := make([]float32, n)
	for i := 0; i < n; i++ {
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
		if math.Float32bits(float32(dec.preemphState[i])) != math.Float32bits(want[i]) {
			t.Fatalf("mem[%d]=%08x want %08x", i, math.Float32bits(float32(dec.preemphState[i])), math.Float32bits(want[i]))
		}
	}
}

func probeLibopusCombFilter(t *testing.T, start, n, t0, t1, tapset0, tapset1, overlap int, g0, g1 float32, window []float32, buf []float64) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCFI", libopusCELTFilterModeCombFilter)
	payload.U32(uint32(start))
	payload.U32(uint32(n))
	payload.U32(uint32(t0))
	payload.U32(uint32(t1))
	payload.U32(uint32(tapset0))
	payload.U32(uint32(tapset1))
	payload.U32(uint32(overlap))
	payload.Float32(g0)
	payload.Float32(g1)
	for i := 0; i < overlap; i++ {
		payload.Float32(window[i])
	}
	for _, sample := range buf {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTFilter(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTFilterModeCombFilter {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTFilterModeCombFilter)
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
	window := GetWindowBuffer(overlap)
	windowF32 := GetWindowBufferF32(overlap)
	windowSq := GetWindowSquareBuffer(overlap)
	buf := make([]float64, start+n+2)
	for i := range buf {
		buf[i] = float64(float32(math.Sin(float64(i+11)*0.031)*2300 + math.Cos(float64(i+7)*0.017)*170))
	}
	want := probeLibopusCombFilter(t, start, n, t0, t1, 0, 0, overlap, 0.28125, 0.65625, windowF32, buf)

	got := append([]float64(nil), buf...)
	combFilterWithSquare(got, start, t0, t1, n, 0.28125, 0.65625, 0, 0, window, windowSq, overlap)
	for i := 0; i < n; i++ {
		got32 := float32(got[start+i])
		if math.Float32bits(got32) != math.Float32bits(want[i]) {
			t.Fatalf("sample[%d]=%08x want %08x", i, math.Float32bits(got32), math.Float32bits(want[i]))
		}
	}
}
