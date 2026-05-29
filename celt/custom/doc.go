//go:build gopus_custom

// Package custom implements the Opus Custom API (opus_custom.h) for non-standard
// sample rates and frame sizes.
//
// Opus Custom is the compile-gated CUSTOM_MODES extension of the CELT codec that
// allows frame sizes not present in the standard Opus specification (2.5/5/10/20 ms)
// and sample rates other than 8/12/16/24/48 kHz. It is intended for specialised
// applications where a specific frame size or sample rate is required and
// interoperability with standard Opus decoders is not needed.
//
// This package mirrors the libopus opus_custom.h API:
//
//	NewMode(Fs, frameSize int) (*CustomMode, error)
//	NewEncoder(mode *CustomMode, channels int) (*CustomEncoder, error)
//	NewDecoder(mode *CustomMode, channels int) (*CustomDecoder, error)
//
// Encoding and decoding (float32 and int16 PCM paths):
//
//	enc.EncodeFloat(pcm []float32, maxBytes int) ([]byte, error)
//	enc.Encode(pcm []int16, maxBytes int) ([]byte, error)
//	dec.DecodeFloat(data []byte, frameSize int) ([]float32, error)
//	dec.Decode(data []byte, frameSize int) ([]int16, error)
//
// CTLs mirror the libopus opus_custom_encoder_ctl / opus_custom_decoder_ctl
// generic CTL constants (OPUS_SET_COMPLEXITY, OPUS_SET_BITRATE, etc.).
//
// Build tag: gopus_custom (mirrors libopus CUSTOM_MODES compile guard).
// Default builds exclude this package entirely; zero build cost when unset.
//
// Oracle parity status: the pinned libopus 1.6.1 reference tree
// (tmp_check/opus-1.6.1) was NOT built with --enable-custom-modes, so byte-exact
// oracle tests are gated to a separate custom-modes libopus build. Self-consistency
// (encode→decode roundtrip) and basic API contract tests run unconditionally under
// the gopus_custom tag. See custom_oracle_test.go for oracle parity tests.
package custom
