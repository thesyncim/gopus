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
//   - The control plane of the Fs==400*shortMdctSize family (e.g. 8k/160,
//     12k/240, 16k/320, 24k/480, 32k/640) is now parameterized and proven exact:
//     CustomMode.InScaledBandFamily reports membership, and
//     TestOracleControlPlaneScaledBandFamily verifies the full mode geometry
//     (maxLM, nbShortMdcts, shortMdctSize, overlap, eBands, effEBands, logN and
//     per-rate pre-emphasis) against opus_custom_mode_create, plus the
//     band-bin scaling celt.ScaledBandStartBase/EndBase == eBands[i]<<LM.
//   - Non-standard rates/frame sizes still return ErrNonStandard from
//     EncodeFloat / DecodeFloat: the CELT data plane (overlap-add MDCT
//     analysis/synthesis, windowing) is keyed to the 48 kHz overlap (120) and
//     the static band-bin scaling (eBand*frameSize/120), so it cannot yet
//     reproduce a libopus --enable-custom-modes bitstream for these modes.
//     NewMode computes the full mode tables (eBands, allocVectors, logN, window,
//     preemph) for them, mirroring opus_custom_mode_create.
package custom
