// Package silk implements the SILK speech layer of Opus (RFC 6716 Section 4.2),
// the low-level codec used by gopus for the SILK and Hybrid Opus modes.
//
// Most applications should use the top-level gopus encoder/decoder APIs rather
// than this package directly. The symbols exported here are advanced
// implementation details and may change before the first release.
//
// # Relationship to libopus
//
// This package implements the SILK decoder and encoder against libopus 1.6.1
// (the silk/ directory of the libopus source tree). Fixed-point state and
// arithmetic follow the reference Q-format operations. Exact decode coverage
// is recorded by the package's oracle tests and reports/go-simd-kernel-evidence.md;
// Go comments name the matching C translation unit where a helper mirrors one.
//
// # Decoder
//
// The decode path is structured as in libopus:
//
//   - Decode / DecodeStereo (silk.go) — public per-packet entry points,
//     mirroring silk/dec_API.c silk_Decode. They parse the SILK header
//     (VAD + LBRR flags), decode each 20 ms frame, run mid/side to left/right
//     stereo unmixing, and resample from the internal SILK rate (8/12/16 kHz)
//     to the decoder API rate.
//   - decodeFrameCoreInto (frame_decode_helpers.go) — one SILK frame, mirroring
//     silk/decode_frame.c silk_decode_frame: decode indices, decode pulses,
//     dequantize parameters, run the LTP/LPC synthesis core.
//   - silkDecodeIndices / silkDecodeParameters / silkDecodeCore /
//     silkDecodePulses (libopus_decode.go) — port silk/decode_indices.c,
//     silk/decode_parameters.c, silk/decode_core.c and silk/decode_pulses.c.
//   - LBRR / Forward Error Correction (lbrr_decode.go) — DecodeFEC mirrors
//     silk_Decode with lostFlag = FLAG_DECODE_LBRR.
//   - Packet Loss Concealment (silk.go, cng.go, plc_glue.go, plc package) —
//     ports silk/PLC.c and silk/CNG.c; invoked when Decode is called with nil
//     data for a lost packet.
//   - Resampler (resample_libopus.go) — ports silk/resampler*.c.
//
// Packet-facing decode paths bound symbol-derived indices before they select
// state or buffers. The package's malformed-input fuzz targets exercise this
// behavior, and its oracle tests record exactness for their tested valid inputs.
package silk
