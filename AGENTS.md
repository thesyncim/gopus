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

## Session Quick Start
- Read in order before new investigation:
  1. `AGENTS.md`
  2. `.planning/ACTIVE.md`
  3. `.planning/DECISIONS.md`
  4. `.planning/WORK_CLAIMS.md`
- Run `make agent-preflight` before tests or edits.
- In first response, state:
  - what will be skipped from re-validation (and why),
  - what focused test slice runs first,
  - when broad gate `make verify-production` will be run.

### Effective Use (Natural)
- Keep kickoff messages short. Example: "Use AGENTS + planning files, run preflight, claim scope if parallel, then start with one narrow test."
- Avoid long pasted templates. One clear sentence of intent plus one concrete scope is enough.
- Ask for one decision at a time (for example: "optimize SILK gain path first" vs "improve all quality").
- Prefer short loops: focused test -> edit -> focused re-test -> brief evidence note.

## Parallel Agent Workflow
- Claim surfaces before edits:
  - `make agent-claim AGENT=<name> PATHS='silk/,testvectors/' NOTE='short scope note'`
- Preferred claim surfaces: `encoder/`, `silk/`, `celt/`, `hybrid/`, `testvectors/`, `tools/`, root docs.
- Avoid overlapping active claims unless coordinated.
- Release claims when done:
  - `make agent-release CLAIM_ID=<id>`

## Memory Discipline
- Update `.planning/ACTIVE.md` evidence log for meaningful hypothesis/result steps.
- Record durable keep/skip decisions in `.planning/DECISIONS.md` with explicit `do_not_repeat_until`.
- Keep active debug notes scoped to current blockers; move resolved deep-dives into `.planning/debug/resolved/`.

## CI Regression Guardrails (Mandatory)
- Treat CI as merge-blocking for correctness and performance; do not bypass failing checks.
- Before proposing merge-ready changes, run:
  - `make verify-production`
  - `make bench-guard`
- If a change is performance-sensitive (encoder/decoder hot path, SILK/CELT/Hybrid core, resamplers, packet loops), include benchmark guard evidence in the PR notes.
- Never relax benchmark thresholds in `tools/bench_guardrails.json` without:
  - measured evidence from `make bench-guard`,
  - a short rationale in the PR/commit message,
  - explicit reviewer sign-off for the threshold change.
- Never disable parity/race/fuzz guard targets to make CI pass; fix root causes or document a scoped, temporary exception with owner and expiry.

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

# Benchmark guardrails (CI perf gate)
make bench-guard
```

## Commit Rules
- Do not mention Codex/Claude/AI in commit messages.
- No `Co-Authored-By` AI attribution.
- Use conventional commit style (`type(scope): description`).
