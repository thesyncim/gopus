package celt

// kfBfly4M1CoreScalar is the kf_bfly4 m == 1 stage: n twiddle-free radix-4
// butterflies over consecutive groups of four interleaved complex values,
// using a fixed-size array view for each group.
func kfBfly4M1CoreScalar(fout []float32, n int) {
	if n <= 0 {
		return
	}
	groups := fout[:8*n]
	for i := 0; i < len(groups); i += 8 {
		g := (*[8]float32)(groups[i : i+8])
		a0r, a0i := g[0], g[1]
		a1r, a1i := g[2], g[3]
		a2r, a2i := g[4], g[5]
		a3r, a3i := g[6], g[7]

		s0r := a0r - a2r
		s0i := a0i - a2i
		f0r := a0r + a2r
		f0i := a0i + a2i

		s1r := a1r + a3r
		s1i := a1i + a3i
		f2r := f0r - s1r
		f2i := f0i - s1i
		f0r += s1r
		f0i += s1i

		s1r = a1r - a3r
		s1i = a1i - a3i
		g[0], g[1] = f0r, f0i
		g[2], g[3] = s0r+s1i, s0i-s1r
		g[4], g[5] = f2r, f2i
		g[6], g[7] = s0r-s1i, s0i+s1r
	}
}
