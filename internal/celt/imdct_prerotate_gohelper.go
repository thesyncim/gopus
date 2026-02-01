package celt

func imdctPreRotateF32Out(out []float32, spectrum []float64, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}
	if len(out) < n4*2 {
		return
	}
	if len(trig) < n4+n4 {
		return
	}
	for i := 0; i < n4; i++ {
		x1 := float32(spectrum[2*i])
		x2 := float32(spectrum[n2-1-2*i])
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		out[2*i] = yi
		out[2*i+1] = yr
	}
}
