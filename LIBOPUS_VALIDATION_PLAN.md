# Libopus Validation Plan

Reference baseline: `tmp_check/opus-1.6.1` (libopus 1.6.1). This is treated as
the ground truth for behavior (including the libwebrtc integration path).

Legend: [x] validated vs libopus, [!] mismatch/suspect, [ ] pending

## Acceptance Criteria
- Reference build: libopus float build from `tmp_check/opus-1.6.1` (same inputs,
  same config, same packet loss pattern).
- Bit-exact components (range coder, tables, window): exact value match or
  integer bit-for-bit match.
- Transform parity (MDCT/IMDCT, DFT): max abs diff <= 1e-6 and correlation
  >= 0.999999 vs libopus float reference.
- Decoder parity: per-frame PCM max abs diff <= 1e-6 and SNR >= 90 dB vs libopus
  float output; compliance vectors must pass libopus thresholds and match SNR
  within 0.1 dB where we track it.
- Encoder parity: for deterministic settings, encoded payload bytes and packet
  sizes must match libopus; otherwise decoded PCM must still meet decoder parity
  (no regressions allowed).

## Shared / Utilities
- [x] Vorbis window (overlap-based) values match `static_modes_float.h`.
- [x] IMDCT + TDAC overlap for overlap=120 matches libopus output.
- [ ] MDCT forward (analysis) for short frame sizes (120/240) vs libopus.
- [x] Range coder (entdec/entenc) bit-exactness vs libopus -> `internal/rangecoding/entropy_libopus_test.go`
- [x] Tables (bands, allocation, pulse cache) parity vs libopus -> `internal/celt/libopus_tables_test.go`

## CELT Decoder
- [!] PVQ band decode / quant_bands for short frames in stereo non-transient
  (testvector07 SNR is low; likely small-band path such as N==2).
- [ ] Stereo coupling (intensity + mid/side + sign handling) vs libopus.
- [ ] Energy decode (coarse/fine/amp) vs libopus.
- [x] Time/frequency resolution (TF) decode vs libopus -> `internal/celt/tf_libopus_test.go`
- [ ] Transient short-block handling (LM/B/interleave) vs libopus.
- [ ] Anti-collapse vs libopus.
- [ ] Postfilter (comb filter) vs libopus.
- [ ] Silence / packet loss behavior vs libopus.
- [ ] PLC behavior vs libopus.

## CELT Encoder
- [ ] Transient detection / short-block selection vs libopus.
- [ ] TF analysis vs libopus.
- [ ] Energy quantization (coarse/fine) vs libopus.
- [ ] PVQ encoding / pulse allocation / stereo coupling vs libopus.
- [ ] Postfilter control vs libopus.
- [ ] MDCT forward short-overlap path vs libopus.

## SILK / Hybrid (if in scope)
- [!] SILK decoder vs libopus -> Tables and unit tests pass; integration tests fail (Q=-137 to -151).
  Root cause: state management across frames (LTP buffer, gain state, output buffer persistence).
- [ ] SILK encoder vs libopus.
- [ ] Hybrid switching (CELT <-> SILK) vs libopus.

## Test Vectors / Compliance
- [!] `testvector07` (CELT mixed mono/stereo) currently failing.
- [ ] Re-run all other vectors and record pass/fail status.

## C Tests to Port (Basic Coverage)
- [ ] `celt/tests/test_unit_mdct.c` (MDCT/IMDCT sanity)
- [x] `celt/tests/test_unit_dft.c` (DFT correctness) -> `internal/celt/dft_unit_test.go` (SNR > 250 dB)
- [x] `celt/tests/test_unit_rotation.c` (rotation/inversion) -> `internal/celt/rotation_unit_test.go`
- [x] `celt/tests/test_unit_cwrs32.c` (CWRS/pulse enumeration) -> `internal/celt/cwrs_unit_test.go`
- [x] `celt/tests/test_unit_entropy.c` (range coder parity) -> `internal/rangecoding/entropy_libopus_test.go`
- [x] `celt/tests/test_unit_laplace.c` (Laplace coder) -> `internal/celt/laplace_unit_test.go` (10000 values roundtrip pass)
- [ ] `tests/test_opus_decode.c` (decoder API + edge cases)
- [ ] `tests/test_opus_encode.c` (encoder API + constraints)
