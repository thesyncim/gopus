package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"runtime"
	"strconv"
	"testing"
)

func probeLibopusFloatQuant(mode uint32, samples []float32) ([]int16, error) {
	return libopustest.ProbeFloatQuant(mode, samples)
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16ExhaustiveGrid(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := make([]float32, 0, 65536)
	for i := -32768; i <= 32767; i++ {
		samples = append(samples, float32(i)*(1.0/32768.0))
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("float32ToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
		if got := opusmath.Float32ToInt16(sample); got != want[i] {
			raw := i - 32768
			t.Fatalf("opusmath.Float32ToInt16(%d/32768)=%d want %d", raw, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusFLOAT2INT16TiesAndClamps(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		float32(-32769.0 / 32768.0),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		float32(32768.5 / 32768.0),
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	for i, sample := range samples {
		if got := float32ToInt16(sample); got != want[i] {
			t.Fatalf("float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
		if got := opusmath.Float32ToInt16(sample); got != want[i] {
			t.Fatalf("opusmath.Float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusCELTDispatchBlocks(t *testing.T) {
	libopustest.RequireOracle(t)
	lengths := []int{15, 16, 17, 31, 32, 33}
	pattern := []float32{
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.0 / 32768.0),
		-1,
		1,
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	for _, length := range lengths {
		t.Run(fmt.Sprintf("len_%d", length), func(t *testing.T) {
			samples := make([]float32, length)
			for i := range samples {
				samples[i] = pattern[i%len(pattern)]
			}
			want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant dispatch", err)
			}
			got := make([]int16, length)
			src := append([]float32(nil), samples...)
			softClipAndFloat32ToInt16(got, src, length, 1, []float32{0})
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("len=%d sample[%d]=%d want %d input=%0.10g", length, i, got[i], want[i], samples[i])
				}
			}
		})
	}
}

func TestFloat32ToInt16NoSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	pattern := []float32{
		float32(-32767.5 / 32768.0),
		float32(-3.5 / 32768.0),
		float32(-2.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(2.5 / 32768.0),
		float32(3.5 / 32768.0),
		float32(32766.5 / 32768.0),
		float32(32767.0 / 32768.0),
		-1,
		1,
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	for _, tc := range []struct {
		name     string
		n        int
		channels int
	}{
		{name: "tail_15_mono", n: 15, channels: 1},
		{name: "block_16_mono", n: 16, channels: 1},
		{name: "block_tail_17_mono", n: 17, channels: 1},
		{name: "block_tail_31_mono", n: 31, channels: 1},
		{name: "two_blocks_32_mono", n: 32, channels: 1},
		{name: "two_blocks_tail_33_mono", n: 33, channels: 1},
		{name: "interleaved_33_three_channel", n: 11, channels: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total := tc.n * tc.channels
			samples := make([]float32, total)
			for i := range samples {
				samples[i] = pattern[i%len(pattern)]
			}
			mode := libopustest.FloatQuantModeFloat2Int16
			if runtime.GOARCH == "arm64" {
				mode = libopustest.FloatQuantModeCELTDispatch
			}
			want, err := probeLibopusFloatQuant(mode, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}
			got := make([]int16, total)
			src := append([]float32(nil), samples...)
			float32ToInt16NoSoftClip(got, src, tc.n, tc.channels)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%d want %d input=%0.10g", i, got[i], want[i], samples[i])
				}
			}
			for i := range src {
				if src[i] != samples[i] {
					t.Fatalf("src[%d] mutated to %0.10g want %0.10g", i, src[i], samples[i])
				}
			}
		})
	}
}

func TestFloat32ToInt16NoSoftClipOutOfRangeMatchesLibopusDispatch(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.Nextafter32(-1, float32(math.Inf(-1))),
		-1,
		float32(-32767.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(-0.5 / 32768.0),
		0,
		float32(0.5 / 32768.0),
		float32(1.5 / 32768.0),
		float32(32767.5 / 32768.0),
		1,
		math.Nextafter32(1, float32(math.Inf(1))),
		float32(-40000.0 / 32768.0),
		float32(40000.0 / 32768.0),
		float32(-1234.5 / 32768.0),
		float32(1234.5 / 32768.0),
		float32(-1235.5 / 32768.0),
		float32(1235.5 / 32768.0),
	}
	mode := libopustest.FloatQuantModeFloat2Int16
	if runtime.GOARCH == "arm64" {
		mode = libopustest.FloatQuantModeCELTDispatch
	}
	want, err := probeLibopusFloatQuant(mode, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float quant", err)
	}
	got := make([]int16, len(samples))
	src := append([]float32(nil), samples...)
	float32ToInt16NoSoftClip(got, src, len(samples), 1)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d input=%0.10g", i, got[i], want[i], samples[i])
		}
	}
}

func TestConvertFloat32ToInt16Unit(t *testing.T) {
	src := []float32{
		-1, -0.875, -0.75, -0.5,
		-1.5 / 32768, -0.5 / 32768, 0, 0.5 / 32768,
		1.5 / 32768, 0.125, 0.25, 0.5,
		0.75, 0.875, 0.99999, 1,
		-0.25, -0.125, 0.33325, -0.33325,
	}
	dst := make([]int16, len(src))
	ok := convertFloat32ToInt16Unit(dst, src, len(src))
	if runtime.GOARCH != "arm64" {
		if ok {
			t.Fatal("default conversion unexpectedly handled the vector")
		}
		return
	}
	if !ok {
		t.Fatal("arm64 conversion rejected in-range samples")
	}
	for i, v := range src {
		want := float32ToInt16(v)
		if pcmInt16VectorTiesAway && i < len(src)&^15 {
			// celt_float2int16_neon uses FCVTAS for complete 16-sample
			// blocks and FCVTNS for the remainder.
			rounded := int32(math.Round(float64(v * 32768)))
			want = int16(max(-32768, min(32767, rounded)))
		}
		if dst[i] != want {
			t.Fatalf("dst[%d] = %d, want %d", i, dst[i], want)
		}
	}

	outOfRange := append([]float32(nil), src...)
	outOfRange[8] = 1.01
	if convertFloat32ToInt16Unit(make([]int16, len(outOfRange)), outOfRange, len(outOfRange)) {
		t.Fatal("arm64 conversion accepted out-of-range samples")
	}
}

func TestConvertFloat32ToInt16UnitRejectsEveryInvalidVectorLane(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("unit SIMD conversion is arm64-only")
	}
	const n = 16
	valid := make([]float32, n)
	for i := range valid {
		valid[i] = 0.5
	}
	if !convertFloat32ToInt16Unit(make([]int16, n), valid, n) {
		t.Fatal("unit conversion rejected a fully valid vector")
	}

	invalidValues := []struct {
		name  string
		value float32
	}{
		{name: "above-one", value: math.Nextafter32(1, float32(math.Inf(1)))},
		{name: "below-minus-one", value: math.Nextafter32(-1, float32(math.Inf(-1)))},
		{name: "positive-infinity", value: float32(math.Inf(1))},
		{name: "negative-infinity", value: float32(math.Inf(-1))},
		{name: "quiet-nan", value: math.Float32frombits(0x7fc12345)},
		{name: "signaling-nan", value: math.Float32frombits(0x7fa12345)},
	}
	for _, invalid := range invalidValues {
		for lane := 0; lane < n; lane++ {
			t.Run(invalid.name+"/lane_"+strconv.Itoa(lane), func(t *testing.T) {
				src := append([]float32(nil), valid...)
				src[lane] = invalid.value
				if convertFloat32ToInt16Unit(make([]int16, n), src, n) {
					t.Fatalf("accepted %s in lane %d", invalid.name, lane)
				}
			})
		}
	}
}

func TestFloat32ToInt16OpusRoundingFixture(t *testing.T) {
	tests := []struct {
		name string
		in   float32
		want int16
	}{
		{name: "-one", in: -1, want: -32768},
		{name: "negative clamp below minus one", in: math.Nextafter32(-1, float32(math.Inf(-1))), want: -32768},
		{name: "negative clamp just above minus one", in: math.Nextafter32(-1, 0), want: -32768},
		{name: "-1.5/32768 tie to even", in: float32(-1.5 / 32768.0), want: -2},
		{name: "-0.5/32768 tie to even", in: float32(-0.5 / 32768.0), want: 0},
		{name: "zero", in: 0, want: 0},
		{name: "0.5/32768 tie to even", in: float32(0.5 / 32768.0), want: 0},
		{name: "1.5/32768 tie to even", in: float32(1.5 / 32768.0), want: 2},
		{name: "positive max exact", in: float32(32767.0 / 32768.0), want: 32767},
		{name: "positive clamp at one", in: 1, want: 32767},
		{name: "positive clamp above one", in: math.Nextafter32(1, float32(math.Inf(1))), want: 32767},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
			if got := opusmath.Float32ToInt16(tc.in); got != tc.want {
				t.Fatalf("opusmath.Float32ToInt16(%0.10g) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFloat32ToInt16OpusRoundingBitNeighbors(t *testing.T) {
	tests := []struct {
		name string
		base float32
		want [3]int16
	}{
		{name: "-1.5/32768", base: float32(-1.5 / 32768.0), want: [3]int16{-2, -2, -1}},
		{name: "-0.5/32768", base: float32(-0.5 / 32768.0), want: [3]int16{-1, 0, 0}},
		{name: "0.5/32768", base: float32(0.5 / 32768.0), want: [3]int16{0, 0, 1}},
		{name: "1.5/32768", base: float32(1.5 / 32768.0), want: [3]int16{1, 2, 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			samples := []float32{
				math.Nextafter32(tc.base, float32(math.Inf(-1))),
				tc.base,
				math.Nextafter32(tc.base, float32(math.Inf(1))),
			}
			for i, sample := range samples {
				if got := float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
				if got := opusmath.Float32ToInt16(sample); got != tc.want[i] {
					t.Fatalf("opusmath.Float32ToInt16 neighbor %d (%0.10g) = %d, want %d", i, sample, got, tc.want[i])
				}
			}
		})
	}
}
