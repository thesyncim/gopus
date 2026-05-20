//go:build !gopus_extra_controls
// +build !gopus_extra_controls

package gopus

// decoderOSCELACEState is an empty placeholder under the default build so
// the `*decoderOSCELACEState` field on Decoder compiles without dragging
// in the osce/lace package. The full struct lives in
// `decoder_osce_lace_state.go` under the `gopus_extra_controls`
// build tag.
type decoderOSCELACEState struct{}
