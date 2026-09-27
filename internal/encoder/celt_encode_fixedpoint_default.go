//go:build !gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/rangecoding"

// fixedPointBuild is false in the default (float) build.
const fixedPointBuild = false

// encoderFixedCELTFields is empty in the default (float) build, keeping the
// Encoder struct byte-unchanged.
type encoderFixedCELTFields struct{}

// encodeCELTFrameFixed never handles a frame in the default build, so the CELT
// frame seam always uses the float celt.Encoder. This keeps the dispatch in
// encodeCELTFrameWithBitrateMaxPayloadAndDRED build-tag agnostic.
func (e *Encoder) encodeCELTFrameFixed(_ []opusRes, _, _, _ int, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) encodeHybridCELTFrameFixed(_ []int32, _, _, _ int, _ *rangecoding.Encoder, _ bool) ([]byte, bool, error) {
	return nil, false, nil
}

func (e *Encoder) encodeRedundantCELTFrameFixed(_ []int32, _, _, _ int, _, _ bool) ([]byte, uint32, bool, error) {
	return nil, 0, false, nil
}

func (e *Encoder) prefillCELTFrameFixed(_ []int32, _, _, _, _ int, _ int32) bool { return false }

// resetFixedCELT is a no-op in the default build.
func (e *Encoder) resetFixedCELT() {}

func (e *Encoder) resetFixedCELTState() {}

// fixedCELTFinalRange never reports an integer range in the default build.
func (e *Encoder) fixedCELTFinalRange() (uint32, bool) { return 0, false }

// clearFixedCELTUsed is a no-op in the default build.
func (e *Encoder) clearFixedCELTUsed() {}

func (e *Encoder) prepareFixedInputRes(_ []float32) {}
func (e *Encoder) clearFixedInputRes()              {}
func (e *Encoder) preprocessFixedInputRes(_ int)    {}
func (e *Encoder) prepareFixedCELTPCM(_ int)        {}
func (e *Encoder) updateFixedDelayBuffer(_ int)     {}
func (e *Encoder) applyFixedStereoWidth(_ int16)    {}
