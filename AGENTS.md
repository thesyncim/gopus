# Gopus Agent Context (Concise)

Canonical project context for agent sessions.

## Project
- **Gopus** is a pure Go implementation of Opus (RFC 6716).
- **C reference (required for parity/debugging):** `tmp_check/opus-1.6.1/` (libopus 1.6.1).

## Current Snapshot (2026-02-10)
- Decoder: complete and stable across CELT/SILK/Hybrid, stereo, and sample rates.
- Encoder: complete feature surface (CELT/SILK/Hybrid, FEC/LBRR, multistream, ambisonics, controls).
- Allocations: zero allocs/op in encoder and decoder hot paths.
- FinalRange/testvector decoding baseline: stable and validated.

### Latest parity/compliance checks
- `TestSILKParamTraceAgainstLibopus`: **PASS** with exact SILK-WB trace parity on canonical 50-frame fixture.
  - Gain index avg abs diff: `0.00`
  - LTP scale mismatch: `0/50`
  - NLSF interp mismatch: `0/50`
  - PER mismatch: `0/50`
  - Pitch lag/contour mismatch: `0/50`
  - LTP index mismatch: `0/200`
  - Signal type mismatch: `0/50`
  - Seed mismatch: `0/50`
- `TestEncoderComplianceSummary`: **PASS** (`19 passed, 0 failed`).
  - Current compliance status: "GOOD" against libopus fixtures across tested CELT/SILK/Hybrid profiles.
  - Known remaining gap: strict production threshold (`Q >= 0`, ~48 dB SNR) is still not met in all profiles.

## Current Priorities
1. Raise absolute encoder quality toward strict production target (`Q >= 0`) while keeping parity with libopus behavior.
2. Focus tuning on SILK/Hybrid speech-bitrate quality and CELT short-frame edge cases.
3. Preserve zero-allocation guarantees in all real-time encode/decode paths.

## Verified Areas (Do Not Re-Debug First)
- SILK decoder correctness path (focus issues on encoder unless evidence says otherwise).
- Resampler parity path used for SILK/hybrid downsampling.
- CWRS sign handling, MDCT/IMDCT roundtrip, and energy coding roundtrip.
- NSQ constant-DC amplitude behavior (~0.576 RMS ratio) is expected dithering behavior, not a defect.

## Implementation Rules
- Always cross-check codec math/bitstream decisions against libopus C sources first.
- Prefer targeted parity tests before broad refactors.
- API direction is zero-allocation caller-owned buffers:
  - `func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)`
  - `func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)`
- Avoid introducing allocation-heavy convenience wrappers in hot paths.

## Key Paths
- Core encoder: `encoder/`
- SILK: `silk/`
- CELT: `celt/`
- Hybrid bridge: `encoder/hybrid.go`, `hybrid/`
- Test vectors/parity/compliance: `testvectors/`
- libopus reference: `tmp_check/opus-1.6.1/`

## Fast Commands
```bash
# Full tests
go test ./... -count=1

# SILK trace parity vs libopus
go test ./testvectors -run TestSILKParamTraceAgainstLibopus -count=1 -v

# Encoder compliance summary vs fixtures
go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v

# Allocation checks
go test -bench=. -benchmem ./...
```

## Commit Rules
- Do not mention Codex/Claude/AI in commit messages.
- No `Co-Authored-By` AI attribution.
- Use conventional commit style (`type(scope): description`).
