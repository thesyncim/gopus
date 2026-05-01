//go:build !arm64 || purego

package celt

func imdctPostRotateF32FromKiss(buf []float32, fft []kissCpx, trig []float32, n2, n4 int) {
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
	imdctPostRotateF32(buf, trig, n2, n4)
}
