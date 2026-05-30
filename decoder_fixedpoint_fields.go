//go:build gopus_fixedpoint

package gopus

import "github.com/thesyncim/gopus/internal/fixedpoint"

// decoderFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixedpoint build to produce integer-exact CELT-only output through the
// public Decoder. It is created lazily on the first CELT-only frame so the
// allocation is avoided for SILK-only streams.
//
// For a packet whose every frame is handled by the integer CELT decoder, the
// int16 and opus_res (int24) outputs are accumulated here so the int16/int24
// public wrappers can read them directly, bypassing the lossy float32->int
// conversion. fixedAllHandled records whether the in-flight packet qualified.
type decoderFixedFields struct {
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16

	// fixedInt16 / fixedRes accumulate the integer CELT output of the current
	// packet, interleaved at the API rate (channels stride). fixedCursor is the
	// running write offset (in interleaved elements) used while a packet is
	// decoded.
	fixedInt16  []int16
	fixedRes    []int32
	fixedCursor int

	// fixedAllHandled is true while every frame of the in-flight packet has been
	// decoded by the integer CELT path (and is therefore recoverable bit-exact
	// from fixedInt16/fixedRes). It is reset at packet start and cleared the
	// moment any frame falls through to the float decoder.
	fixedAllHandled bool
	// fixedPacketActive guards the accumulation: it is true only between
	// beginFixedPacket and the int16/int24 wrapper consuming the result.
	fixedPacketActive bool
}
