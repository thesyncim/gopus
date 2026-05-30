//go:build !gopus_fixedpoint

package gopus

import "github.com/thesyncim/gopus/celt"

// celtDecodeFixedAPIRate is a no-op in the default (float) build: it never
// handles the CELT-only decode, so the caller falls through to the float CELT
// decoder. It exists only to keep the dispatch in
// decodeOpusFrameIntoWithStatePolicyAndQEXT build-tag agnostic.
func (d *Decoder) celtDecodeFixedAPIRate(_ []byte, _ int, _ bool, _ celt.CELTBandwidth, _ []float32) (bool, error) {
	return false, nil
}

// resetFixedCELT is a no-op in the default build.
func (d *Decoder) resetFixedCELT() {}

// The integer-output accumulation helpers are no-ops in the default build; the
// int16/int24 wrappers there always use the float conversion.
func (d *Decoder) beginFixedPacket()          {}
func (d *Decoder) endFixedPacket()            {}
func (d *Decoder) markFixedUnhandled()        {}
func (d *Decoder) fixedInt16Ready(_ int) bool { return false }

// finishInt16Output / finishInt24Output always use the shared float conversion
// in the default build, matching the previous behavior exactly.
func (d *Decoder) finishInt16Output(pcm []int16, scratch []float32, n, channels int) bool {
	softClipAndFloat32ToInt16(pcm, scratch, n, channels, d.softClipMem[:])
	return false
}

func (d *Decoder) finishInt24Output(pcm []int32, scratch []float32, n, channels int) bool {
	float32ToInt24Slice(pcm, scratch, n, channels)
	return false
}
