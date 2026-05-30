//go:build gopus_fixedpoint

package gopus

// decodeInt24TracksFloat32Skip is true under -tags gopus_fixedpoint, where
// DecodeInt24 routes CELT-only frames to the integer decoder's libopus-exact
// opus_res int24 instead of the float32 decode.
const decodeInt24TracksFloat32Skip = true
