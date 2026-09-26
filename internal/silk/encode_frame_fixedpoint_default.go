//go:build !gopus_fixed_point

package silk

// silkEncoderFixedFields is empty in the default (float) build, keeping the
// integer SILK encode path unlinked.
type silkEncoderFixedFields struct{}

// fixedEncodeActive reports whether the integer SILK encode path is selected.
// Always false in the default build.
func (e *Encoder) fixedEncodeActive() bool { return false }

// silkFixedEncodeBuild reports whether the integer SILK encode path is compiled
// in. False in the default (float) build.
const silkFixedEncodeBuild = false

// encodeFrameFixedBody is never reached in the default build (fixedEncodeActive
// returns false); the stub keeps encodeFrame build-tag agnostic.
func (e *Encoder) encodeFrameFixedBody(_ []int16, _, _, _ int, _, _ bool, _ int, _ bool) int32 {
	return 0
}

// prefillFrameFixed is never reached in the default build (fixedEncodeActive
// returns false).
func (e *Encoder) prefillFrameFixed(_ []int16) {}

// resetFixedState is a no-op in the default build.
func (e *Encoder) resetFixedState() {}

// resetStereoSideFixedState is a no-op in the default build.
func (e *Encoder) resetStereoSideFixedState() {}
