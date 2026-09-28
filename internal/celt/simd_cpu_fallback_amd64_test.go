//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"testing"

	"simd/archsimd"
)

var celtCPUFeatureSink float32

// TestCELTCPUFeatureFallbackMath is selected under Penryn emulation. It
// exercises the production dispatchers on a CPU with no AVX and compares
// their outputs with the scalar kernels without calling the C oracle.
func TestCELTCPUFeatureFallbackMath(t *testing.T) {
	if archsimd.X86.AVX() {
		t.Skip("requires a CPU without AVX")
	}

	values := make([]float32, 19)
	for i := range values {
		values[i] = float32((i*17)%29-14) / 7
	}
	gotHi, gotLo := rawMaxMinScan(values, 0, 0)
	wantHi, wantLo := rawMaxMinScanScalar(values, 0, 0)
	if math.Float32bits(gotHi) != math.Float32bits(wantHi) || math.Float32bits(gotLo) != math.Float32bits(wantLo) {
		t.Fatalf("extrema got (%08x,%08x), want (%08x,%08x)", math.Float32bits(gotHi), math.Float32bits(gotLo), math.Float32bits(wantHi), math.Float32bits(wantLo))
	}
	// Initial NaNs must retain the scalar MAX16/MIN16 unordered-compare rule.
	for _, initial := range []float32{float32(math.NaN()), 0} {
		gotHi, gotLo = rawMaxMinScan(values, initial, initial)
		wantHi, wantLo = rawMaxMinScanScalar(values, initial, initial)
		if math.Float32bits(gotHi) != math.Float32bits(wantHi) || math.Float32bits(gotLo) != math.Float32bits(wantLo) {
			t.Fatalf("initial %08x extrema got (%08x,%08x), want (%08x,%08x)", math.Float32bits(initial), math.Float32bits(gotHi), math.Float32bits(gotLo), math.Float32bits(wantHi), math.Float32bits(wantLo))
		}
	}

	pcm := values[:17]
	gotPreemph, wantPreemph := make([]float32, len(pcm)), make([]float32, len(pcm))
	state := [2]float32{0.25, -0.5}
	gotState := preemphInterleaved(pcm, gotPreemph, len(pcm), 1, float32(PreemphCoef), state)
	wantState := preemphInterleavedScalar(pcm, wantPreemph, len(pcm), 1, float32(PreemphCoef), state)
	assertCELTFloat32SlicesEqual(t, "preemphasis", gotPreemph, wantPreemph)
	if gotState != wantState {
		t.Fatalf("preemphasis state got %v, want %v", gotState, wantState)
	}

	rotation, rotationWant := make([]celtNorm, 23), make([]celtNorm, 23)
	for i := range rotation {
		rotation[i] = celtNorm(values[i%len(values)])
	}
	copy(rotationWant, rotation)
	expRotation1StrideSIMD(rotation, len(rotation), 4, opusVal16(0.8125), opusVal16(-0.375))
	expRotation1NormScalar(rotationWant, len(rotationWant), 4, opusVal16(0.8125), opusVal16(-0.375))
	assertCELTFloat32SlicesEqual(t, "exp rotation", rotation, rotationWant)

	left, right := append([]celtNorm(nil), values...), append([]celtNorm(nil), values...)
	wantLeft, wantRight := append([]celtNorm(nil), left...), append([]celtNorm(nil), right...)
	stereoSplitInto(left, right)
	stereoSplitScalar(wantLeft, wantRight)
	assertCELTFloat32SlicesEqual(t, "stereo split left", left, wantLeft)
	assertCELTFloat32SlicesEqual(t, "stereo split right", right, wantRight)

	mergeLeft, mergeRight := append([]float32(nil), values[:16]...), append([]float32(nil), values[1:17]...)
	wantMergeLeft, wantMergeRight := append([]float32(nil), mergeLeft...), append([]float32(nil), mergeRight...)
	const mid, leftGain, rightGain = float32(0.75), float32(0.625), float32(0.875)
	stereoMergeRescaleNEON(mergeLeft, mergeRight, mid, leftGain, rightGain)
	for i := range wantMergeLeft {
		l := noFMA32Mul(mid, wantMergeLeft[i])
		r := wantMergeRight[i]
		wantMergeLeft[i] = noFMA32Mul(leftGain, noFMA32Sub(l, r))
		wantMergeRight[i] = noFMA32Mul(rightGain, noFMA32Add(l, r))
	}
	assertCELTFloat32SlicesEqual(t, "stereo merge left", mergeLeft, wantMergeLeft)
	assertCELTFloat32SlicesEqual(t, "stereo merge right", mergeRight, wantMergeRight)

	const combN = 8
	combSrc, combWant, combDelay := append([]float32(nil), values[:combN]...), append([]float32(nil), values[:combN]...), make([]float32, combN+4)
	for i := range combDelay {
		combDelay[i] = values[(i+2)%len(values)]
	}
	combFilterConstSSE(combWant, combSrc, combDelay, 0, combN, 0.125, -0.0625, 0.03125)
	for i := range combSrc {
		want := combFilterConstSSEValue(combSrc[i], 0.125, -0.0625, 0.03125, combDelay[i+2], combDelay[i+3], combDelay[i+1], combDelay[i+4], combDelay[i])
		if math.Float32bits(combWant[i]) != math.Float32bits(want) {
			t.Fatalf("constant comb %d got %08x, want %08x", i, math.Float32bits(combWant[i]), math.Float32bits(want))
		}
	}
	combOverlapWant, combD0, combD1, combWindow := append([]float32(nil), combSrc...), make([]float32, combN+4), make([]float32, combN+4), make([]float32, combN)
	for i := range combD0 {
		combD0[i], combD1[i] = values[i%len(values)], values[(i+5)%len(values)]
	}
	for i := range combWindow {
		combWindow[i] = float32(i+1) / 10
	}
	combOverlapGot := append([]float32(nil), combOverlapWant...)
	combFilterOverlapScalar(combOverlapWant, combD0, combD1, combWindow, 0.125, -0.0625, 0.03125, 0.25, -0.125, 0.0625)
	combFilterOverlap(combOverlapGot, combD0, combD1, combWindow, 0.125, -0.0625, 0.03125, 0.25, -0.125, 0.0625)
	assertCELTFloat32SlicesEqual(t, "crossfade comb", combOverlapGot, combOverlapWant)

	a, b, c, d := values[:16], values, values, values
	gotA, gotB := absSumPair(a, b)
	if gotA != absSumSerial(a) || gotB != absSumSerial(b[:len(a)]) {
		t.Fatalf("pair abs sum got (%08x,%08x), want (%08x,%08x)", math.Float32bits(gotA), math.Float32bits(gotB), math.Float32bits(absSumSerial(a)), math.Float32bits(absSumSerial(b[:len(a)])))
	}
	gotA, gotB, gotC, gotD := absSumQuad(a, b, c, d)
	for i, got := range []float32{gotA, gotB, gotC, gotD} {
		want := absSumSerial([][]float32{a, b, c, d}[i][:len(a)])
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("quad abs sum %d got %08x, want %08x", i, math.Float32bits(got), math.Float32bits(want))
		}
	}

	for _, stride := range []int{1, 2, 4} {
		const n0 = 9
		x := make([]celtNorm, 2*stride*n0)
		for i := range x {
			x[i] = celtNorm(values[i%len(values)])
		}
		want := append([]celtNorm(nil), x...)
		switch stride {
		case 1:
			haar1Stride1(x, n0)
		case 2:
			haar1Stride2(x, n0)
		case 4:
			haar1Stride4(x, n0)
		}
		haar1ReferenceNorm(want, 2*n0, stride)
		assertCELTFloat32SlicesEqual(t, "haar", x, want)
	}

	pulses := make([]int32, 19)
	for i := range pulses {
		pulses[i] = int32((i*43)%37 - 18)
	}
	gotPulses, wantPulses := make([]celtNorm, len(pulses)), make([]celtNorm, len(pulses))
	scalePulsesInto(gotPulses, pulses, 0.375)
	scalePulsesIntoScalar(wantPulses, pulses, 0.375)
	assertCELTFloat32SlicesEqual(t, "pulse scale", gotPulses, wantPulses)
	scaleGot, scaleWant := make([]float32, len(values)), make([]float32, len(values))
	scaleFloat32Into(scaleGot, values, 0.6875)
	scaleFloat32IntoRef(scaleWant, values, 0.6875)
	assertCELTFloat32SlicesEqual(t, "scale into", scaleGot, scaleWant)
	got0, got1, got2 := spreadCountThresholds(gotPulses, len(gotPulses), 0.375)
	want0, want1, want2 := spreadCountThresholdsScalar(gotPulses, 0.375)
	if got0 != want0 || got1 != want1 || got2 != want2 {
		t.Fatalf("spread counts got (%d,%d,%d), want (%d,%d,%d)", got0, got1, got2, want0, want1, want2)
	}

	if mdctUseSSEForward {
		t.Fatal("MDCT AVX dispatch enabled on a CPU without AVX")
	}
	mdctInput := make([]float32, 40)
	for i := range mdctInput {
		mdctInput[i] = float32((i*11)%23-11) / 13
	}
	mdctOut, mdctAgain := make([]float32, 32), make([]float32, 32)
	var mdctScratch MDCTForwardScratch
	mdctScratch.ForwardWithOverlapFloat32Into(mdctInput, 8, mdctOut)
	mdctScratch.ForwardWithOverlapFloat32Into(mdctInput, 8, mdctAgain)
	assertCELTFloat32SlicesEqual(t, "MDCT", mdctOut, mdctAgain)
	for i, v := range mdctOut {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("MDCT output %d is not finite: %v", i, v)
		}
	}

	// Warm the selected no-AVX wrappers and MDCT scratch before measuring the
	// steady-state fallback path.
	mdctScratch.ForwardWithOverlapFloat32Into(mdctInput, 8, mdctOut)
	allocs := testing.AllocsPerRun(20, func() {
		hi, lo := rawMaxMinScan(values, 0, 0)
		state := preemphInterleaved(pcm, gotPreemph, len(pcm), 1, float32(PreemphCoef), state)
		expRotation1Norm(rotation, len(rotation), 4, opusVal16(0.8125), opusVal16(-0.375))
		stereoSplitInto(left, right)
		s0, s1 := absSumPair(a, b)
		haar1Stride4(gotPulses[:16], 2)
		scalePulsesInto(gotPulses, pulses, 0.375)
		scaleFloat32Into(scaleGot, values, 0.6875)
		stereoMergeRescaleNEON(mergeLeft, mergeRight, mid, leftGain, rightGain)
		combFilterConstSSE(combWant, combSrc, combDelay, 0, combN, 0.125, -0.0625, 0.03125)
		combFilterOverlap(combOverlapGot, combD0, combD1, combWindow, 0.125, -0.0625, 0.03125, 0.25, -0.125, 0.0625)
		t0, t1, t2 := spreadCountThresholds(gotPulses, len(gotPulses), 0.375)
		mdctScratch.ForwardWithOverlapFloat32Into(mdctInput, 8, mdctOut)
		celtCPUFeatureSink = hi + lo + state[0] + state[1] + s0 + s1 + float32(t0+t1+t2) + mdctOut[0] + scaleGot[0] + mergeLeft[0] + combWant[0] + combOverlapGot[0]
	})
	if allocs != 0 {
		t.Fatalf("no-AVX selected kernels allocated %v times", allocs)
	}
}

// TestCELTAVXSafeFloatAbsNeg checks sign-bit operations on AVX-only CPUs. In
// particular, this selector runs on Sandy Bridge without requiring AVX2.
func TestCELTAVXSafeFloatAbsNeg(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("requires AVX")
	}
	checkCELTAVXSafeFloatAbsNeg(t)
}

//go:noinline
func checkCELTAVXSafeFloatAbsNeg(t *testing.T) {
	t.Helper()
	inputBits := [4]uint32{0x80000000, 0x7f800001, 0xffc12345, 0xff800000}
	input := [4]float32{}
	for i, bits := range inputBits {
		input[i] = math.Float32frombits(bits)
	}
	var absOut, negOut [4]float32
	v := archsimd.LoadFloat32x4Array(&input)
	absF32x4AVX(v).StoreArray(&absOut)
	negF32x4AVX(v).StoreArray(&negOut)
	for i, bits := range inputBits {
		if got, want := math.Float32bits(absOut[i]), bits&0x7fffffff; got != want {
			t.Fatalf("abs lane %d got %08x, want %08x", i, got, want)
		}
		if got, want := math.Float32bits(negOut[i]), bits^0x80000000; got != want {
			t.Fatalf("neg lane %d got %08x, want %08x", i, got, want)
		}
	}
}

func TestCELTRawMaxMinInitialNaNMatchesSequential(t *testing.T) {
	x := []float32{-4, 3, -2, 8, 1, -9, 6, 5, 0.25, -0.5, 11, -12}
	for _, tc := range []struct {
		maxVal float32
		minVal float32
	}{
		{float32(math.NaN()), 0},
		{0, float32(math.NaN())},
		{float32(math.NaN()), float32(math.NaN())},
	} {
		gotMax, gotMin := rawMaxMinScan(x, tc.maxVal, tc.minVal)
		wantMax, wantMin := rawMaxMinScanScalar(x, tc.maxVal, tc.minVal)
		if math.Float32bits(gotMax) != math.Float32bits(wantMax) || math.Float32bits(gotMin) != math.Float32bits(wantMin) {
			t.Fatalf("initial (%08x,%08x): got (%08x,%08x), want (%08x,%08x)",
				math.Float32bits(tc.maxVal), math.Float32bits(tc.minVal), math.Float32bits(gotMax), math.Float32bits(gotMin), math.Float32bits(wantMax), math.Float32bits(wantMin))
		}
	}
}

// TestCELTAVX2XCorrFallbackMath runs on Penryn and AVX-only CPUs. It verifies
// that the eight-lane CELT entry points select their scalar path when AVX2+FMA
// is unavailable; it never forces a vector path on an unsupported CPU.
func TestCELTAVX2XCorrFallbackMath(t *testing.T) {
	if libopusFloatPitchXCorrUsesAVX2FMA() {
		t.Skip("requires AVX2+FMA to be unavailable")
	}
	x := []float32{0.125, -0.5, 0.75, -1.25, 0.0625, 0.25, -0.375, 1.5, -0.875, 0.4375}
	y := make([]float32, len(x)+15)
	for i := range y {
		y[i] = float32((i*19)%31-15) / 17
	}
	var got, want [8]float32
	xcorrKernelAVX8(&x[0], &y[0], &got, len(x))
	xcorrKernelAVX8ScalarGo(&x[0], &y[0], &want, len(x))
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("xcorr lane %d got %08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	got, want = [8]float32{}, [8]float32{}
	xcorrKernelAVX8OnePass(&x[0], &y[0], &got, len(x))
	xcorrKernelAVX8ScalarGo(&x[0], &y[0], &want, len(x))
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("one-pass xcorr lane %d got %08x, want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	tinyX, tinyY := x[:5], y[:12]
	gotTiny, wantTiny := make([]float32, 8), make([]float32, 8)
	pitchXCorrFloat32AVX2FMAOrderTiny(tinyX, tinyY, gotTiny, len(tinyX), len(gotTiny))
	pitchXCorrFloat32AVX2FMAOrderTinyScalar(tinyX, tinyY, wantTiny, len(tinyX), len(wantTiny))
	assertCELTFloat32SlicesEqual(t, "tiny xcorr", gotTiny, wantTiny)
}

func assertCELTFloat32SlicesEqual(t *testing.T, name string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length got %d, want %d", name, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d] got %08x, want %08x", name, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}
