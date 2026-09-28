# Codebase parity evidence audit

This audit examines public encode/decode, multistream, optional extensions,
oracle construction, CI selectors, and exactness claims. Its scope is test
validity and coverage: a numerical allowance or omitted assertion is an
**evidence gap**, not proof of a runtime defect. Exact checks require the same
libopus feature set, CPU dispatch, sample format, controls, and decoder history.

## Findings and verification

| Priority | Surface | Finding | Current evidence |
|---|---|---|---|
| P1 | Public FEC robustness | The C probe uses independent decoders while Go retains its primed decoder; accept/count checks omit PCM. | The stereo-coded/mono-API concealment correction passes 128 channel/duration/API cases with exact PCM, ranges, recovery and zero warm allocations in eight local lanes. The persistent FEC probe exposes a separate suffix mismatch still under investigation. |
| P1 | Malformed fixed multistream | A feature-based bypass omits accepted fixed-point PCM; float gates use a coarse tolerance. | Pre-fade SILK capture and per-child integer reconstruction pass all 9,000 mutations in each of the four fixed scalar/SIMD lanes. Zero warm allocations and full PCM/range/PLC/reset regressions pass. SILK and Hybrid multistream final ranges include the redundant CELT contribution at `70be920b`. |
| P1 | Multistream oracle errors | Infrastructure failures can be mistaken for C packet rejection. | The test requires the typed child exit and complete negative decode diagnostic. Build, launch, protocol and forged-marker failures remain errors; classification and mutation gates pass. |
| P1 | Multistream mode changes | A 5 ms window has a gross-error budget and the rest has a numerical tolerance. | Every float bit, including the transition window, matches in all eight local feature/ISA lanes. Exact assertions apply to the full output. |
| P1 | Multistream recovery queue | Correlation and RMS alone do not prove sample equality. | All 17 PLC, FEC, and handover scenarios pass exact full-output checks in all eight local lanes. Quality diagnostics remain additional checks. |
| P1 | Hybrid float output | A cell can compare no Hybrid packets; ARM permits a numerical budget. | Each channel/bandwidth/bitrate cell requires Hybrid coverage and exact output. All eight local lanes pass, including the warm allocation guard. |
| P2 | FEC packet oracle | The helper requests DRED regardless of the Go feature build and lacks matching private-header configuration. | The public feature selector and configured headers pair the builds. Four packet selectors pass all eight local lanes. |
| P2 | Broad decoder differential tests | Normalizing int24 output into float32 can erase integer bits; a magnitude cutoff omits comparisons. | Valid and malformed gates compare original int24 integers. All 1,440 encoder configurations, 4,320 packets and 12,960 format decodes pass in each of eight local lanes. The strict long-stream gate is tracked separately below. |
| P2 | LPCNet/DRED evidence | Predictor/state tests use numerical budgets, and some waveform helpers compare only a common prefix. OSCE-only probes request an extra C DRED feature. | LPCNet helpers use the public feature selector; complete package output/state bit checks pass OSCE, DRED and DRED+OSCE+QEXT scalar/SIMD at `f03fad55`. Public float DRED and original-format int24 checks are under review. |
| P2 | Fixture mode transitions | Live matched-C transition output has only quality assertions, including an architecture-specific floor. | Complete packet-sequence PCM passes exact checks in all eight local lanes. Quality scoring remains independent. |
| P2 | Fixed multistream layout coverage | An unexpected encoder mode can skip a required layout. | Unexpected modes fail. All eight layouts and reset replays execute with exact output in the four fixed local lanes. |
| P2 | Projection decode | Quality checks alone do not establish complete PCM or aggregate final-range equality. | Four-channel and nine-channel mixed-mode receive, loss and reset sequences pass full float-bit checks and the XOR of independently decoded C child ranges in all eight local lanes at `3d652aeb`. |
| P2 | Multistream API rates and gain controls | Live same-format comparisons use numerical allowances. | Receive, requested/overlong/empty PLC, high-gain int16 and three gain-control cases require complete exact output. All eight local lanes pass at `b3488edb` and `4f19e553`. |
| P2 | Private CELT API rates and soft clipping | CELT receive/PLC uses quality-only checks; soft clipping can accept an output prefix. | CELT receive/PLC compares every float bit against the matching private float C API in all eight local lanes. Soft clipping requires equal lengths and exact samples in both default instruction lanes at `0756bfc4`. |
| P2 | SILK CBR floor encode | Packet-byte checks omit the oracle's final range. | All 70 frames require packet and final-range equality; all eight local lanes pass at `38ace4a9`. |
| P1 | CELT encoder sequence | A common-prefix comparison omits packet counts and final ranges; fixed Go builds use a float C reference. | The selected feature/ISA `opus_demo` and public Go int24 API receive identical quantized input and controls. All packets, including the EOF flush, and all ranges match across 19 cases in all eight local lanes at `e359ec6d`; empty streams and records fail. |
| P1 | Long-stream encode/decode | ARM architecture waivers can accept packet, range or PCM mismatches. | The unvoiced SILK SNR source-order correction at `34edbf41` passes 13 configurations × 2,500 frames in both local default instruction lanes: exact packets, ranges, PCM bits and C-reported per-step counts. Empty decode streams fail, and warm encoder allocation guards pass. |

Here, eight local lanes means default, fixed-point, QEXT, and fixed-point+QEXT,
each with scalar and SIMD Go and matching C builds, on ARM64 with Go 1.27.1.
These results do not substitute for native AMD64 execution. The existing native
feature batches include the exact Hybrid, transition, and recovery selectors;
no extra CI jobs are required.

## Open runtime witnesses

In addition to the persistent FEC suffix case above,
mono QEXT SIMD reconstruction and the DRED history audit remain under
investigation. The SILK SNR correction passes the original frame-91 witness
and the strict 2,500-frame matrix. Native AMD64 validates the selected-correlation
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
