//go:build !gopus_fixedpoint

package gopus

// decodeInt24TracksFloat32Skip is false in the default (float) build, where
// DecodeInt24 derives int24 from the same float32 decode as Decode.
const decodeInt24TracksFloat32Skip = false
