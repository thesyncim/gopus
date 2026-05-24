package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSILKFloatToInt16MatchesLibopusFloat2ShortArray(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := silkFloat2ShortOracleSamples()
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeSILKFloat2Short, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk float2short", err)
	}
	for i, sample := range samples {
		if got := floatToInt16Round(sample); got != want[i] {
			t.Fatalf("floatToInt16Round(%0.10g)=%d want %d", sample, got, want[i])
		}
		if got := float64ToInt16Round(float64(sample)); got != want[i] {
			t.Fatalf("float64ToInt16Round(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestSILKFloatToInt16TieGridMatchesLibopusFloat2ShortArray(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		-32769,
		-32768.75,
		-32768.5,
		-32768.25,
		-32768,
		-32767.75,
		-32767.5,
		32766.5,
		32766.75,
		32767,
		32767.25,
		32767.5,
		32768,
	}
	for n := -64; n <= 64; n++ {
		base := float32(n)
		samples = appendFloat32Neighbors(samples, base-0.5)
		samples = appendFloat32Neighbors(samples, base)
		samples = appendFloat32Neighbors(samples, base+0.5)
	}

	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeSILKFloat2Short, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk float2short", err)
	}
	for i, sample := range samples {
		if got := floatToInt16Round(sample); got != want[i] {
			t.Fatalf("floatToInt16Round(sample[%d]=%0.10g)=%d want %d", i, sample, got, want[i])
		}
		if got := float64ToInt16Round(float64(sample)); got != want[i] {
			t.Fatalf("float64ToInt16Round(sample[%d]=%0.10g)=%d want %d", i, sample, got, want[i])
		}
	}
}

func TestSILKFloatToInt16SliceScaledMatchesLibopusFloat2ShortArray(t *testing.T) {
	libopustest.RequireOracle(t)
	input := []float32{
		float32(-1.2),
		float32(-1),
		float32(-32767.5 / silkSampleScale),
		float32(-1235.5 / silkSampleScale),
		float32(-1234.5 / silkSampleScale),
		float32(-3.5 / silkSampleScale),
		float32(-2.5 / silkSampleScale),
		float32(-1.5 / silkSampleScale),
		float32(-0.5 / silkSampleScale),
		0,
		float32(0.5 / silkSampleScale),
		float32(1.5 / silkSampleScale),
		float32(2.5 / silkSampleScale),
		float32(3.5 / silkSampleScale),
		float32(1234.5 / silkSampleScale),
		float32(1235.5 / silkSampleScale),
		float32(32766.5 / silkSampleScale),
		float32(32767.5 / silkSampleScale),
		1,
		float32(1.2),
	}
	for _, scale := range []float32{1, float32(silkSampleScale), float32(silkSampleScale / 2)} {
		t.Run("scale_"+itoaFloatScale(scale), func(t *testing.T) {
			scaled := make([]float32, len(input))
			for i, sample := range input {
				scaled[i] = sample * scale
			}
			want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeSILKFloat2Short, scaled)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk float2short", err)
			}
			got := make([]int16, len(input))
			floatToInt16SliceScaled(got, input, scale)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("scale=%0.10g sample[%d]=%0.10g scaled=%0.10g got=%d want %d",
						scale, i, input[i], scaled[i], got[i], want[i])
				}
			}
		})
	}
}

func TestSILKFloatToInt32ScaledMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, scale := range []float32{1024, 4096, 8192, 16384, 65536, 131072} {
		t.Run("scale_"+itoaFloatScale(scale), func(t *testing.T) {
			samples := silkFloat2IntScaledOracleSamples(scale)
			want, err := libopustest.ProbeFloatQuantScaledInt32(scale, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "silk scaled float2int", err)
			}
			for i, sample := range samples {
				got := float64ToInt32Round(float64(sample * scale))
				if got != want[i] {
					t.Fatalf("scale=%0.10g sample[%d]=%0.10g scaled=%0.10g got=%d want %d",
						scale, i, sample, sample*scale, got, want[i])
				}
			}
		})
	}
}

func TestSILKInt16ToFloat32SliceMatchesLibopusShort2FloatArray(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := silkShort2FloatOracleSamples()
	want, err := libopustest.ProbeSILKShort2Float(samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk short2float", err)
	}
	got := make([]float32, len(samples))
	int16ToFloat32Slice(got, samples)
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample[%d]=%d got %08x %.10g want %08x %.10g",
				i, samples[i],
				math.Float32bits(got[i]), got[i],
				math.Float32bits(want[i]), want[i])
		}
	}
}

func appendFloat32Neighbors(dst []float32, sample float32) []float32 {
	return append(dst,
		math.Nextafter32(sample, float32(math.Inf(-1))),
		sample,
		math.Nextafter32(sample, float32(math.Inf(1))),
	)
}

func silkShort2FloatOracleSamples() []int16 {
	samples := []int16{
		-32768,
		-32767,
		-16384,
		-4097,
		-4096,
		-255,
		-2,
		-1,
		0,
		1,
		2,
		255,
		4095,
		4096,
		16383,
		16384,
		32766,
		32767,
	}
	for v := -1024; v <= 1024; v += 17 {
		samples = append(samples, int16(v))
	}
	return samples
}

func silkFloat2ShortOracleSamples() []float32 {
	return []float32{
		float32(-40000),
		math.Nextafter32(float32(-32768.5), float32(math.Inf(-1))),
		float32(-32768.5),
		math.Nextafter32(float32(-32768.5), float32(math.Inf(1))),
		float32(-32768),
		float32(-32767.5),
		float32(-1235.5),
		float32(-1234.5),
		float32(-3.5),
		float32(-2.5),
		float32(-1.5),
		float32(-0.5),
		0,
		float32(0.5),
		float32(1.5),
		float32(2.5),
		float32(3.5),
		float32(1234.5),
		float32(1235.5),
		float32(32766.5),
		float32(32767),
		math.Nextafter32(float32(32767.5), float32(math.Inf(-1))),
		float32(32767.5),
		math.Nextafter32(float32(32767.5), float32(math.Inf(1))),
		float32(40000),
	}
}

func silkFloat2IntScaledOracleSamples(scale float32) []float32 {
	targets := []float32{
		-131073.5,
		-65536.5,
		-32768.5,
		-1235.5,
		-1234.5,
		-3.5,
		-2.5,
		-1.5,
		-0.5,
		0,
		0.5,
		1.5,
		2.5,
		3.5,
		1234.5,
		1235.5,
		32767.5,
		65535.5,
		131071.5,
	}
	out := make([]float32, 0, len(targets)*3)
	for _, target := range targets {
		sample := target / scale
		out = append(out,
			math.Nextafter32(sample, float32(math.Inf(-1))),
			sample,
			math.Nextafter32(sample, float32(math.Inf(1))),
		)
	}
	return out
}

func itoaFloatScale(scale float32) string {
	switch scale {
	case 1:
		return "1"
	case 1024:
		return "1024"
	case 4096:
		return "4096"
	case 8192:
		return "8192"
	case float32(silkSampleScale):
		return "32768"
	case float32(silkSampleScale / 2):
		return "16384"
	case 65536:
		return "65536"
	case 131072:
		return "131072"
	default:
		return "custom"
	}
}
