# Parity target

gopus targets strong behavioral and audio-quality parity with pinned libopus
1.6.1, with byte-exact guarantees scoped to recorded build configurations and
tests. Universal packet or float-bit identity across compiler targets is not a
release requirement. This contract governs correctness work and review.

## Required guarantees

| Surface | Required result |
|---|---|
| API and packet handling | Matching supported controls, errors, sample counts, durations, framing, reset and recovery behavior, including malformed input. Document intentional Go API mappings. |
| Integer and fixed-point primitives | Exact arithmetic, conversions, rounding, saturation, bitstream syntax and entropy operations for identical inputs and state. |
| Decoder entropy | Exact final range for identical packets, controls and entropy history. Different encoder packets do not imply equal final ranges. Successful normal decoding must agree with the corresponding encoder range; FEC, PLC, reset and malformed-input ranges must match the corresponding C operation. |
| Established exact coverage | Preserve passing packet, range and PCM regressions on their recorded configurations. A failure needs investigation, not a broader tolerance. |
| Floating-point paths | Source-equivalent codec logic and strict quality checks. A demonstrated compiler contraction or reduction-order difference may have a narrow, tested numerical allowance. |
| Runtime | Zero steady-state allocations in the caller-buffer hot paths, bounded memory access, and no panic or corruption on malformed input. |

Both references use the same input, controls, features, scalar widths, compiler
target and effective instruction dispatch: C scalar versus Go scalar, C SIMD
versus Go SIMD. A cross-ISA comparison cannot justify an allowance.

Integer PCM or encoder decisions downstream of an accepted floating-point
difference may differ only within that documented scope. This does not excuse
incorrect integer arithmetic or conversions on identical primitive inputs.

## Accepting a floating-point difference

An unexplained mismatch remains a failure. Acceptance requires:

1. A reproducible first-divergence case tied to the C and Go expressions and
   their actual rounding or reduction order. Rule out wrong widths, casts,
   indexing, initialization, state updates and oracle configuration.
2. A kernel-specific error bound justified by that arithmetic, checked on
   representative and boundary inputs. There is no blanket architecture ULP
   waiver or common-prefix comparison.
3. End-to-end quality and interoperability evidence over the affected modes,
   including repeated frames, transitions, loss and recovery where applicable.
   Decode the same packets with both implementations; when encoder packets
   differ, cross-decode both streams and check their individual ranges. Require
   finite output for valid finite input and no growing history/recovery error
   beyond the documented bound.
4. A reviewed, narrowly scoped regression and an evidence entry naming the
   kernel, build configuration, bound and measured results. Tests must still
   fail outside that scope; no skips or log-only assertions replace checks.

The existing quality floors stay unchanged. `IntentNearExact` requires
`opus_compare` Q >= 20 where applicable, correlation >= 0.997, and RMS ratio
0.98–1.02; waveform-only regions retain the correlation/RMS requirements.
Any stricter case-specific requirement also applies. These are minimum quality
floors, not permission to introduce errors up to those limits. Passing them
alone does not establish a rounding-only cause or byte equality.

## Work and performance priorities

Fix semantic bugs, unsafe behavior, wrong conversions, state divergence and
quality regressions first. Preserve cheap, clear rounding boundaries that match
the source. Avoid additional compiler-specific helpers or hot-loop function
calls solely to reproduce a C compiler's last bit when the allowance above is
proven. Existing fixes need evidence before removal.

Measure correctness fixes and optimizations on matched native builds. A measured
1–2% end-to-end difference is acceptable; do not spend repeated iterations chasing
it. A larger slowdown solely for float-bit identity is not a default tradeoff.
Essential correctness fixes still take priority and their cost is reported.
Diagnostic timings may investigate that cost, but published comparison tables
require the stated correctness contract and retain exact revision/ISA provenance.

## Evidence and enforcement

Reports distinguish **exact**, **validated numerical difference**, **unresolved**,
and **upstream undefined behavior**. A percentage of equal packets measures only
that test corpus; it is not a percentage of codec correctness.

No floating-point allowance is accepted by this document alone. Current v3
packet and PCM mismatches remain unresolved until the evidence above classifies
them. Exact audit tests retain their assertions; a reviewed numerical case needs
an executable bounded check before its exact diagnostic can become non-blocking.
The documented custom-QEXT C history bug remains a separate upstream-UB exception.

### Executable gates

- `TestEncoderCBRPairedOracleContract` runs all 19 CBR cases through independent
  Go/C encoders and persistent decoders. It checks complete output, same-packet
  ranges and quality before rejecting unresolved packet or PCM differences.
  A quality pass alone does not accept a case.
- The manual AMD64 benchmark validator requires all 19 cases, 2,175 encoded
  packets and 76 decode paths per target/instruction lane, with no unresolved,
  failed, skipped or missing contract cases. It runs the contract even when
  exact diagnostics fail.
- v1/v2 exact gates remain mandatory. The v3 CBR exact result is a separately
  recorded diagnostic backed by the contract. The broader 60-case encoder and
  24-case decoder exact selections remain blocking until contract checks cover
  those same VBR, automatic-mode and public-format cases.
- Quality gates reject incomplete or non-finite PCM, invalid profiles and
  failed quality-tool execution. Numerical thresholds retain their values.

See [coverage and outstanding findings](parity-evidence-audit.md),
[kernel/performance evidence](go-simd-kernel-evidence.md), and the
[upstream C boundary](libopus-custom-qext-boundaries.md).
