package opusmath

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFloat32ToInt16RawMatchesLibopusSILKFloat2Short(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := rawInt16OracleSamples()
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeSILKFloat2Short, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk float2short", err)
	}
	for i, sample := range samples {
		if got := Float32ToInt16Raw(sample); got != want[i] {
			t.Fatalf("Float32ToInt16Raw(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestFloat32ToInt16MatchesLibopusFloat2Int16(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := pcmUnitOracleSamples()
	want, err := libopustest.ProbeFloatQuant(libopustest.FloatQuantModeFloat2Int16, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "FLOAT2INT16", err)
	}
	for i, sample := range samples {
		if got := Float32ToInt16(sample); got != want[i] {
			t.Fatalf("Float32ToInt16(%0.10g)=%d want %d", sample, got, want[i])
		}
	}
}

func TestFloat32ToInt24MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const scale = float32(1 << 23)
	samples := []float32{
		math.Float32frombits(0xff800000), // -Inf
		math.Float32frombits(0x7f800000), // +Inf
		math.Float32frombits(0x7fc12345), // quiet NaN
		math.Float32frombits(0x7f812345), // signaling NaN payload
		math.Nextafter32(256, 0),
		256,
		math.Nextafter32(256, float32(math.Inf(1))),
		math.Nextafter32(-256, 0),
		-256,
		math.Nextafter32(-256, float32(math.Inf(-1))),
		1e30,
		-1e30,
	}
	for _, raw := range []float32{-2.5, -1.5, -0.5, 0.5, 1.5, 2.5, 3.5} {
		samples = append(samples, raw/scale)
	}
	want, err := libopustest.ProbeFloat2Int24(samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "float2int24", err)
	}
	for i, sample := range samples {
		if got := Float32ToInt24(sample); got != want[i] {
			t.Fatalf("Float32ToInt24(%08x, %0.10g)=%d want C float2int=%d", math.Float32bits(sample), sample, got, want[i])
		}
	}
}

func rawInt16OracleSamples() []float32 {
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
	for raw := -2048; raw <= 2048; raw++ {
		base := float32(raw)
		samples = append(samples, base-0.5, base, base+0.5)
	}
	return samples
}

func pcmUnitOracleSamples() []float32 {
	samples := make([]float32, 0, 2*65540)
	for raw := -32770; raw <= 32769; raw++ {
		samples = append(samples, float32(raw)*(1.0/32768.0))
		samples = append(samples, float32(float64(raw)+0.5)*(1.0/32768.0))
	}
	return samples
}
