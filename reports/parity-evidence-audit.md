# Codebase parity evidence audit

This audit examines public encode/decode, multistream, optional extensions,
oracle construction, CI selectors, and exactness claims. Its scope is test
validity and coverage: a numerical allowance or omitted assertion is an
**evidence gap**, not proof of a runtime defect. Exact checks require the same
libopus feature set, CPU dispatch, sample format, controls, and decoder history.

## Findings and verification

| Priority | Surface | Finding | Current evidence |
|---|---|---|---|
| P1 | Public FEC robustness | The C probe uses independent decoders while Go retains its primed decoder; accept/count checks omit PCM. | A persistent C probe reproduces a PCM difference after an exact Hybrid prime and a plain loss call. Runtime investigation is open. |
| P1 | Malformed fixed multistream | A feature-based bypass omits accepted fixed-point PCM; float gates use a coarse tolerance. | Pre-fade SILK capture and per-child integer reconstruction pass all 9,000 mutations in each of the four fixed scalar/SIMD lanes. A compact multiframe witness also checks the redundant CELT contribution to final range; broader range and allocation validation is in progress. |
| P1 | Multistream oracle errors | Infrastructure failures can be mistaken for C packet rejection. | The test distinguishes the helper's explicit negative decode status from build, execution, and protocol failures; broader validation accompanies the fixed multistream repair. |
| P1 | Multistream mode changes | A 5 ms window has a gross-error budget and the rest has a numerical tolerance. | Every float bit, including the transition window, matches in all eight local feature/ISA lanes. Exact assertions apply to the full output. |
| P1 | Multistream recovery queue | Correlation and RMS alone do not prove sample equality. | All 17 PLC, FEC, and handover scenarios pass exact full-output checks in all eight local lanes. Quality diagnostics remain additional checks. |
| P1 | Hybrid float output | A cell can compare no Hybrid packets; ARM permits a numerical budget. | Each channel/bandwidth/bitrate cell requires Hybrid coverage and exact output. All eight local lanes pass, including the warm allocation guard. |
| P2 | FEC packet oracle | The helper requests DRED regardless of the Go feature build and lacks matching private-header configuration. | The public feature selector and configured headers pair the builds. Four packet selectors pass all eight local lanes. |
| P2 | Broad decoder differential tests | Normalizing int24 output into float32 can erase integer bits; a magnitude cutoff omits comparisons. | Single-stream malformed gates preserve original int24 output. The broad differential gate remains under review. |
| P2 | LPCNet/DRED evidence | Predictor/state tests use numerical budgets, and some waveform helpers compare only a common prefix. OSCE-only probes request an extra C DRED feature. | Predictor helpers use the public feature selector; exact two-step output and GRU-state checks pass OSCE and DRED+OSCE scalar/SIMD. Combined QEXT and remaining DRED checks are under review. |
| P2 | Fixture mode transitions | Live matched-C transition output has only quality assertions, including an architecture-specific floor. | Complete packet-sequence PCM passes exact checks in all eight local lanes. Quality scoring remains independent. |
| P2 | Fixed multistream layout coverage | An unexpected encoder mode can skip a required layout. | Unexpected modes fail. All eight layouts and reset replays execute with exact output in the four fixed local lanes. |

Here, eight local lanes means default, fixed-point, QEXT, and fixed-point+QEXT,
each with scalar and SIMD Go and matching C builds, on ARM64 with Go 1.27.1.
These results do not substitute for native AMD64 execution. The existing native
feature batches include the exact Hybrid, transition, and recovery selectors;
no extra CI jobs are required.

## Open runtime witnesses

In addition to the Hybrid loss and malformed fixed multistream cases above,
mono QEXT second-frame reconstruction and long-running SILK CBR packet equality
remain under investigation. Native AMD64 validates the selected-correlation
correction: all seven LACE/NoLACE/BWE cases pass both instruction lanes across
OSCE, OSCE+QEXT and DRED+OSCE+QEXT at `905eec03`.

The [kernel and end-to-end evidence report](go-simd-kernel-evidence.md) records
measured coverage, revisions, and all 53 replacement routines. Complete codec
byte/sample parity remains unproven.

## Deliberate boundaries

Real-audio quality and perceptual loss tests remain useful independent evidence;
they do not count as exactness proof. Synthetic decay checks also have a distinct
purpose from a live C output comparison. The only documented unsafe C-reference
boundary is the [custom-QEXT history access](libopus-custom-qext-boundaries.md);
malformed packets alone do not justify excluding supported, defined C output.
