package celt

import (
	"reflect"
	"testing"
	"unsafe"
)

func imdctPreRotateF32Ref(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}

	_ = spectrum[n2-1]
	_ = trig[n2-1]
	out := unsafe.Slice((*float32)(unsafe.Pointer(&fftIn[0])), n4*2)
	_ = out[n4*2-1]

	i := 0
	for ; i+1 < n4; i += 2 {
		x10 := float32(spectrum[2*i])
		x20 := float32(spectrum[n2-1-2*i])
		t00 := trig[i]
		t10 := trig[n4+i]
		b0 := 2 * i
		out[b0] = x10*t00 - x20*t10
		out[b0+1] = x20*t00 + x10*t10

		i1 := i + 1
		x11 := float32(spectrum[2*i1])
		x21 := float32(spectrum[n2-1-2*i1])
		t01 := trig[i1]
		t11 := trig[n4+i1]
		b1 := 2 * i1
		out[b1] = x11*t01 - x21*t11
		out[b1+1] = x21*t01 + x11*t11
	}

	if i < n4 {
		x1 := float32(spectrum[2*i])
		x2 := float32(spectrum[n2-1-2*i])
		t0 := trig[i]
		t1 := trig[n4+i]
		b := 2 * i
		out[b] = x1*t0 - x2*t1
		out[b+1] = x2*t0 + x1*t1
	}
}

func imdctPostRotateF32Ref(buf []float32, trig []float32, n2, n4 int) {
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}

	_ = buf[n2-1]
	_ = trig[n2-1]

	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < limit; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}
}

func imdctPostRotateF32FromKissRef(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < n4 {
		return
	}
	_ = buf[n2-1]
	_ = fft[n4-1]
	j := 0
	for i := 0; i < n4; i++ {
		v := fft[i]
		buf[j] = v.r
		buf[j+1] = v.i
		j += 2
	}
	imdctPostRotateF32Ref(buf, trig, n2, n4)
}

func TestIMDCTRotateDispatchMatchesReference(t *testing.T) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float64, n2)
	trig := make([]float32, n2)
	for i := range spectrum {
		spectrum[i] = float64((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}

	gotFFT := make([]complex64, n4)
	wantFFT := make([]complex64, n4)
	imdctPreRotateF32(gotFFT, spectrum, trig, n2, n4)
	imdctPreRotateF32Ref(wantFFT, spectrum, trig, n2, n4)
	if !reflect.DeepEqual(gotFFT, wantFFT) {
		t.Fatalf("imdctPreRotateF32 mismatch")
	}

	gotBuf := make([]float32, n2)
	wantBuf := make([]float32, n2)
	copy(gotBuf, unsafe.Slice((*float32)(unsafe.Pointer(&gotFFT[0])), n4*2))
	copy(wantBuf, unsafe.Slice((*float32)(unsafe.Pointer(&wantFFT[0])), n4*2))
	imdctPostRotateF32(gotBuf, trig, n2, n4)
	imdctPostRotateF32Ref(wantBuf, trig, n2, n4)
	if !reflect.DeepEqual(gotBuf, wantBuf) {
		t.Fatalf("imdctPostRotateF32 mismatch")
	}
}

func TestIMDCTPostRotateF32FromKissMatchesReference(t *testing.T) {
	for _, n2 := range []int{10, 120} {
		n4 := n2 / 2
		trig := make([]float32, n2)
		fft := make([]kissCpx, n4)
		for i := range trig {
			trig[i] = float32((i%19)-9) * 0.03125
		}
		for i := range fft {
			fft[i] = kissCpx{
				r: float32((i%17)-8) * 0.0625,
				i: float32((i%23)-11) * -0.03125,
			}
		}

		got := make([]float32, n2)
		want := make([]float32, n2)
		imdctPostRotateF32FromKiss(got, fft, trig, n2, n4)
		imdctPostRotateF32FromKissRef(want, fft, trig, n2, n4)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("n2=%d imdctPostRotateF32FromKiss mismatch", n2)
		}
	}
}

func BenchmarkIMDCTPreRotateCurrent(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float64, n2)
	trig := make([]float32, n2)
	fftIn := make([]complex64, n4)
	for i := range spectrum {
		spectrum[i] = float64((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPreRotateF32(fftIn, spectrum, trig, n2, n4)
	}
}

func BenchmarkIMDCTPreRotateReference(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	spectrum := make([]float64, n2)
	trig := make([]float32, n2)
	fftIn := make([]complex64, n4)
	for i := range spectrum {
		spectrum[i] = float64((i%17)-8) * 0.0625
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPreRotateF32Ref(fftIn, spectrum, trig, n2, n4)
	}
}

func BenchmarkIMDCTPostRotateCurrent(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	buf := make([]float32, n2)
	trig := make([]float32, n2)
	for i := range buf {
		buf[i] = float32((i%23)-11) * 0.03125
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPostRotateF32(buf, trig, n2, n4)
	}
}

func BenchmarkIMDCTPostRotateReference(b *testing.B) {
	n2 := 120
	n4 := n2 / 2
	buf := make([]float32, n2)
	trig := make([]float32, n2)
	for i := range buf {
		buf[i] = float32((i%23)-11) * 0.03125
	}
	for i := range trig {
		trig[i] = float32((i%19)-9) * 0.03125
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imdctPostRotateF32Ref(buf, trig, n2, n4)
	}
}
