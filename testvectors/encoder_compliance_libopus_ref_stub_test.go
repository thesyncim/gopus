//go:build !cgo_libopus

package testvectors

import (
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func libopusComplianceReferenceAvailable() bool {
	return longFrameFixtureReferenceAvailable()
}

func encodeWithLibopusComplianceReference(
	_ []float32,
	_, _, _, _ int,
	_ encoder.Mode,
	_ types.Bandwidth,
) [][]byte {
	return nil
}
