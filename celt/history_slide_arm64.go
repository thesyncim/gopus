//go:build arm64 && !purego

package celt

//go:noescape
func slidePlanarHistoryPrefixLarge(hist []float64, frameSize, keep int)
