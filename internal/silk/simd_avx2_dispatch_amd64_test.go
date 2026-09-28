//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"math"
	"simd/archsimd"
	"slices"
	"testing"
)

// TestSILKAMD64SIMDRequiresAVX2 exercises the public kernel dispatch with
// AVX2 disabled. It also runs on AVX-only and pre-AVX emulated CPUs, where
// the feature variables naturally select the scalar paths.
func TestSILKAMD64SIMDRequiresAVX2(t *testing.T) {
	if !archsimd.X86.AVX2() {
		for name, enabled := range map[string]bool{
			"rewhiten":               silkRewhitenLTPUsesAVX2,
			"LPC float":              silkLPCAnalysisF32UsesAVX2,
			"warped autocorrelation": silkWarpedAutocorrUsesAVX2,
			"pitch xcorr":            silkUsePitchXcorrAVX2FMA,
			"inner product":          silkUseInnerProductFLPAVX2FMA,
			"delayed decision NSQ":   silkNSQDelDecUsesAVX2,
			"up2 HQ":                 up2HQUsesAVX2,
			"LPC int16":              silkLPCAnalysisFilterUsesAVX2,
			"stereo":                 silkStereoUsesAVX2,
			"resampler FIR":          firInterpolUsesAVX2,
			"int16 to float32":       int16ToFloat32UsesAVX2,
		} {
			if enabled {
				t.Errorf("%s enables a 256-bit SIMD path without AVX2", name)
			}
		}
	}

	t.Run("rewhiten", func(t *testing.T) {
		old := silkRewhitenLTPUsesAVX2
		silkRewhitenLTPUsesAVX2 = false
		defer func() { silkRewhitenLTPUsesAVX2 = old }()
		xq := make([]int16, 512)
		for i := range xq {
			xq[i] = int16(i*37 - 8000)
		}
		coef := make([]int16, maxLPCOrder)
		for i := range coef {
			coef[i] = int16(i*19 - 130)
		}
		want, got := make([]int16, 512), make([]int16, 512)
		const start, offset, length = 100, 80, 220
		rewhitenLTPScalar(want, xq, start, offset, coef, maxLPCOrder, length, maxLPCOrder)
		rewhitenLTP(got, xq, start, offset, coef, length, maxLPCOrder)
		if !slices.Equal(got, want) {
			t.Fatal("rewhiten scalar dispatch differs from scalar kernel")
		}
	})

	t.Run("LPC float", func(t *testing.T) {
		old := silkLPCAnalysisF32UsesAVX2
		silkLPCAnalysisF32UsesAVX2 = false
		defer func() { silkLPCAnalysisF32UsesAVX2 = old }()
		in := make([]float32, 64)
		for i := range in {
			in[i] = float32(i%13-6) * 0.125
		}
		coef := make([]float32, maxLPCOrder)
		for i := range coef {
			coef[i] = float32(i-8) * 0.03125
		}
		want, got := make([]float32, len(in)), make([]float32, len(in))
		lpcAnalysisFilterF32Scalar(want, coef, in, len(in), maxLPCOrder)
		lpcAnalysisFilterF32(got, coef, in, len(in), maxLPCOrder)
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("LPC float sample %d: got %08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
	})

	t.Run("warped autocorrelation", func(t *testing.T) {
		old := silkWarpedAutocorrUsesAVX2
		silkWarpedAutocorrUsesAVX2 = false
		defer func() { silkWarpedAutocorrUsesAVX2 = old }()
		in := make([]float32, 32)
		for i := range in {
			in[i] = float32(i%9-4) * 0.125
		}
		var gotSt, gotCorr, wantSt, wantCorr warpedAutocorrState
		warpedAutocorrelationSamples(&wantSt, &wantCorr, in, 0.02, maxShapeLpcOrder)
		warpedAutocorrelationSections(&gotSt, &gotCorr, in, 0.02, maxShapeLpcOrder)
		if gotSt != wantSt || gotCorr != wantCorr {
			t.Fatal("warped autocorrelation scalar dispatch differs from scalar kernel")
		}
	})

	t.Run("pitch xcorr", func(t *testing.T) {
		old := silkUsePitchXcorrAVX2FMA
		silkUsePitchXcorrAVX2FMA = false
		defer func() { silkUsePitchXcorrAVX2FMA = old }()
		var x [32]float32
		var y [39]float32
		for i := range x {
			x[i] = float32(i%11-5) * 0.125
		}
		for i := range y {
			y[i] = float32(i%7-3) * 0.25
		}
		var want, got [8]float32
		xcorrKernelAVX8ScalarGo(&x[0], &y[0], &want, len(x))
		xcorrKernelAVX8OnePass(&x[0], &y[0], &got, len(x))
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("pitch xcorr %d: got %08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
	})

	t.Run("inner product", func(t *testing.T) {
		old := silkUseInnerProductFLPAVX2FMA
		silkUseInnerProductFLPAVX2FMA = false
		defer func() { silkUseInnerProductFLPAVX2FMA = old }()
		a, b := make([]float32, 32), make([]float32, 32)
		for i := range a {
			a[i] = float32(i%11-5) * 0.125
			b[i] = float32(i%7-3) * 0.25
		}
		got := innerProductFLPAVX2(a, b, len(a))
		want := innerProductF32Libopus(a, b, len(a))
		if math.Float64bits(float64(got)) != math.Float64bits(float64(want)) {
			t.Fatalf("inner product: got %v want %v", got, want)
		}
	})

	t.Run("up2 HQ", func(t *testing.T) {
		old := up2HQUsesAVX2
		up2HQUsesAVX2 = false
		defer func() { up2HQUsesAVX2 = old }()
		in := make([]int16, 24)
		for i := range in {
			in[i] = int16(i*37 - 400)
		}
		want, got := make([]int16, 2*len(in)), make([]int16, 2*len(in))
		wantState, gotState := [6]int32{43, -17, 9, -5, 37, -11}, [6]int32{43, -17, 9, -5, 37, -11}
		up2HQCoreGo(want, in, &wantState)
		up2HQCore(got, in, &gotState)
		if !slices.Equal(got, want) || gotState != wantState {
			t.Fatal("up2 HQ scalar dispatch differs from scalar kernel")
		}
	})

	t.Run("LPC int16", func(t *testing.T) {
		old := silkLPCAnalysisFilterUsesAVX2
		silkLPCAnalysisFilterUsesAVX2 = false
		defer func() { silkLPCAnalysisFilterUsesAVX2 = old }()
		in, out := make([]int16, 64), make([]int16, 64)
		coef := make([]int16, maxLPCOrder)
		for i := range in {
			in[i] = int16(i*31 - 500)
		}
		for i := range coef {
			coef[i] = int16(i*11 - 100)
		}
		if got := silkLPCAnalysisFilterVec(out, in, coef, len(in), maxLPCOrder); got != maxLPCOrder {
			t.Fatalf("LPC vector prefix=%d with AVX2 disabled", got)
		}
	})

	t.Run("stereo", func(t *testing.T) {
		old := silkStereoUsesAVX2
		silkStereoUsesAVX2 = false
		defer func() { silkStereoUsesAVX2 = old }()
		mid, side := make([]int16, 66), make([]int16, 66)
		for i := range mid {
			mid[i] = int16(i*17 - 500)
			side[i] = int16(i*23 - 700)
		}
		wantMid, wantSide := slices.Clone(mid), slices.Clone(side)
		stereoPredictSideScalar(wantMid, wantSide, 0, 64, 1234, -567, 11, -7)
		stereoPredictSide(mid, side, 0, 64, 1234, -567, 11, -7)
		if !slices.Equal(mid, wantMid) || !slices.Equal(side, wantSide) {
			t.Fatal("stereo prediction scalar dispatch differs from scalar kernel")
		}
		stereoMidSideToLRScalar(wantMid[:64], wantSide[:64])
		stereoMidSideToLR(mid[:64], side[:64])
		if !slices.Equal(mid, wantMid) || !slices.Equal(side, wantSide) {
			t.Fatal("stereo mixing scalar dispatch differs from scalar kernel")
		}
	})

	t.Run("resampler FIR", func(t *testing.T) {
		old := firInterpolUsesAVX2
		firInterpolUsesAVX2 = false
		defer func() { firInterpolUsesAVX2 = old }()
		dst, buf := make([]int16, 16), make([]int16, 32)
		if got := firInterpolVec(dst, buf, 32768); got != 0 {
			t.Fatalf("FIR vector prefix=%d with AVX2 disabled", got)
		}
	})

	t.Run("int16 to float32", func(t *testing.T) {
		old := int16ToFloat32UsesAVX2
		int16ToFloat32UsesAVX2 = false
		defer func() { int16ToFloat32UsesAVX2 = old }()
		src, dst := make([]int16, 17), make([]float32, 17)
		for i := range src {
			src[i] = int16(i*2000 - 16000)
		}
		writeInt16AsFloat32Core(dst, src, len(src))
		for i := range dst {
			want := float32(src[i]) * (1.0 / 32768.0)
			if math.Float32bits(dst[i]) != math.Float32bits(want) {
				t.Fatalf("int16 to float32 sample %d: got %08x want %08x", i, math.Float32bits(dst[i]), math.Float32bits(want))
			}
		}
	})
}
