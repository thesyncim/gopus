//go:build !gopus_extra_controls
// +build !gopus_extra_controls

package gopus

// decoderOSCEBWEState is an empty placeholder under the default build so the
// `*decoderOSCEBWEState` field on Decoder compiles without dragging in the
// osce/bwe package. The full struct lives in `decoder_osce_bwe_state.go`
// under the `gopus_extra_controls` build tag.
type decoderOSCEBWEState struct{}
