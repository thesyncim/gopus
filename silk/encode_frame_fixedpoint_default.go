//go:build !gopus_fixedpoint

package silk

// silkEncoderFixedFields is empty in the default (float) build, keeping the
// Encoder struct byte-unchanged and the integer SILK encode path unlinked.
type silkEncoderFixedFields struct{}

// fixedEncodeActive reports whether the integer SILK encode path is selected.
// Always false in the default build.
func (e *Encoder) fixedEncodeActive() bool { return false }

// silkFixedEncodeBuild reports whether the integer SILK encode path is compiled
// in. False in the default (float) build.
const silkFixedEncodeBuild = false

// encodeFrameFixedBody is never reached in the default build (fixedEncodeActive
// returns false); the stub keeps EncodeFrame build-tag agnostic.
func (e *Encoder) encodeFrameFixedBody(_ []float32, _, _, _, _, _ int, _, _, _, _ bool) []byte {
	return nil
}

// resetFixedState is a no-op in the default build.
func (e *Encoder) resetFixedState() {}
