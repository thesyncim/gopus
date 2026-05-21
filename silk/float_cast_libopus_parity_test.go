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

func itoaFloatScale(scale float32) string {
	switch scale {
	case 1:
		return "1"
	case float32(silkSampleScale):
		return "32768"
	case float32(silkSampleScale / 2):
		return "16384"
	default:
		return "custom"
	}
}
