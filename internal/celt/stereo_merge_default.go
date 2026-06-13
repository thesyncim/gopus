//go:build !arm64 || purego

package celt

// stereoMergeRescaleNEON is the portable fallback for the arm64 rescale kernel,
// running the same per-lane noFMA32 mid/side combine over len(x) elements.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	// x already has len n; reslice y to n so the compiler proves both indices
	// in-bounds and drops the per-iteration bounds checks (the caller always
	// passes len(y) == len(x); a shorter y panics here, as it did at y[i]).
	x = x[:n]
	y = y[:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		l0 := noFMA32Mul(mid, x[i])
		l1 := noFMA32Mul(mid, x[i+1])
		l2 := noFMA32Mul(mid, x[i+2])
		l3 := noFMA32Mul(mid, x[i+3])
		r0, r1, r2, r3 := y[i], y[i+1], y[i+2], y[i+3]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l0, r0))
		x[i+1] = noFMA32Mul(lgain, noFMA32Sub(l1, r1))
		x[i+2] = noFMA32Mul(lgain, noFMA32Sub(l2, r2))
		x[i+3] = noFMA32Mul(lgain, noFMA32Sub(l3, r3))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l0, r0))
		y[i+1] = noFMA32Mul(rgain, noFMA32Add(l1, r1))
		y[i+2] = noFMA32Mul(rgain, noFMA32Add(l2, r2))
		y[i+3] = noFMA32Mul(rgain, noFMA32Add(l3, r3))
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}
