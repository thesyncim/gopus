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
//   - Genuinely custom band layouts outside that family (e.g. 48000/640,
//     NbEBands=19) are also encoded and decoded byte/sample-identically to
//     libopus --enable-custom-modes: the per-mode band tables (eBands, widths,
//     logN, allocVectors and the compute_pulse_cache index/bits/caps) computed by
//     NewMode are threaded through both halves of the CELT data plane.
//   - The native DECODE plane reproduces libopus sample-for-sample across the
//     whole non-standard space within the native band-cap (every short-block
//     decomposition LM 0..3, 8k..96k, mono and stereo), verified by
//     TestOracleDecodeParityBroadSweep.
//
// Band-cap boundary: non-standard modes whose compute_ebands band count exceeds
// the native data-plane capacity (NbEBands > 21, which occurs at high sample
// rates with a small short-MDCT, e.g. 32000/100 or 44100/120 -> 22 bands) are
// declined by NewEncoder and NewDecoder with ErrNonStandard. The static gopus
// energy/history buffers are sized by MaxBands, so these wider layouts cannot be
// driven byte-exact yet; declining them keeps the boundary clean rather than
// emitting a non-conformant bitstream. libopus accepts them because its CELTMode
// buffers are sized by the mode's own nbEBands; see
// TestOracleNonStandardBandCapDeclined.
package custom
