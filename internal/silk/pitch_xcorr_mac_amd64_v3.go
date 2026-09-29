//go:build amd64.v3

package silk

// pitchXcorrMAC32 follows celt/pitch.h:MAC16_16 in celt_pitch_xcorr_c.
// GCC contracts each scalar accumulation for the AMD64 v3 target. Keeping the
// operands in registers in this helper makes the Go v3 compiler emit one FMA.
//
//go:noinline
func pitchXcorrMAC32(acc, x, y float32) float32 {
	return x*y + acc
}
