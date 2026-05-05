//go:build (arm64 || amd64) && !purego

package celt

// prefilterPitchXcorrFast keeps CELT prefilter pitch search on the same
// float32 accumulation path libopus uses for opus_val16 pitch buffers.
func prefilterPitchXcorrFast(x, y, xcorr []float64, length, maxPitch int) {
	prefilterPitchXcorr(x, y, xcorr, length, maxPitch)
}
