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
// Oracle parity status: oracle_test.go compares encode+decode against a libopus
// build configured with --enable-custom-modes. That reference tree is produced
// on demand by tools/ensure_libopus.sh LIBOPUS_ENABLE_CUSTOM=1 (->
// tmp_check/opus-1.6.1-custom) and linked through
// libopustest.CHelperConfig{CustomRef: true}.
//
//   - Standard 48 kHz modes (120/240/480/960) are byte- and sample-exact against
//     the libopus custom-modes encoder/decoder.
//   - Non-standard rates/frame sizes are NOT yet libopus-correct: EncodeFloat /
//     DecodeFloat fall back to the nearest standard 48 kHz frame size and resample,
//     so the CustomMode band/preemph tables NewMode computes are not used by the
//     CELT core. These modes are only self-consistent (gopus encode -> gopus
//     decode); the oracle test records the first divergence from libopus.
package custom
