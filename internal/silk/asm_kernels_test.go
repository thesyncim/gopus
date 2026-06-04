package silk

import (
	"reflect"
	"testing"
)

func TestSilkAssemblyKernelsMatchReference(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 80, 120}
	for _, n := range lengths {
		for offset := 0; offset < 4; offset++ {
			runSilkAssemblyReferenceCase(t, n, offset, uint64(n*4099+offset))
		}
	}
}

func FuzzSilkAssemblyKernelsMatchReference(f *testing.F) {
	for _, seed := range []struct {
		length uint8
		offset uint8
		seed   uint64
	}{
		{1, 0, 1},
		{4, 1, 2},
		{7, 2, 3},
		{16, 3, 4},
		{31, 0, 5},
		{80, 1, 6},
	} {
		f.Add(seed.length, seed.offset, seed.seed)
	}
	f.Fuzz(func(t *testing.T, rawLength, rawOffset uint8, seed uint64) {
		length := int(rawLength%120) + 1
		offset := int(rawOffset % 4)
		runSilkAssemblyReferenceCase(t, length, offset, seed)
	})
}

func runSilkAssemblyReferenceCase(t *testing.T, length, offset int, seed uint64) {
	t.Helper()
	testWriteInt16AsFloat32Core(t, length, offset, seed)
	testSilkPitchXcorrCore(t, length, offset, seed)
	testSilkResamplerFIRCore(t, length, offset, seed)
	testSilkUp2HQCore(t, asmMin(length, maxSubFrameLength), offset, seed)
	testSilkLPCSynthesisCore(t, asmMin(length, maxSubFrameLength), seed)
}

func testWriteInt16AsFloat32Core(t *testing.T, n, offset int, seed uint64) {
	t.Helper()
	srcBuf := make([]int16, offset+n+8)
	dstGotBuf := make([]float32, offset+n+8)
	dstWantBuf := make([]float32, offset+n+8)
	src := srcBuf[offset : offset+n]
	got := dstGotBuf[offset : offset+n]
	want := dstWantBuf[offset : offset+n]
	for i := range src {
		src[i] = asmInt16(seed, i)
	}

	writeInt16AsFloat32Core(got, src, n)
	for i, v := range src {
		want[i] = float32(v) * (1.0 / 32768.0)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writeInt16AsFloat32Core n=%d offset=%d mismatch: got %v want %v", n, offset, got, want)
	}
}

func testSilkPitchXcorrCore(t *testing.T, length, offset int, seed uint64) {
	t.Helper()
	maxPitch := length%32 + 1
	xBuf := make([]float32, offset+length+8)
	yBuf := make([]float32, offset+length+maxPitch+8)
	outGotBuf := make([]float32, offset+maxPitch+8)
	outWantBuf := make([]float32, offset+maxPitch+8)
	x := xBuf[offset : offset+length]
	y := yBuf[offset : offset+length+maxPitch]
	got := outGotBuf[offset : offset+maxPitch]
	want := outWantBuf[offset : offset+maxPitch]
	for i := range x {
		x[i] = asmExactF32(seed, i)
	}
	for i := range y {
		y[i] = asmExactF32(seed^0x9e3779b97f4a7c15, i)
	}

	celtPitchXcorrFloatImpl(x, y, got, length, maxPitch)
	asmCeltPitchXcorrFloatRef(x, y, want, length, maxPitch)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("celtPitchXcorrFloatImpl length=%d maxPitch=%d offset=%d mismatch: got %v want %v", length, maxPitch, offset, got, want)
	}
}

func testSilkResamplerFIRCore(t *testing.T, nOut, offset int, seed uint64) {
	t.Helper()
	testFIRCore(t, "21846", offset, nOut, (nOut-1)/3+8, seed, firInterpol21846Core, firInterpol21846CoreGo)
	testFIRCore(t, "32768", offset, nOut, ((nOut-1)>>1)+8, seed^0xbf58476d1ce4e5b9, firInterpol32768Core, firInterpol32768CoreGo)
	testFIRCore(t, "43691", offset, nOut, (2*(nOut-1))/3+8, seed^0x94d049bb133111eb, firInterpol43691Core, firInterpol43691CoreGo)
}

func testFIRCore(t *testing.T, name string, offset, nOut, bufLen int, seed uint64, current, ref func([]int16, []int16, int)) {
	t.Helper()
	dstGotBuf := make([]int16, offset+nOut+8)
	dstWantBuf := make([]int16, offset+nOut+8)
	bufRaw := make([]int16, offset+bufLen+8)
	dstGot := dstGotBuf[offset : offset+nOut]
	dstWant := dstWantBuf[offset : offset+nOut]
	buf := bufRaw[offset : offset+bufLen]
	for i := range buf {
		buf[i] = asmInt16(seed, i)
	}

	current(dstGot, buf, nOut)
	ref(dstWant, buf, nOut)
	if !reflect.DeepEqual(dstGot, dstWant) {
		t.Fatalf("firInterpol%sCore nOut=%d offset=%d mismatch: got %v want %v", name, nOut, offset, dstGot, dstWant)
	}
}

func testSilkUp2HQCore(t *testing.T, n, offset int, seed uint64) {
	t.Helper()
	inBuf := make([]int16, offset+n+8)
	outGotBuf := make([]int16, offset+2*n+8)
	outWantBuf := make([]int16, offset+2*n+8)
	in := inBuf[offset : offset+n]
	got := outGotBuf[offset : offset+2*n]
	want := outWantBuf[offset : offset+2*n]
	for i := range in {
		in[i] = asmInt16(seed, i)
	}
	stateGot := [6]int32{
		int32(seed&0x1ffff) - 0x10000,
		int32((seed>>8)&0x1ffff) - 0x10000,
		int32((seed>>16)&0x1ffff) - 0x10000,
		int32((seed>>24)&0x1ffff) - 0x10000,
		int32((seed>>32)&0x1ffff) - 0x10000,
		int32((seed>>40)&0x1ffff) - 0x10000,
	}
	stateWant := stateGot

	up2HQCore(got, in, &stateGot)
	up2HQCoreGo(want, in, &stateWant)
	if !reflect.DeepEqual(got, want) || stateGot != stateWant {
		t.Fatalf("up2HQCore n=%d offset=%d mismatch: got (%v,%v) want (%v,%v)", n, offset, got, stateGot, want, stateWant)
	}
}

func testSilkLPCSynthesisCore(t *testing.T, n int, seed uint64) {
	t.Helper()
	sLPC, a, pres := testLPCSynthesisInputs()
	for i := range sLPC {
		sLPC[i] ^= int32(asmMix(seed, i) & 0xffff)
	}
	for i := range a {
		a[i] ^= int16(asmMix(seed^0x517cc1b727220a95, i) & 0x3ff)
	}
	for i := range pres {
		pres[i] ^= int32(asmMix(seed^0xdb4f0b9175ae2165, i) & 0xffff)
	}
	sLPCRef := sLPC
	var got [maxSubFrameLength]int16
	var want [maxSubFrameLength]int16
	gainQ10 := int32(1024 + int(seed%8192))

	synthesizeLPCOrder16Core(sLPC[:], a[:], pres[:], got[:], gainQ10, n)
	synthesizeLPCOrder16ScalarForTest(sLPCRef[:], a[:], pres[:], want[:], gainQ10, n)
	if !reflect.DeepEqual(got[:n], want[:n]) || !reflect.DeepEqual(sLPC[:maxLPCOrder+n], sLPCRef[:maxLPCOrder+n]) {
		t.Fatalf("synthesizeLPCOrder16Core n=%d mismatch", n)
	}
}

func asmCeltPitchXcorrFloatRef(x, y []float32, out []float32, length, maxPitch int) {
	for lag := 0; lag < maxPitch; lag++ {
		sum := float32(0)
		for i := 0; i < length; i++ {
			sum += x[i] * y[lag+i]
		}
		out[lag] = sum
	}
}

func asmExactF32(seed uint64, i int) float32 {
	return float32(int(asmMix(seed, i)%65)-32) * 0.0625
}

func asmInt16(seed uint64, i int) int16 {
	return int16(int(asmMix(seed, i)%65535) - 32767)
}

func asmMix(seed uint64, i int) uint64 {
	x := seed + uint64(i+1)*0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func asmMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
