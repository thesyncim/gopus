package celt

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/util"
	"math"
	"math/rand"
	"testing"
)

var pitchDownsampleSink32 float32

func benchmarkPitchDownsampleF32(b *testing.B, channels int, fn func([]float64, []float32, int, int, int)) {
	rng := rand.New(rand.NewSource(43))
	length := (combFilterMaxPeriod + 480) >> 1
	xLen := 2 * length
	if channels == 2 {
		xLen *= 2
	}
	x := make([]float64, xLen)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	xLP := make([]float32, length)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(x, xLP, length, channels, 2)
	}
	pitchDownsampleSink32 = xLP[length-1]
}

func BenchmarkPitchDownsampleCurrentMono(b *testing.B) {
	benchmarkPitchDownsampleF32(b, 1, pitchDownsample)
}

func BenchmarkPitchDownsampleCurrentStereo(b *testing.B) {
	benchmarkPitchDownsampleF32(b, 2, pitchDownsample)
}

func TestCeltFIR5MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0xf15))
	val := func() float32 {
		switch rng.Intn(8) {
		case 0:
			return 0
		case 1:
			return float32(math.Copysign(0, -1))
		}
		return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
	}
	for iter := range 2000 {
		n := rng.Intn(40)
		if iter%10 == 0 {
			n = 500 + rng.Intn(600)
		}
		var num [5]float32
		for i := range num {
			num[i] = val()
		}
		got := make([]float32, n)
		for i := range got {
			got[i] = val()
		}
		want := append([]float32(nil), got...)
		celtFIR5F32(got, num)
		celtFIR5Scalar(want, num)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d n=%d: x[%d]=%v want %v", iter, n, i, got[i], want[i])
			}
		}
	}
}

// TestPitchDownsample2MatchesScalar checks the dispatched factor-2 decimation
// against the scalar loops, bit for bit, for mono and stereo input.
func TestPitchDownsample2MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0xd0a2))
	for iter := range 1000 {
		n := 2 + rng.Intn(40)
		if iter%10 == 0 {
			n = 300 + rng.Intn(400)
		}
		x0 := make([]float32, 2*n)
		x1 := make([]float32, 2*n)
		for i := range x0 {
			x0[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
			x1[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(9)-4)))
		}
		for _, second := range [][]float32{nil, x1} {
			got := make([]float32, n)
			want := make([]float32, n)
			pitchDownsample2(got, x0, second)
			pitchDownsample2Scalar(want, x0, second, 1)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("iter %d n=%d stereo=%v: dst[%d]=%v want %v", iter, n, second != nil, i, got[i], want[i])
				}
			}
		}
	}
}

// TestPitchDownsampleSigZeroAllocs locks the pitch_downsample() path, with its
// decimation and celt_fir5, to zero allocations.
func TestPitchDownsampleSigZeroAllocs(t *testing.T) {
	for _, channels := range []int{1, 2} {
		const length = 572
		x := make([]celtSig, channels*2*length)
		for i := range x {
			x[i] = float32((i*37)%101-50) * 0.01
		}
		lp := make([]float32, length)
		pitchDownsampleSig(x, lp, length, channels, 2)
		if allocs := testing.AllocsPerRun(20, func() { pitchDownsampleSig(x, lp, length, channels, 2) }); allocs != 0 {
			t.Fatalf("channels=%d: %v allocs per run, want 0", channels, allocs)
		}
	}
}

var pitchSearchSink int

func pitchSearchLegacy(xLP []float32, y []float32, length, maxPitch int, scratch *encoderScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch

	xLP4 := ensureFloat32Slice(&scratch.prefilterXLP4, length>>2)
	yLP4 := ensureFloat32Slice(&scratch.prefilterYLP4, lag>>2)
	xcorr := ensureFloat32Slice(&scratch.prefilterXcorr, maxPitch>>1)

	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}

	pitchXCorrFloat32(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitchF32(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if util.Abs(i-2*bestPitch[0]) > 2 && util.Abs(i-2*bestPitch[1]) > 2 {
			continue
		}
		sum := innerProdFloat32(xLP, y[i:], length>>1)
		if sum < -1 {
			sum = -1
		}
		xcorr[i] = sum
	}
	findBestPitchF32(xcorr, y, length>>1, maxPitch>>1, &bestPitch)

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		if (c - a) > 0.7*(b-a) {
			offset = 1
		} else if (a - c) > 0.7*(b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

func benchmarkPitchSearch(b *testing.B, fn func([]float32, []float32, int, int, *encoderScratch) int) {
	rng := rand.New(rand.NewSource(42))
	length := 480
	maxPitch := combFilterMaxPeriod - 3*combFilterMinPeriod
	xLP := make([]float32, length)
	y := make([]float32, length+maxPitch)
	for i := range xLP {
		xLP[i] = float32(rng.Float64()*2 - 1)
	}
	for i := range y {
		y[i] = float32(rng.Float64()*2 - 1)
	}
	var scratch encoderScratch
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pitchSearchSink = fn(xLP, y, length, maxPitch, &scratch)
	}
}

func BenchmarkPitchSearchCurrent(b *testing.B) {
	benchmarkPitchSearch(b, pitchSearch)
}

func BenchmarkPitchSearchLegacy(b *testing.B) {
	benchmarkPitchSearch(b, pitchSearchLegacy)
}

func TestPitchSearchNearTieMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const length = 960
	assertMatch := func(t *testing.T, x, y []float32, maxPitch int) {
		t.Helper()
		want := probeLibopusPLCPitchSearch(t, x, y, length, maxPitch)
		var scratch encoderScratch
		got := pitchSearch(x, y, length, maxPitch, &scratch)
		if got != want {
			t.Fatalf("pitch lag=%d selected libopus=%d", got, want)
		}
		if allocs := testing.AllocsPerRun(100, func() {
			pitchSearch(x, y, length, maxPitch, &scratch)
		}); allocs != 0 {
			t.Fatalf("warm pitch search allocated %.2f times per call", allocs)
		}
	}

	for _, tc := range []struct {
		maxPitch int
		period   float64
	}{
		{975, 50.3},
		{976, 23.7},
		{977, 73.1},
	} {
		t.Run(fmt.Sprintf("max%d_period%.1f", tc.maxPitch, tc.period), func(t *testing.T) {
			y := make([]float32, length+tc.maxPitch)
			phase := 0.0
			for i := range y {
				wobble := 0.000003 * math.Sin(float64(i)*0.011)
				phase += 2 * math.Pi * (1/tc.period + wobble)
				y[i] = float32(0.45*math.Sin(phase) + 0.12*math.Sin(2*phase) +
					0.001*math.Sin(0.37*float64(i)))
			}
			x := make([]float32, length)
			copy(x, y[512:512+length])
			assertMatch(t, x, y, tc.maxPitch)
		})
	}

	t.Run("finite_cancellation_max979", func(t *testing.T) {
		const maxPitch = 979
		y := make([]float32, length+maxPitch)
		x := make([]float32, length)
		for i := range y {
			y[i] = float32(0.9 + 0.001*math.Sin(float64(i)*0.17))
		}
		for i := range x {
			sign := float32(1)
			if i&1 != 0 {
				sign = -1
			}
			x[i] = sign + float32(0.0001*math.Sin(float64(i)*0.037))
		}
		assertMatch(t, x, y, maxPitch)
	})
}

func TestFindBestPitchInRangesMatchesFullSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := range 500 {
		length := 16 + rng.Intn(128)
		maxPitch := 8 + rng.Intn(96)
		y := make([]float64, length+maxPitch)
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}

		a := rng.Intn(maxPitch)
		b := rng.Intn(maxPitch)
		ranges := normalizePitchSearchRanges(
			pitchSearchRange{lo: max(0, a-2), hi: min(maxPitch-1, a+2)},
			pitchSearchRange{lo: max(0, b-2), hi: min(maxPitch-1, b+2)},
		)

		xcorr := make([]float64, maxPitch)
		for _, r := range ranges {
			if r.hi < r.lo {
				continue
			}
			for i := r.lo; i <= r.hi; i++ {
				switch rng.Intn(4) {
				case 0:
					xcorr[i] = 0
				case 1:
					xcorr[i] = -(rng.Float64() * 2)
				default:
					xcorr[i] = rng.Float64() * 8
				}
			}
		}

		var want [2]int
		findBestPitch(xcorr, y, length, maxPitch, &want)

		var got [2]int
		findBestPitchInRanges(xcorr, y, length, ranges, &got)

		if got != want {
			t.Fatalf("iter %d mismatch: got=%v want=%v ranges=%v", iter, got, want, ranges)
		}
	}
}

func TestPitchSearchMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	for iter := range 200 {
		length := 480
		maxPitch := combFilterMaxPeriod - 3*combFilterMinPeriod
		xLP := make([]float32, length)
		y := make([]float32, length+maxPitch)
		for i := range xLP {
			xLP[i] = float32(rng.Float64()*2 - 1)
		}
		for i := range y {
			y[i] = float32(rng.Float64()*2 - 1)
		}

		var scratchCurrent, scratchLegacy encoderScratch
		got := pitchSearch(xLP, y, length, maxPitch, &scratchCurrent)
		want := pitchSearchLegacy(xLP, y, length, maxPitch, &scratchLegacy)
		if got != want {
			t.Fatalf("iter %d mismatch: got=%d want=%d", iter, got, want)
		}
	}
}

func benchPitchXcorrF32(b *testing.B, length, maxPitch int) {
	benchPitchXcorrF32With(b, length, maxPitch, pitchXCorrFloat32)
}

func benchPitchXcorrF32With(b *testing.B, length, maxPitch int, fn func([]float32, []float32, []float32, int, int)) {
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, maxPitch+length)
	xcorr := make([]float32, maxPitch)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(x, y, xcorr, length, maxPitch)
	}
}

// BenchmarkXcorrF32Coarse mirrors the quarter-resolution coarse pitch search.
func BenchmarkXcorrF32Coarse(b *testing.B) { benchPitchXcorrF32(b, 240, 360) }

// BenchmarkXcorrF32Half mirrors the half-resolution fine pitch search window.
func BenchmarkXcorrF32Half(b *testing.B) { benchPitchXcorrF32(b, 480, 64) }

// BenchmarkXcorrF32TinyCoarse mirrors the 2.5 ms / 8 kHz encoder coarse pitch
// search shape after quarter decimation.
func BenchmarkXcorrF32TinyCoarse(b *testing.B) { benchPitchXcorrF32(b, 5, 244) }

func BenchmarkXcorrF32TinyCoarsePLCOrder(b *testing.B) {
	benchPitchXcorrF32With(b, 5, 244, pitchXCorrFloat32PLC)
}

// BenchmarkXcorrF32TinyFine mirrors the 2.5 ms / 8 kHz encoder fine-search
// window shape.
func BenchmarkXcorrF32TinyFine(b *testing.B) { benchPitchXcorrF32(b, 10, 10) }

func BenchmarkXcorrF32TinyFinePLCOrder(b *testing.B) {
	benchPitchXcorrF32With(b, 10, 10, pitchXCorrFloat32PLC)
}

// BenchmarkXcorrKernel4F32 isolates the inner 4-lag kernel (scalar reference).
func BenchmarkXcorrKernel4F32(b *testing.B) {
	const length = 480
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, length+4)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	var sum [4]float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sum = [4]float32{}
		xcorrKernel4Float32(x, y, &sum, length)
	}
	_ = sum
}

const libopusPitchXcorrInputMagic = "GXCI"

type libopusPitchXcorrCase struct {
	name     string
	x        []float32
	y        []float32
	maxPitch int
}

func buildLibopusPitchXcorrInput(cases []libopusPitchXcorrCase) ([]byte, error) {
	payload := libopustest.NewOraclePayload(libopusPitchXcorrInputMagic, uint32(len(cases)))
	for _, tc := range cases {
		if tc.maxPitch <= 0 || len(tc.x) == 0 || len(tc.y) != len(tc.x)+tc.maxPitch-1 {
			return nil, fmt.Errorf("%s: invalid xcorr input lengths x=%d y=%d maxPitch=%d", tc.name, len(tc.x), len(tc.y), tc.maxPitch)
		}
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x...)
		payload.Float32s(tc.y...)
	}
	return payload.Bytes(), nil
}

func TestPitchXCorrOracleInputFraming(t *testing.T) {
	cases := []libopusPitchXcorrCase{
		{
			name:     "short",
			x:        []float32{1.25, math.Float32frombits(0x80000000)},
			y:        []float32{math.Float32frombits(0x7fc01234), -0.5},
			maxPitch: 1,
		},
		{
			name:     "longer",
			x:        []float32{math.Float32frombits(0xffc05678), 0.25, float32(math.Inf(1))},
			y:        []float32{math.SmallestNonzeroFloat32, -1, 2, math.Float32frombits(0x00000002), 0},
			maxPitch: 3,
		},
	}

	data, err := buildLibopusPitchXcorrInput(cases)
	if err != nil {
		t.Fatalf("build xcorr oracle input: %v", err)
	}
	if len(data) < 12 {
		t.Fatalf("xcorr oracle input length=%d, want header and two records", len(data))
	}
	if got := string(data[:4]); got != libopusPitchXcorrInputMagic {
		t.Fatalf("input magic=%q want %q", got, libopusPitchXcorrInputMagic)
	}
	if got := binary.LittleEndian.Uint32(data[4:8]); got != 1 {
		t.Fatalf("input version=%d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(data[8:12]); got != uint32(len(cases)) {
		t.Fatalf("input record count=%d want %d", got, len(cases))
	}

	off := 12
	readU32 := func(label string) uint32 {
		t.Helper()
		if off+4 > len(data) {
			t.Fatalf("truncated %s at byte %d", label, off)
			return 0
		}
		got := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4
		return got
	}
	readFloats := func(label string, want []float32) {
		t.Helper()
		for i, value := range want {
			got := readU32(fmt.Sprintf("%s[%d]", label, i))
			if expected := math.Float32bits(value); got != expected {
				t.Fatalf("%s[%d] bits=%08x want %08x", label, i, got, expected)
			}
		}
	}
	for _, tc := range cases {
		if got := readU32(tc.name + " x length"); got != uint32(len(tc.x)) {
			t.Fatalf("%s x length=%d want %d", tc.name, got, len(tc.x))
		}
		if got := readU32(tc.name + " max pitch"); got != uint32(tc.maxPitch) {
			t.Fatalf("%s max pitch=%d want %d", tc.name, got, tc.maxPitch)
		}
		readFloats(tc.name+" x", tc.x)
		readFloats(tc.name+" y", tc.y)
	}
	if off != len(data) {
		t.Fatalf("input framing consumed %d of %d bytes; %d trailing", off, len(data), len(data)-off)
	}
}
