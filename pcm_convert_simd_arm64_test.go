//go:build arm64 && goexperiment.simd && !nosimd

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPCMInt16SIMDExceptionalInputsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	samples := []float32{
		math.Float32frombits(0x7fc12345),
		math.Float32frombits(0xffc54321),
		math.Float32frombits(0x7fa12345),
		float32(math.Inf(1)),
		float32(math.Inf(-1)),
		math.Nextafter32(1, float32(math.Inf(1))),
		math.Nextafter32(-1, float32(math.Inf(-1))),
		math.Float32frombits(0x7f7fffff),
		math.Float32frombits(0xff7fffff),
		-1,
		1,
		math.Float32frombits(0x00000001),
		math.Float32frombits(0x80000001),
		float32(-2.0 / 32768.0),
		float32(-1.0 / 32768.0),
		float32(1.0 / 32768.0),
		float32(2.0 / 32768.0),
		0,
		math.Float32frombits(0x80000000),
		float32(32767.0 / 32768.0),
	}
	want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, samples)
	if err != nil {
		libopustest.HelperUnavailable(t, "exceptional PCM conversion", err)
	}
	got := make([]int16, len(samples))
	float32ToInt16NoSoftClip(got, samples, len(samples), 1)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d] bits=%08x: got %d, libopus got %d", i, math.Float32bits(samples[i]), got[i], want[i])
		}
	}
}

func TestPCMInt16SIMDUnalignedSlicesStayWithinBounds(t *testing.T) {
	const n = 32
	srcStorage := make([]float32, n+1)
	dstStorage := make([]int16, n+2)
	src := srcStorage[1:]
	dst := dstStorage[1 : n+1]
	for i := range src {
		src[i] = float32((i*37)%191-95) / 128
	}
	dstStorage[0], dstStorage[n+1] = 1234, -1234
	if !convertFloat32ToInt16Unit(dst, src, n) {
		t.Fatal("unit conversion rejected in-range offset slices")
	}
	assertPCMInt16SIMDMatchesScalar(t, dst, src)
	if dstStorage[0] != 1234 || dstStorage[n+1] != -1234 {
		t.Fatalf("unit conversion overwrote guards: %d, %d", dstStorage[0], dstStorage[n+1])
	}

	for i := range src {
		src[i] = float32((i*53)%511-255) / 128
	}
	convertFloat32ToInt16NoSoftClipUnit(dst, src, n)
	assertPCMInt16SIMDMatchesScalar(t, dst, src)
	if dstStorage[0] != 1234 || dstStorage[n+1] != -1234 {
		t.Fatalf("saturating conversion overwrote guards: %d, %d", dstStorage[0], dstStorage[n+1])
	}
}

func TestPCMInt16SIMDConversionsAllocateZero(t *testing.T) {
	const n = 480
	src := make([]float32, n)
	dst := make([]int16, n)
	for i := range src {
		src[i] = float32((i*37)%191-95) / 128
	}
	if !convertFloat32ToInt16Unit(dst, src, n) {
		t.Fatal("unit conversion rejected in-range input")
	}
	allocs := testing.AllocsPerRun(100, func() {
		if !convertFloat32ToInt16Unit(dst, src, n) {
			panic("unit conversion rejected in-range input")
		}
		pcmInt16SIMDAllocSink = dst[n-1]
	})
	if allocs != 0 {
		t.Fatalf("unit conversion allocated %g times per run", allocs)
	}

	for i := range src {
		src[i] = float32((i*53)%511-255) / 128
	}
	convertFloat32ToInt16NoSoftClipUnit(dst, src, n)
	allocs = testing.AllocsPerRun(100, func() {
		convertFloat32ToInt16NoSoftClipUnit(dst, src, n)
		pcmInt16SIMDAllocSink = dst[n-1]
	})
	if allocs != 0 {
		t.Fatalf("saturating conversion allocated %g times per run", allocs)
	}
}

var pcmInt16SIMDAllocSink int16

func assertPCMInt16SIMDMatchesScalar(t *testing.T, got []int16, src []float32) {
	t.Helper()
	for i := range src {
		if want := float32ToInt16(src[i]); got[i] != want {
			t.Fatalf("sample[%d]=%g: got %d, scalar wants %d", i, src[i], got[i], want)
		}
	}
}
