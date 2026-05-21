package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
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
