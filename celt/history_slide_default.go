//go:build !arm64

package celt

func slidePlanarHistoryPrefixLarge(hist []float64, frameSize, keep int) {
	copy(hist[:keep], hist[frameSize:frameSize+keep])
}
