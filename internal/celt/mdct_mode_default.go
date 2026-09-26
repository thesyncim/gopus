//go:build !gopus_custom_modes

package celt

type customMDCTState struct{}

func (*customMDCTState) mdctLookup(int) *mdctTransformLookup { return nil }
