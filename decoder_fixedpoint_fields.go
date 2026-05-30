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

	// fixedHybridRes is the per-frame interleaved opus_res scratch that holds the
	// SILK lowband (INT16TORES) before the integer CELT highband accumulates onto
	// it; it then carries the combined hybrid opus_res output.
	fixedHybridRes []int32
	// fixedHybridInt16 is the per-frame int16 view (Res2Int16) of fixedHybridRes.
	fixedHybridInt16 []int16
	// fixedHybridEnd is the CELT end band (CELT_SET_END_BAND) for the in-flight
	// hybrid frame, captured before the hybrid decode invokes the highband hook.
	fixedHybridEnd int
	// fixedHybridErr captures an error from the highband hook (the hook signature
	// is void to match the hybrid float path; the dispatch checks it afterwards).
	fixedHybridErr error
	// fixedHybridReset records whether the integer CELT cross-frame state must be
	// reset before the in-flight hybrid frame (mirroring the float OPUS_RESET_STATE
	// on a mode transition), set before the hybrid decode.
	fixedHybridReset bool
}
