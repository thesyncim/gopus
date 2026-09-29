//go:build gopus_custom_modes

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
// Custom encoders and decoders use the one-byte custom frame header by default,
// matching opus_custom_encoder_create() and opus_custom_decoder_create(). The
// header carries frame size, channel count, and end band. SetSignalling(false)
// selects raw CELT payloads for callers that carry those values out of band.
// Encoder defaults match opus_custom_encoder_init_arch: CBR, constrained-VBR
// enabled for callers that turn VBR on, complexity 5, and 24-bit input depth.
// The decoder complexity default is 0. Signalled packets can carry a shorter
// frame than the supplied decode output capacity. QEXT builds also decode the
// QEXT extension when it appears in code-3 packet padding.
//
// CTLs mirror the libopus opus_custom_encoder_ctl / opus_custom_decoder_ctl
// generic CTL constants (OPUS_SET_COMPLEXITY, OPUS_SET_BITRATE, etc.).
//
// Build tag: gopus_custom_modes (mirrors libopus CUSTOM_MODES compile guard).
// Default builds exclude this package entirely; zero build cost when unset.
//
// Oracle parity status: oracle_test.go compares encode+decode against a libopus
// build configured with --enable-custom-modes. That reference tree is produced
// on demand by tools/ensure_libopus.sh LIBOPUS_ENABLE_CUSTOM=1 (->
// tmp_check/opus-1.6.1-custom) and linked through
// libopustest.CHelperConfig{CustomRef: true}.
//
//   - TestOracleParityStandardModes compares packet bytes and decoded samples
//     against libopus for mono sine inputs at 48 kHz with 120/240/480/960-sample
//     frames.
//   - The control plane of the Fs==400*shortMdctSize family (e.g. 8k/160,
//     12k/240, 16k/320, 24k/480, 32k/640) uses parameterized mode geometry:
//     CustomMode.InScaledBandFamily reports membership, and
//     TestOracleControlPlaneScaledBandFamily verifies the full mode geometry
//     (maxLM, nbShortMdcts, shortMdctSize, overlap, eBands, effEBands, logN and
//     per-rate pre-emphasis) against opus_custom_mode_create, plus the
//     band-bin scaling celt.ScaledBandStartBase/EndBase == eBands[i]<<LM.
//   - Custom band layouts outside that family (e.g. 48000/640,
//     NbEBands=19) are also encoded and decoded byte/sample-identically to
//     libopus --enable-custom-modes: the per-mode band tables (eBands, widths,
//     logN, allocVectors and the compute_pulse_cache index/bits/caps) computed by
//     NewMode are threaded through both halves of the CELT data plane.
//   - Modes with more than 21 bands use mode-sized energy histories and scratch.
//     TestOracleWideBandStatefulParity checks exact packets, final ranges,
//     float PCM, and int16 PCM for mono and stereo histories with reset,
//     periodic concealment, noise concealment, and recovery.
package custom
