package celt

import (
	"math"
	"reflect"
	"testing"
)

func TestCELTAssemblyWrappersMatchReferenceEdges(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 120, 480}
	maxPitches := []int{1, 2, 3, 4, 5, 8, 15, 16, 31, 32}
	for _, n := range lengths {
		for _, maxPitch := range maxPitches {
			for offset := 0; offset < 4; offset++ {
				runCELTAssemblyReferenceCase(t, n, maxPitch, offset, uint64(n*1009+maxPitch*37+offset))
			}
		}
	}
}

func FuzzCELTAssemblyWrappersMatchReference(f *testing.F) {
	for _, seed := range []struct {
		length   uint8
		maxPitch uint8
		offset   uint8
		seed     uint64
	}{
		{1, 1, 0, 1},
		{4, 4, 1, 2},
		{7, 5, 2, 3},
		{16, 8, 3, 4},
		{31, 15, 0, 5},
		{64, 32, 1, 6},
	} {
		f.Add(seed.length, seed.maxPitch, seed.offset, seed.seed)
	}
	f.Fuzz(func(t *testing.T, rawLength, rawMaxPitch, rawOffset uint8, seed uint64) {
		length := int(rawLength%96) + 1
		maxPitch := int(rawMaxPitch%32) + 1
		offset := int(rawOffset % 4)
		runCELTAssemblyReferenceCase(t, length, maxPitch, offset, seed)
	})
}

func runCELTAssemblyReferenceCase(t *testing.T, length, maxPitch, offset int, seed uint64) {
	t.Helper()

	xBuf := make([]float64, offset+length+8)
	yBuf := make([]float64, offset+length+maxPitch+8)
	y2Buf := make([]float64, offset+length+8)
	x := xBuf[offset : offset+length]
	y := yBuf[offset : offset+length+maxPitch]
	y2 := y2Buf[offset : offset+length]
	for i := range x {
		x[i] = asmExactF64(seed, i)
	}
	for i := range y {
		y[i] = asmExactF64(seed^0x9e3779b97f4a7c15, i)
	}
	for i := range y2 {
		y2[i] = asmExactF64(seed^0xbf58476d1ce4e5b9, i)
	}

	gotXcorr := make([]float64, maxPitch)
	wantXcorr := make([]float64, maxPitch)
	celtPitchXcorr(x, y, gotXcorr, length, maxPitch)
	refPitchXcorr(x, y, wantXcorr, length, maxPitch)
	requireASMFloat64BitsEqual(t, "celtPitchXcorr", gotXcorr, wantXcorr)

	gotPrefilter := make([]float64, maxPitch)
	wantPrefilter := make([]float64, maxPitch)
	prefilterPitchXcorr(x, y, gotPrefilter, length, maxPitch)
	asmPrefilterPitchXcorrRef(x, y, wantPrefilter, length, maxPitch)
	requireASMFloat64BitsEqual(t, "prefilterPitchXcorr", gotPrefilter, wantPrefilter)

	gotFastPrefilter := make([]float64, maxPitch)
	prefilterPitchXcorrFast(x, y, gotFastPrefilter, length, maxPitch)
	requireASMFloat64BitsEqual(t, "prefilterPitchXcorrFast", gotFastPrefilter, wantPrefilter)

	gotInner := prefilterInnerProd(x, y[:length], length)
	wantInner := asmPrefilterInnerProdRef(x, y[:length], length)
	if math.Float64bits(gotInner) != math.Float64bits(wantInner) {
		t.Fatalf("prefilterInnerProd mismatch: got %016x want %016x", math.Float64bits(gotInner), math.Float64bits(wantInner))
	}

	got1, got2 := prefilterDualInnerProd(x, y[:length], y2, length)
	want1, want2 := prefilterDualInnerProdRef(x, y[:length], y2, length)
	if math.Float64bits(got1) != math.Float64bits(want1) || math.Float64bits(got2) != math.Float64bits(want2) {
		t.Fatalf("prefilterDualInnerProd mismatch: got (%016x,%016x) want (%016x,%016x)",
			math.Float64bits(got1), math.Float64bits(got2), math.Float64bits(want1), math.Float64bits(want2))
	}

	var gotAC, wantAC [5]float64
	pitchAutocorr5(y[:length], length, &gotAC)
	pitchAutocorr5Ref(y[:length], length, &wantAC)
	if gotAC != wantAC {
		t.Fatalf("pitchAutocorr5 mismatch: got %v want %v", gotAC, wantAC)
	}

	len2 := maxPitch
	tmp := make([]float64, len2*2)
	for i := range tmp {
		tmp[i] = asmExactF64(seed^0x94d049bb133111eb, i)
	}
	gotEnergy := make([]float32, len2)
	wantEnergy := make([]float32, len2)
	gotMean := transientEnergyPairs(tmp, gotEnergy, len2)
	wantMean := asmTransientEnergyPairsRef(tmp, wantEnergy, len2)
	if math.Float64bits(gotMean) != math.Float64bits(wantMean) || !reflect.DeepEqual(gotEnergy, wantEnergy) {
		t.Fatalf("transientEnergyPairs mismatch: got (%016x,%v) want (%016x,%v)",
			math.Float64bits(gotMean), gotEnergy, math.Float64bits(wantMean), wantEnergy)
	}

	roundGot := append([]float64(nil), x...)
	roundWant := append([]float64(nil), x...)
	roundFloat64ToFloat32(roundGot)
	for i, v := range roundWant {
		roundWant[i] = float64(float32(v))
	}
	requireASMFloat64BitsEqual(t, "roundFloat64ToFloat32", roundGot, roundWant)

	widenSrc := make([]float32, length)
	for i := range widenSrc {
		widenSrc[i] = float32(asmExactF64(seed^0x2545f4914f6cdd1d, i))
	}
	widenGot := make([]float64, length)
	widenWant := make([]float64, length)
	widenFloat32ToFloat64(widenGot, widenSrc, length)
	for i, v := range widenSrc {
		widenWant[i] = float64(v)
	}
	requireASMFloat64BitsEqual(t, "widenFloat32ToFloat64", widenGot, widenWant)

	scaleGot := make([]float64, length)
	scaleWant := make([]float64, length)
	scale := []float64{0, 0.25, -0.5, 2}[seed&3]
	scaleFloat64Into(scaleGot, x, scale, length)
	for i, v := range x {
		scaleWant[i] = scale * v
	}
	requireASMFloat64BitsEqual(t, "scaleFloat64Into", scaleGot, scaleWant)

	if got, want := absSum(x), asmAbsSumRef(x); math.Float64bits(got) != math.Float64bits(want) {
		t.Fatalf("absSum mismatch: got %016x want %016x", math.Float64bits(got), math.Float64bits(want))
	}

	runPVQAssemblyReferenceCase(t, length, seed)
}

func runPVQAssemblyReferenceCase(t *testing.T, length int, seed uint64) {
	t.Helper()
	n := length%48 + 1
	pulsesLeft := int(seed%8) + 1
	absX := make([]float32, n)
	y := make([]float32, n)
	for i := 0; i < n; i++ {
		absX[i] = float32((int(asmMix(seed, i))%17)+1) * 0.25
		y[i] = float32((int(asmMix(seed^0x517cc1b727220a95, i)) % 8) * 2)
	}

	xy := float64(float32((int(seed%17) + 1))) * 0.25
	yy := float64(float32((int((seed>>8)%17) + 2)))
	if got, want := pvqSearchBestPos(absX, y, xy, yy, n), pvqSearchBestPosRef(absX, y, xy, yy, n); got != want {
		t.Fatalf("pvqSearchBestPos mismatch: got %d want %d", got, want)
	}

	yGot := append([]float32(nil), y...)
	yWant := append([]float32(nil), y...)
	iyGot := make([]int, n)
	iyWant := make([]int, n)
	gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, xy, yy, n, pulsesLeft)
	wantXY, wantYY := pvqSearchPulseLoopRef(absX, yWant, iyWant, xy, yy, n, pulsesLeft)
	if gotXY != wantXY || gotYY != wantYY || !reflect.DeepEqual(yGot, yWant) || !reflect.DeepEqual(iyGot, iyWant) {
		t.Fatalf("pvqSearchPulseLoop mismatch: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", gotXY, gotYY, yGot, iyGot, wantXY, wantYY, yWant, iyWant)
	}

	x := make([]float64, n)
	for i := range x {
		x[i] = asmExactF64(seed^0xdb4f0b9175ae2165, i)
	}
	gotAbs := make([]float32, n)
	wantAbs := make([]float32, n)
	gotY := make([]float32, n)
	wantY := make([]float32, n)
	gotSign := make([]byte, n)
	wantSign := make([]byte, n)
	gotIY := make([]int, n)
	wantIY := make([]int, n)
	pvqExtractAbsSign(x, gotAbs, gotY, gotSign, gotIY, n)
	pvqExtractAbsSignRef(x, wantAbs, wantY, wantSign, wantIY, n)
	if !reflect.DeepEqual(gotAbs, wantAbs) || !reflect.DeepEqual(gotY, wantY) || !reflect.DeepEqual(gotSign, wantSign) || !reflect.DeepEqual(gotIY, wantIY) {
		t.Fatalf("pvqExtractAbsSign mismatch")
	}
}

func asmExactF64(seed uint64, i int) float64 {
	v := int(asmMix(seed, i)%65) - 32
	return float64(v) * 0.0625
}

func asmMix(seed uint64, i int) uint64 {
	x := seed + uint64(i+1)*0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func asmPrefilterPitchXcorrRef(x, y, xcorr []float64, length, maxPitch int) {
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}

func asmPrefilterInnerProdRef(x, y []float64, length int) float64 {
	sum := float32(0)
	for i := 0; i < length; i++ {
		sum += float32(x[i]) * float32(y[i])
	}
	return float64(sum)
}

func asmTransientEnergyPairsRef(tmp []float64, x2out []float32, len2 int) float64 {
	var mean float32
	for i := 0; i < len2; i++ {
		t0 := float32(tmp[2*i])
		t1 := float32(tmp[2*i+1])
		x2 := t0*t0 + t1*t1
		x2out[i] = x2
		mean += x2
	}
	return float64(mean)
}

func asmAbsSumRef(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
}

func requireASMFloat64BitsEqual(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got %d want %d", name, len(got), len(want))
	}
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("%s[%d] mismatch: got %016x want %016x", name, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
		}
	}
}
