//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func libopusFloat2Int16ForEncoderTest(t *testing.T, samples []float32) []int16 {
	t.Helper()
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		t.Skipf("libopus float quant helper unavailable: %v", err)
	}
	return want
}

func encoderFloatQuantGrid() []float32 {
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw)*(1.0/32768.0))
		samples = append(samples, float32(float64(raw)+0.5)*(1.0/32768.0))
	}
	return samples
}

func TestEncoderFloat32ToInt16LibopusMatchesCGrid(t *testing.T) {
	samples := encoderFloatQuantGrid()
	want := libopusFloat2Int16ForEncoderTest(t, samples)
	for i, sample := range samples {
		if got := int16(float32ToInt16Libopus(sample)); got != want[i] {
			t.Fatalf("float32ToInt16Libopus(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestQuantizeFloat32ToInt16LibopusInPlaceMatchesCGrid(t *testing.T) {
	samples := encoderFloatQuantGrid()
	want := libopusFloat2Int16ForEncoderTest(t, samples)
	got := append([]float32(nil), samples...)
	quantizeFloat32ToInt16LibopusInPlace(got)
	for i := range got {
		wantSample := float32(want[i]) * (1.0 / 32768.0)
		if got[i] != wantSample {
			t.Fatalf("quantized[%d]=%0.10g want %0.10g", i, got[i], wantSample)
		}
	}
}

func TestDownmixStereoToSilkMonoLibopusMatchesCQuantization(t *testing.T) {
	interleaved := []float32{
		1, 1,
		-1, -1,
		float32(0.75), float32(0.75),
		float32(-0.75), float32(-0.75),
		float32(1.5 / 32768.0), 0,
		float32(-1.5 / 32768.0), 0,
		float32(-3.5 / 32768.0), float32(1.0 / 32768.0),
		float32(32767.5 / 32768.0), float32(1.0 / 32768.0),
	}
	sums := make([]float32, len(interleaved)/2)
	for i := range sums {
		sums[i] = interleaved[2*i] + interleaved[2*i+1]
	}
	wantQ0 := libopusFloat2Int16ForEncoderTest(t, sums)

	got := make([]float32, len(sums))
	downmixStereoToSilkMonoLibopus(got, interleaved, len(got))
	for i := range got {
		want := float32(silkRShiftRound1(int32(wantQ0[i]))) * (1.0 / 32768.0)
		if got[i] != want {
			t.Fatalf("downmix[%d]=%0.10g want %0.10g", i, got[i], want)
		}
	}
}

func TestAverageSilkResamplerOutputsLibopusMatchesCQuantization(t *testing.T) {
	left := []float32{
		1,
		-1,
		float32(1.5 / 32768.0),
		float32(-1.5 / 32768.0),
		float32(32767.5 / 32768.0),
		float32(-32767.5 / 32768.0),
	}
	right := []float32{
		1,
		-1,
		float32(0.5 / 32768.0),
		float32(-0.5 / 32768.0),
		float32(1.0 / 32768.0),
		float32(-1.0 / 32768.0),
	}
	samples := append(append([]float32(nil), left...), right...)
	wantQ0 := libopusFloat2Int16ForEncoderTest(t, samples)

	got := append([]float32(nil), left...)
	averageSilkResamplerOutputsLibopus(got, right, len(got))
	for i := range got {
		want := float32((int32(wantQ0[i])+int32(wantQ0[len(left)+i]))>>1) * (1.0 / 32768.0)
		if got[i] != want {
			t.Fatalf("average[%d]=%0.10g want %0.10g", i, got[i], want)
		}
	}
}

func TestFloatQuantFormulaMatchesC(t *testing.T) {
	samples := encoderFloatQuantGrid()
	want := libopusFloat2Int16ForEncoderTest(t, samples)
	for i, sample := range samples {
		if got := opusFloatToInt16Formula(sample); got != want[i] {
			t.Fatalf("formula(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func opusFloatToInt16Formula(x float32) int16 {
	y := x * 32768.0
	if y > 32767.0 {
		return 32767
	}
	if y < -32768.0 {
		return -32768
	}
	i := int32(y)
	frac := y - float32(i)
	if frac > 0.5 || (frac == 0.5 && (i&1) != 0) {
		i++
	} else if frac < -0.5 || (frac == -0.5 && (i&1) != 0) {
		i--
	}
	return int16(i)
}
