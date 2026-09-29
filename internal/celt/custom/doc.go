//go:build gopus_custom_modes

// Package custom implements the Opus Custom API in `opus_custom.h` for CELT
// modes with frame sizes or sample rates outside standard Opus geometry. Such
// modes require a peer with the same mode configuration. With signalling
// enabled, builds without QEXT use Opus TOC signalling only when the mode has
// Fs=48000 and a 120-sample short MDCT; other modes use the custom header.
// QEXT builds use TOC-style header conversion for every mode.
//
// [NewMode] builds the CELT band and transform tables for a mode. By default,
// [CustomEncoder] and [CustomDecoder] include a one-byte signaling header;
// callers must use the matching mode to interpret its frame geometry.
// [CustomEncoder.SetSignalling] can disable the header when the caller supplies
// frame size and channel count out of band.
//
// The package is available only with the `gopus_custom_modes` build tag. When
// QEXT is also enabled, the decoder recognizes the QEXT extension in code-3
// packet padding. Matched reference configurations and tested cases are listed
// in `reports/validation.md#coverage`.
package custom
