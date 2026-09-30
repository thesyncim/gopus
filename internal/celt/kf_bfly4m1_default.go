package celt

import "unsafe"

// kfBfly4M1CoreScalar is the kf_bfly4 m == 1 stage: n twiddle-free radix-4
// butterflies over consecutive groups of four values. Each group is addressed
// through a four-element array pointer at a byte offset, so its loads and
// stores use constant displacements from one base.
func kfBfly4M1CoreScalar(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}
	// Group i sits at byte offset 32*i of the checked fout[:4*n].
	base := unsafe.Pointer(unsafe.SliceData(fout[:4*n]))
	for off := uintptr(0); off < uintptr(n)*32; off += 32 {
		g := (*[4]kissCpx)(unsafe.Add(base, off))
		a0r, a0i := g[0].r, g[0].i
		a1r, a1i := g[1].r, g[1].i
		a2r, a2i := g[2].r, g[2].i
		a3r, a3i := g[3].r, g[3].i

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
		g[0] = kissCpx{f0r, f0i}
		g[1] = kissCpx{s0r + s1i, s0i - s1r}
		g[2] = kissCpx{f2r, f2i}
		g[3] = kissCpx{s0r - s1i, s0i + s1r}
	}
}
