package testvectors

import "github.com/thesyncim/gopus/internal/celt"

// vorbisWindowFull returns the full-length Vorbis window value for a window of size n2.
// It mirrors the CELT half-window (overlap) to build a full 2*overlap window.
func vorbisWindowFull(i, n2 int) float64 {
	if n2 <= 0 {
		return 0
	}
	overlap := n2 / 2
	if overlap <= 0 {
		return 0
	}
	if i < overlap {
		return celt.VorbisWindow(i, overlap)
	}
	return celt.VorbisWindow(n2-1-i, overlap)
}
