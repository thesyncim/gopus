# Codebase parity evidence audit

This audit examines public encode/decode, multistream, optional extensions,
oracle construction, CI selectors, and exactness claims. Its scope is test
validity and coverage: a numerical allowance or omitted assertion is an
**evidence gap**, not proof of a runtime defect. Exact checks require the same
libopus feature set, CPU dispatch, sample format, controls, and decoder history.

## Findings and verification

| Priority | Surface | Finding | Current evidence |
|---|---|---|---|
| P1 | Public FEC robustness | Independent decoder sessions cannot prove persistent FEC history; accept/count checks omit PCM. | The stereo-coded/mono-API concealment correction passes 128 channel/duration/API cases with exact PCM, ranges, recovery and zero warm allocations in eight local lanes. The 2.5 ms Hybrid-to-SILK CELT overlap correction passes 16 transition cases in all eight lanes at `2f334bed`. The strict persistent mutation sweep passes all 8,000 sequences in six local float/fixed/QEXT scalar/SIMD lanes at `a8cdf338`, with exact prime/FEC PCM and ranges and no skipped primes. Both rate-switch witnesses also pass normal recovery and following PLC. |
| P1 | Malformed fixed multistream | A feature-based bypass omits accepted fixed-point PCM; float gates use a coarse tolerance. | Pre-fade SILK capture and per-child integer reconstruction pass all 9,000 mutations in each of the four fixed scalar/SIMD lanes. Zero warm allocations and full PCM/range/PLC/reset regressions pass. SILK and Hybrid multistream final ranges include the redundant CELT contribution at `70be920b`. |
| P1 | Multistream oracle errors | Infrastructure failures can be mistaken for C packet rejection. | The test requires the typed child exit and complete negative decode diagnostic. Build, launch, protocol and forged-marker failures remain errors; classification and mutation gates pass. |
| P1 | Multistream mode changes | A 5 ms window has a gross-error budget and the rest has a numerical tolerance. | Every float bit, including the transition window, matches in all eight local feature/ISA lanes. Exact assertions apply to the full output. |
| P1 | Multistream recovery queue | Correlation and RMS alone do not prove sample equality. | All 17 PLC, FEC, and handover scenarios pass exact full-output checks in all eight local lanes. Quality diagnostics remain additional checks. |
| P1 | Hybrid float output | A cell can compare no Hybrid packets; ARM permits a numerical budget. | Each channel/bandwidth/bitrate cell requires Hybrid coverage and exact output. All eight local lanes pass, including the warm allocation guard. |
| P2 | FEC packet oracle | The helper requests DRED regardless of the Go feature build and lacks matching private-header configuration. | The public feature selector and configured headers pair the builds. Four packet selectors pass all eight local lanes. |
| P2 | Broad decoder differential tests | Normalizing int24 output into float32 can erase integer bits; a magnitude cutoff omits comparisons. | Valid and malformed gates compare original int24 integers. All 1,440 encoder configurations, 4,320 packets and 12,960 format decodes pass in each of eight local lanes. The strict long-stream gate is tracked separately below. |
| P2 | LPCNet/DRED evidence | Predictor/state tests use numerical budgets, and some waveform helpers compare only a common prefix. OSCE-only probes request an extra C DRED feature. | LPCNet helpers use the public feature selector; complete package output/state bit checks pass OSCE, DRED and DRED+OSCE+QEXT scalar/SIMD at `f03fad55`. The 132 edited public DRED gates pass all eight local DRED feature/ISA lanes with matching float priming and actual int24 output. Independent nil-FEC, tiny-FEC and complexity-change history witnesses also pass exactly; durable FEC integration passes at `b29fcff7`. Native early artifact `11006418166` at `55b13f5f` passes the included neural PCM/range checks on both Go instruction lanes, including all eight LACE/NoLACE feature/ISA combinations. Its only reported blocker is one allocation in `TestDecoderNoSidecarDeepPLCWarmZeroAllocs` at 16 kHz/complexity 5 in two feature lanes; the allocation fix and full CI result are tracked below. |
| P2 | Fixture mode transitions | Live matched-C transition output has only quality assertions, including an architecture-specific floor. | Complete packet-sequence PCM passes exact checks in all eight local lanes. Quality scoring remains independent. |
| P2 | Fixed multistream layout coverage | An unexpected encoder mode can skip a required layout. | Unexpected modes fail. All eight layouts and reset replays execute with exact output in the four fixed local lanes. |
| P2 | Projection decode | Quality checks alone do not establish complete PCM or aggregate final-range equality. | Four-channel and nine-channel mixed-mode receive, loss and reset sequences pass full float-bit checks and the XOR of independently decoded C child ranges in all eight local lanes at `3d652aeb`. |
| P2 | Multistream API rates and gain controls | Live same-format comparisons use numerical allowances. | Receive, requested/overlong/empty PLC, high-gain int16 and three gain-control cases require complete exact output. All eight local lanes pass at `b3488edb` and `4f19e553`. |
| P2 | Private CELT API rates and soft clipping | CELT receive/PLC uses quality-only checks; soft clipping can accept an output prefix. | CELT receive/PLC compares every float bit against the matching private float C API in all eight local lanes. Soft clipping requires equal lengths and exact samples in both default instruction lanes at `0756bfc4`. |
| P2 | SILK CBR floor encode | Packet-byte checks omit the oracle's final range. | All 70 frames require packet and final-range equality; all eight local lanes pass at `38ace4a9`. |
| P1 | CELT encoder sequence | A common-prefix comparison omits packet counts and final ranges; fixed Go builds use a float C reference. | The selected feature/ISA `opus_demo` and public Go int24 API receive identical quantized input and controls. All packets, including the EOF flush, and all ranges match across 19 cases in all eight local lanes at `e359ec6d`; empty streams and records fail. |
| P1 | Long-stream encode/decode | ARM architecture waivers can accept packet, range or PCM mismatches. | The unvoiced SILK SNR source-order correction at `34edbf41` passes 13 configurations × 2,500 frames in both local default instruction lanes: exact packets, ranges, PCM bits and C-reported per-step counts. Empty decode streams fail, and warm encoder allocation guards pass. |
| P1 | QEXT multiframe decode | Go-generated expected output cannot establish independent C equality; mono SIMD cubic reduction must match selected C rounding. | Independent selected-C combined/separate packet sequences match every float bit and range for mono/stereo. The cubic correction and 11-size boundary grid pass exact and zero-allocation gates at `77cb9a9d`. |
| P2 | Automatic encoder modes | Mode-only comparisons omit packet bytes and final ranges. | The C helper records every packet/range for 432 configurations × 10 frames. All eight local lanes pass at `a0a9c877`. |
| P2 | Multistream short-input encode | Float-input packet evidence does not cover the public int16 API. | Five layouts × six stateful frames compare actual C short-input packets/ranges; all eight local lanes pass at `73347769`. |
| P2 | Legacy ARM CELT gates | A shared architecture skip omits exact kernel and self-equivalence checks; two private C helpers omit QEXT feature selection. | Sixteen focused checks pass all eight local lanes without skips at `ba23ba99`. VQ/partition helpers use the selected float feature archive and `opus_select_arch()` for RTCD calls. Native AMD64 validation is pending. |
| P2 | Custom control oracle | Fixed coefficients require their C Q scales, and declared-supported geometries must fail on unexpected oracle rejection. | The full custom package passes all eight local lanes at `00eeb471`; all five scaled-band modes retain mono/stereo coverage. Rejected 44.1 kHz/882-sample geometry has independent C/Go error checks. |
| P2 | Multistream clipping lifecycle | Projection loss applies clipping, reset retains child clipping memory, and a successful float call leaves ordinary int16 clipping memory stale. | Public float/int16/int24 transition, projection reset, and loss/recovery sequences match selected C PCM and ranges in all eight local lanes at `d69ff3b1`. Clipping state belongs to each elementary decoder. |
| P2 | Multistream constructors | Zero sample rate panics and unsupported rates pass through; projection channels require validation before allocation. | Rate/channel rejection and supported-rate construction pass all eight local lanes at `d69ff3b1`. Tagged 96 kHz acceptance remains supported; its runtime gap is tracked below. |
| P1 | Malformed Hybrid main length | Invalid redundancy can leave entropy storage intact while the logical main length becomes zero; decoding CELT from storage consumes invalid payload. | Explicit main-length propagation selects highband-only concealment and zero outer final range. Two 10 ms witnesses and one 20 ms witness, recovery/loss, all three sample formats, and native 96 kHz pass in all eight local lanes with zero warm allocations. Projection checks cover 4,000 malformed packets and 12,000 random buffers per lane; accepted output is exact in float/int16/int24, and oracle infrastructure failures remain errors. |
| P2 | Rectangular projection | More matrix columns than output channels bypass float/int16 demixing and fail int24 decoding. | Two rectangular layouts, received/lost/recovered packets and all three output formats match selected C in all eight local lanes at `0694ad51`; warmed caller-buffer decoding allocates zero. |
| P1 | Native 96 kHz multistream | The subpackage initializes elementary codecs at 48 kHz despite accepting 96 kHz in QEXT builds. | Native geometry and selected-C PLC rounding pass exact float/int16/int24 output, ranges and zero warm allocations at `3885a34d`. Encode packets, durations and ranges pass mono 2.5/5/10/20 ms plus 20 ms coupled/discrete stereo float/int16, QEXT off/on, in all four float/fixed-QEXT scalar/SIMD lanes at `5a60c58b`. |

Here, eight local lanes means default, fixed-point, QEXT, and fixed-point+QEXT,
each with scalar and SIMD Go and matching C builds, on ARM64 with Go 1.27.1.
These results do not substitute for native AMD64 execution. The existing native
feature batches include the exact Hybrid, transition, and recovery selectors;
no extra CI jobs are required.

## Feature integration checks

The neural PLC unit test enables complexity 5 at `284b9189`; complexity 0
uses classical PLC, whose retained-history cursor has different semantics.
LACE remains active across SILK/Hybrid packets, and CELT preserves its SILK
filter state. Unit checks and an independent four-frame exact C sequence pass
all eight local OSCE feature/ISA lanes at `2b4e2b23`.

Multistream DRED recovery offsets advance only for active sidecars at
`90f93ef0`. Main-model PLC still runs for every eligible child. The unchanged
sidecar isolation/queue assertions and exact C PCM/range/state checks pass six
focused local feature/ISA lanes. Late OSCE model checks distinguish retained
feature history from loaded weights.

Reference validation uses each selected builder's actual stamp contract at
`7b33b0b3`. Wrong-ISA and stale stamps remain errors. DNN helpers include the
pinned source root after the generated build configuration; the affected CBR
and QEXT helpers compile and pass their focused exact checks. Native CI
checks in artifact `11006418166` at `55b13f5f` pass their exactness cases.

PLC state interfaces require their history and filter methods at
`a6081168`. Go 1.27.1 can lazily allocate an interface-assertion cache;
allocation profiling records a 48-byte cache allocation inside SILK
concealment. The five PLC capability assertions are absent from the hot
paths, and compiled AMD64 concealment has no `runtime.typeAssert` calls.
The unchanged single-call zero-allocation and exact-output tests pass local
scalar DRED and scalar/SIMD DRED+OSCE+QEXT. Native execution of this fix is
pending; the failing `55b13f5f` artifact remains part of the evidence.

## Runtime parity evidence

The persistent malformed FEC sweep passes 8,000 sequences per lane at
`a8cdf338`: default, fixed-point and float-QEXT, each scalar/SIMD. The default
lanes contain 6,715 matching accepted FEC calls and 1,285 matching rejections,
with no skipped primes. Coded-channel transitions, per-frame resampling and
PLC's rate-reset signal type have exact recovery regressions. DRED history
passes 132 edited tests in all eight local feature/ISA lanes. Root and
multistream OSCE automatic-loss/recovery matrices each pass all eight local
lanes. Durable FEC history passes all eight local DRED feature/ISA lanes at
`b29fcff7`. Mixed missing-LBRR, outer PLC prefixes, tiny FEC payloads, model
reload and complexity-change history pass all four combined DRED/OSCE lanes,
including warm zero-allocation guards. Native AMD64 artifact `11006418166` at `55b13f5f` passes the included exactness cases. Mono QEXT SIMD reconstruction passes its
selected-C primitive and public-sequence gates at `77cb9a9d`. The SILK SNR correction passes the original frame-91 witness
and the strict 2,500-frame matrix. Native AMD64 validates the selected-correlation
correction: all seven LACE/NoLACE/BWE cases pass both instruction lanes across
OSCE, OSCE+QEXT and DRED+OSCE+QEXT at `905eec03`.

The [kernel and end-to-end evidence report](go-simd-kernel-evidence.md) records
measured coverage, revisions, and all 53 replacement routines. Complete codec
byte/sample parity remains unproven.

Multistream long-burst PLC state, native 96 kHz crossfade stride, and Hybrid
QEXT payload routing pass all eight applicable local lanes at `3885a34d`.
The wide-band QEXT decoder consumes all signaled bands while rendering only
the physical spectrum; its exact PCM/range, stereo synthesis-stage and zero
warm allocation gates pass at `28cb897e`, together with integer-format clipping
lifecycle checks.

Native early artifact `11006418166` at `55b13f5f` records 93 successful phase
exits, two nonzero warm-allocation phase exits, and aggregate exit 1. The two
failures are the one-allocation no-sidecar PLC guard described above; the
artifact's PCM, range, and exactness checks pass. Full run
[36500305019](https://github.com/thesyncim/gopus/actions/runs/36500305019) is
canceled; the full A/B sweep remains incomplete. Independent nil-FEC, tiny-FEC and complexity 0→5 history
witnesses pass exact counts, PCM bits and ranges through recovery. Non-fullband
QEXT header/refinement matches selected C at `4186cbb3`, including the vector
body and scalar tail. SILK pitch reporting
uses rate-reset state at `949ca0f3`; comfort-noise excitation history survives
rate changes at `1d99069e`, with eight exact public sequences and zero warm
allocations in float/fixed scalar/SIMD. The custom wrapper matches default packet
signalling, finite header budgets, constructor controls, and error/reset state
at `17e27246`; its complete package passes all eight local lanes. It is not
called by root or multistream public APIs.
Mono-to-stereo recovery preserves independent channel history at `a676f2db`: root
and multistream pass all eight local lanes, all three output formats, 0/1/2/6/15
losses and every supported API rate, with zero warm caller-buffer allocations.
Empty repeat markers preserve following multistream QEXT payloads at `e404dcf6`;
CELT/Hybrid mono/stereo recovery sequences pass all four QEXT feature/ISA lanes.

CELT coarse-energy trial snapshots reserve the base-packet capacity at
`f093c171`. Increasing packet budgets reuse that storage without allocations;
the focused allocation guard and full CELT package pass scalar and SIMD.
The native 96 kHz variable-budget reproducer also passes exact packet/range
and zero warm allocation checks in both instruction lanes. This storage fix
has separate mode-selection evidence below.

Fixed-point CELT energy trials reserve both range-coder snapshots to the mode
packet capacity at `2eaeab92`. Increasing-budget allocation, coarse-energy,
CBR/VBR encode and native 96 kHz QEXT oracle checks pass all four applicable
local feature/ISA lanes. The native 96 kHz multistream variable-budget witness
matches C packets/ranges and allocates zero after warmup. These checks cover
storage ownership; shared encoder mode selection is validated separately below.

SILK PLC checks the internal rate before both decoded-frame updates and
concealment at `170fd3b9`, and starts random-scale state at the C decoder
initialization value. The Hybrid-to-SILK FEC rate-change regression compares
exact PCM, counts and ranges for narrow/medium/wide bands and short/long loss
requests. It and the zero-allocation guard pass local float/fixed scalar/SIMD
lanes. The persistent malformed-FEC witnesses and their recovery sequences
pass at `a8cdf338`.

Native 96 kHz root and multistream encoding share mode selection, input
history and the SILK/Hybrid/CELT frame driver at `a86354c6`. Four local
float/fixed-QEXT scalar/SIMD lanes pass C packet/range and warm allocation
checks for QEXT off/on, fresh/primed low-budget sequences and packets through
40 ms. The internal CELT gate compares the actual staged input with C.
Multistream uses the shared analysis and short-input projection route at
`cb821a49`; 48/96 kHz projection packet/range and zero-allocation checks pass
the applicable float/fixed-QEXT scalar/SIMD lanes. Native AMD64 artifact `11006418166` at `55b13f5f` passes these included
exactness cases.

## Deliberate boundaries

Real-audio quality and perceptual loss tests remain useful independent evidence;
they do not count as exactness proof. Synthetic decay checks also have a distinct
purpose from a live C output comparison. The only documented unsafe C-reference
boundary is the [custom-QEXT history access](libopus-custom-qext-boundaries.md);
malformed packets alone do not justify excluding supported, defined C output.
