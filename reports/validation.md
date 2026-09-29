# Validation reference

This reference records the correctness contract, tested configurations, upstream
boundaries and performance evidence for the Go codec kernels. Results apply to
the named revisions and build lanes; they are not a universal byte-parity claim.

- [Parity contract](#parity-contract)
- [Coverage](#coverage)
- [Reference boundary](#reference-boundary)
- [Performance](#performance)
- [Compiler code generation](#compiler-code-generation)

## Parity contract

gopus targets strong behavioral and audio-quality parity with pinned libopus
1.6.1, with byte-exact guarantees scoped to recorded build configurations and
tests. Universal packet or float-bit identity across compiler targets is not a
release requirement. This contract governs correctness work and review.

### Required guarantees

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

### Accepting a floating-point difference

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

### Work and performance priorities

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

### Evidence and enforcement

Reports distinguish **exact**, **validated numerical difference**, **unresolved**,
and **upstream undefined behavior**. A percentage of equal packets measures only
that test corpus; it is not a percentage of codec correctness.

No floating-point allowance is accepted by this document alone. Current v3
packet and PCM mismatches remain unresolved until the evidence above classifies
them. Exact audit tests retain their assertions; a reviewed numerical case needs
an executable bounded check before its exact diagnostic can become non-blocking.
The documented custom-QEXT C history bug remains a separate upstream-UB exception.

#### Executable gates

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

## Coverage

The subsequent [native audit at `cd2c6c15`](https://github.com/thesyncim/gopus/actions/runs/36567216398)
finishes with a failure. Its status does not accept or classify the remaining v3
packet and PCM differences. No validated numerical allowance is recorded here.


The [parity target](#parity-contract) defines required behavior, scoped exactness
and the evidence needed to accept a numerical difference. No current v3 mismatch
is accepted solely because it appears small or originates in floating-point code.

This audit examines public encode/decode, multistream, optional extensions,
oracle construction, CI selectors, and exactness claims. Its scope is test
validity and coverage: a numerical allowance or omitted assertion is an
**evidence gap**, not proof of a runtime defect. Exact checks require the same
libopus feature set, CPU dispatch, sample format, controls, and decoder history.

### Public API boundary audit

The additional public-boundary checks cover ten concrete mismatches against
selected libopus 1.6.1. Scalar Go uses scalar C; SIMD Go uses the matching C
instruction lane. Each failure sequence includes following state or recovery
output so matching rejection alone cannot hide a state divergence.

| Surface | Required behavior | Independent regression |
|---|---|---|
| Multistream int24 at 96 kHz | Both constructors reserve 120 ms of native-rate conversion storage. | Mono/stereo, 80/100/120 ms, silence and signed 24-bit input compare packets/ranges and warm allocations. |
| Failed forced-stereo broadcast | Controls apply to children in order; an earlier coupled child retains its update when a mono child rejects. | Per-child controls, packets/ranges, reset and recovery at complexities 7/9/10. |
| Multistream application after minimal packets | Application remains mutable while no child has committed its first frame. | Mono and coupled-plus-mono low-budget packets, subsequent controls and full packets/ranges. |
| Native 96 kHz frame sizes | Validate the API-rate duration before converting wrapper bookkeeping to 48 kHz. | All nine legal durations and adjacent invalid sizes, unchanged state and following packets/ranges. |
| Malformed framing with short decode output | Structural packet errors precede insufficient output capacity. | Stateful float/int16/int24 rejection and recovery at standard rates and native 96 kHz. |
| Malformed framing with zero or partial-channel output | C rejects zero complete samples per channel before parsing; the Go facade returns `ErrBufferTooSmall` when output has fewer than one complete channel tuple. | Float/int16/int24 malformed and valid packets check error mapping, final range and recovery at standard rates and native 96 kHz. |
| Encoder final range after rejection | The selected float/fixed public input wrapper determines whether an invalid frame clears the range. | All three input APIs, invalid duration, empty output, combined errors and re-primed recovery at 48/96 kHz. |
| 100 ms encoder packet budget | A one-byte budget returns `OPUS_BUFFER_TOO_SMALL`; a two-byte budget may produce the short packet, and selected duration controls the guard. | Strict C comparisons cover mono/stereo float32/int16/int24 sequences, one-byte rejection recovery, two-byte output and selected 100/20 ms durations; QEXT covers 96 kHz. |
| Multistream invalid frame sizes | Reject unsupported or overflowing frame sizes before budget checks and int16 conversion scratch. | Live-C comparisons cover legal/invalid sizes across API rates and restricted-SILK mode; local checks verify early errors leave output and conversion scratch untouched. |
| Fixed-point tonality analysis | Analysis starts at complexity 10 in fixed builds and 7 in float builds. | Initial packets and following control sequences at complexities 7, 9 and 10. |

The existing integrated boundary/control/allocation selection passes all
eight local ARM64 lanes: float/fixed × QEXT off/on × scalar/SIMD. The combined
zero/partial-channel decode, 100 ms encode-budget and multistream frame-size
regressions also pass all eight strict-oracle lanes, with no failed or skipped
cases. The root package and internal encoder pass their broader default suites;
the internal encoder also passes nosimd/SIMD, multistream passes default/SIMD,
and the root fast suite passes SIMD. Root, internal encoder and multistream SIMD lint report no issues. The encode differential gate additionally requires every supported
configuration and every frame's return, packet and final range, including empty
output. Its 1,788 configurations
× eight frames × eight lanes yield 114,432 passing frame comparisons. The CTL
sequence gate compares PROCESS/RESET results and all selected GET values,
including final range and DTX, with explicit matching initial bitrate controls.
Analysis-reuse tests explicitly select complexity 10; undersized-budget and
DTX cadence oracles also apply the same complexity to Go and C. All six DTX
cadence cases pass across the eight feature/ISA lanes. The existing native
feature batches include these boundary and DTX tests; native AMD64 validation
of this audit is pending. Earlier native results below retain their
measured revisions and do not stand in for this patch.

### Ogg container validation

The reader reports checksum and capture-pattern errors without reading an
unbounded malformed tail. Nonempty truncated pages return their parse error;
a clean end of stream returns EOF. `TestReaderReadPageReturnsPermanentErrorsPromptly`,
`TestReaderReadPageDistinguishesTruncationFromEOF` and `TestReaderReadPageAcceptsFragmentedPages`
check these states, bytes consumed and fragmented valid input.

Header parsing and writer construction widen stream/coupled counts before
validating the 255-channel limit, including projection layouts. The layout
boundary regressions (`TestParseOpusHeadDecodedChannelBudget` and
`TestNewWriterWithConfigDecodedChannelBudget`) cover totals 255, 256 and 300, the silence sentinel,
projection matrix framing and rejection before writer output. These are
container validation checks, not additional C codec byte-parity evidence.

### Findings and verification

Exact equality to a configured libopus build is a stronger requirement than
[Opus conformance](https://www.rfc-editor.org/rfc/rfc6716.html#section-6).
Encoder packet choices and small decoder numerical differences can satisfy
the standard. This report tracks exact-reference failures separately from
conformance and audio-quality evidence; a byte mismatch alone does not establish
invalid Opus output or audible degradation.

The opt-in [compiler-target audit](#amd64-compiler-targets)
at `bc5ddaeb` passes all selected default-float v1/v2 scalar/SIMD checks. At v3,
scalar encode/decode cases pass 40/60 and 8/24; SIMD cases pass 24/60 and 6/24.
CBR exact cases are 8/19 scalar and 1/19 SIMD, with 432/382 and 681/517
packet/range differences out of 2,175 respectively. All warm allocation checks
pass. At `834221f9`, all six FFT/MDCT live-C suites pass in both v3 modes,
as do the strengthened SILK LPC/window/gain and CELT log2/angle-math checks.
Scalar and SIMD pitch, band energy, rotation and unquantization checks pass.
CBR cases in that focused run pass 14/19 scalar (68 packet/61 range differences)
and 6/19 SIMD (321 packet/196 range differences), each out of 2,175 packets.
Encoder packet/range and public decoder PCM differences remain open. Both
decoder traces first differ after comb filtering. The merged SILK optimization
tests also expose a scalar v3 scaled-float-to-int16 rounding mismatch.
Full byte parity across compiler targets is not established.

The [focused native audit at `743867aa`](https://github.com/thesyncim/gopus/actions/runs/36563006244)
passes both the scaled-float conversion regression and independent C conversion
oracle in v3 scalar/SIMD builds. Its SILK control traces verify instrumented C
against ordinary C before reporting packet differences at frame 6 (MB mono)
and frame 13 (WB stereo). Their first reported control differences occur later,
so those controls do not yet identify the root cause. These cases remain
unresolved under the parity target; they have no numerical allowance.

The [native v3 audit at `29211b7c`](https://github.com/thesyncim/gopus/actions/runs/36610118795)
uses Go 1.27.1 and GCC 13.3 on AMD EPYC 7763 with matching scalar/SIMD C
references. CBR exact cases are 14/19 scalar (68 packet/61 range differences)
and 6/19 SIMD (321 packet/196 range differences), out of 2,175 packets per
lane. The two SILK encoder cases also fail the unchanged quality gate;
CELT/Hybrid have unresolved same-packet PCM differences. FFT/MDCT and SILK
primitive suites pass. The CELT encoder trace rejects inconsistent quantization
dimensions, so it does not yet establish a runtime divergence location.

The [native audit at `6fb767cf`](https://github.com/thesyncim/gopus/actions/runs/36618419822)
on Intel Xeon Platinum 8573C with Go 1.27.1 and GCC 13.3 passes 58/60 scalar
and 60/60 SIMD encoder checks, 15/24 scalar and 24/24 SIMD decoder checks, and
all 15 warm-allocation checks in each lane, with no skipped cases. CBR exact
cases are 14/19 scalar (70 packet/61 range differences) and 15/19 SIMD (75
packet/72 range differences) out of 2,175 packets per lane. These counts are
separate gates, not an overall byte-parity percentage. Haar and the
constant/ramped comb history seams, scalar stereo tails and SIMD exp2
approximation match the paired C references. The comb fallback, DC rejection
and high-pass filter match C; stereo fade and scalar decoder PCM still expose
differences. The long CBR streams also expose same-packet PCM differences in
both lanes, including short CELT and 10 ms Hybrid frames; the 24-case SIMD
decoder pass does not establish long-stream decoder parity. The corrected DC
oracle verifies all ten sample-rate/channel cases in each lane.

The SILK replay sends the actual Go LPC input/state to the linked C FindLPC
implementation. Both select the same interpolation factors at the first
failing frames (3 for MB mono frame 6; 0 for WB stereo frame 13), while the
ordinary C encoder selects 2 and 1. The original C call snapshots match the Go
decision state but differ in the LPC residual input: MB mono frame 6 first
differs at sample 140, WB stereo frame 13 at sample 192. Work therefore
follows the residual producer rather than altering FindLPC decisions. The
scalar decoder witness first differs in normalized coefficients before
synthesis. The transparent CELT frame-95 trace first differs at the SIMD MDCT
spectrum and scalar coarse-energy decisions. No numerical allowance is
accepted for these unresolved differences.

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
| P2 | LPCNet/DRED evidence | Predictor/state tests use numerical budgets, and some waveform helpers compare only a common prefix. OSCE-only probes request an extra C DRED feature. | LPCNet helpers use the public feature selector; complete package output/state bit checks pass OSCE, DRED and DRED+OSCE+QEXT scalar/SIMD at `f03fad55`. The 132 edited public DRED gates pass all eight local DRED feature/ISA lanes with matching float priming and actual int24 output. Independent nil-FEC, tiny-FEC and complexity-change history witnesses also pass exactly; durable FEC integration passes at `b29fcff7`. All 96 c6 early phase exit records at `11009635111`/`c6dfb561` have status 0, with no JSON test failures. No-sidecar warm zero-allocation passes all 12 feature/ISA phases; FARGAN/PLC-feature checks pass both instruction lanes, `SinF32` passes standard/fixed/fixed+QEXT in both lanes, and all eight LACE/NoLACE feature/ISA combinations pass. Artifact `11006418166` at `55b13f5f` preserves the one-allocation failure in two 16 kHz/complexity-5 phases; profiling attributes it to a 48-byte Go interface-assertion cache allocation, addressed by PLC capability changes at `a6081168`. Full A/B run `36506668630` passes at `c6dfb561`. |
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
| P2 | Legacy ARM CELT gates | A shared architecture skip omits exact kernel and self-equivalence checks; two private C helpers omit QEXT feature selection. | Sixteen focused checks pass all eight local lanes without skips at `ba23ba99`. VQ/partition helpers use the selected float feature archive and `opus_select_arch()` for RTCD calls. All sixteen checks also pass in the native AMD64 SIMD full suite at `c6dfb561`. |
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

### Feature integration checks

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
scalar DRED and scalar/SIMD DRED+OSCE+QEXT. Artifact `11006418166` at
`55b13f5f` preserves the native allocation failure; the
c6 early matrix below validates the capability change on AMD64.

### Runtime parity evidence

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

The [kernel and end-to-end evidence report](#performance) records
measured coverage, revisions, and all 53 replacement routines. All recorded
exactness gates pass at `c6dfb561`; claims remain scoped to the listed
configurations and cases.

Multistream long-burst PLC state, native 96 kHz crossfade stride, and Hybrid
QEXT payload routing pass all eight applicable local lanes at `3885a34d`.
The wide-band QEXT decoder consumes all signaled bands while rendering only
the physical spectrum; its exact PCM/range, stereo synthesis-stage and zero
warm allocation gates pass at `28cb897e`, together with integer-format clipping
lifecycle checks.

Artifact `11006418166` at `55b13f5f` records 93 zero phase exits, two
nonzero warm-allocation phase exits, and aggregate exit 1. The two failures
are the one-allocation no-sidecar PLC guard at 16 kHz/complexity 5. Profiling
attributes 48 bytes to Go 1.27.1's lazy interface-assertion cache inside SILK
concealment; capability changes at `a6081168` remove the five optional PLC
assertions from hot paths. Native early artifact `11009635111` at `c6dfb561`
records 96 zero phase exits and no JSON test failures. It passes all 12
no-sidecar warm zero-allocation feature/ISA phases, both-instruction-lane
FARGAN/PLC-feature checks, `SinF32` standard/fixed/fixed+QEXT checks in both
lanes, and all eight LACE/NoLACE feature/ISA combinations. Full A/B run
[36506668630](https://github.com/thesyncim/gopus/actions/runs/36506668630)
passes, including the full native SIMD package sweep in artifact `11009623177`.
Independent nil-FEC, tiny-FEC and complexity 0→5 history witnesses pass exact
counts, PCM bits and ranges through recovery. Non-fullband
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

### Deliberate boundaries

Real-audio quality and perceptual loss tests remain useful independent evidence;
they do not count as exactness proof. Synthetic decay checks also have a distinct
purpose from a live C output comparison. The only documented unsafe C-reference
boundary is the [custom-QEXT history access](#reference-boundary);
malformed packets alone do not justify excluding supported, defined C output.

### Verified local coverage

The following results use live, matching references on ARM64 with Go 1.27.1.
They describe their explicit cases and revisions, not all possible inputs.

| Surface | Coverage | Result |
|---|---|---|
| Fixed multistream decode | 3,024 surround, 400 discrete, 144 projection and 72 Go-encoded cases; integer Hybrid PLC, redundancy, multi-frame and degenerate packets | All 3,640 cases pass at `1e3edd99` in fixed scalar and fixed+QEXT scalar/SIMD |
| Fixed projection encode | Q15 stereo width, integer mode thresholds, five rates, full projection packet sweep | Exact state, packets and ranges in fixed/fixed+QEXT scalar/SIMD; zero warm allocations at `1e862928` |
| Hybrid-to-CELT transitions | Five API rates, mono/stereo, 0/±3 dB | All 30 cases exact in four fixed lanes; zero allocations at `6aefe867` |
| Multistream mode transitions and recovery | Every sample including 5 ms crossfades; 17 PLC/FEC/handover cases | Exact in all eight default/fixed/QEXT scalar/SIMD lanes at `eee85f70` |
| Hybrid public float decode | Mono/stereo, FB/SWB, six bitrates; at least one Hybrid packet required per cell | Exact in all eight default/fixed/QEXT scalar/SIMD lanes at `eee85f70`; warm caller-buffer allocation check passes |
| SILK/Hybrid loss channel routing | 128 coded/API channel, duration and loss-entry cases | Exact PCM, final range, recovery and zero warm allocations in all eight local lanes at `ae6d505a` |
| Malformed fixed multistream | 9,000 mutations per fixed/fixed+QEXT scalar/SIMD lane | Exact original-format PCM; compact redundancy PCM/range/PLC/reset regressions and warm allocation guard at `70be920b` |
| Malformed single-stream decode | All accepted float32/int16 samples and raw int24 outputs; no magnitude carve-out in exact comparison | Six focused feature/ISA lanes and the full default scalar public suite pass at `c0d68c52` |
| FEC packet oracle identity | Public feature archive and matching private-header configuration | Four exact packet selectors pass all eight default/fixed/QEXT scalar/SIMD lanes at `79685ce8` |
| Loss after CELT redundancy | 24/48 kHz, 10/20 ms, mono/stereo, three output formats, gains, consecutive loss and recovery | Exact in all eight float/fixed/QEXT scalar/SIMD lanes at `f7453894` |
| Low-rate fixed Hybrid | 8/12 kHz received, loss and recovery | Exact fixed/fixed+QEXT scalar/SIMD output with zero warm allocations |
| Strict encoder packets | CELT 19 case/signal pairs; CBR 19 cases / 2,175 packets; FEC 24 configurations × 3 signals | Scalar/SIMD pass without residual waivers at `4cb8015c` |
| CVBR public/CLI encode | Matching float API and CLI int24 input, including zero-padded EOF frame | Packet bytes, sizes and ranges pass scalar/SIMD; fixed+QEXT SIMD also passes at `84d1aa38` |
| CLI decode conformance | Matching opus_decode_float and opus_decode24 APIs across the CLI configuration matrix | Every decoded float bit matches in scalar, SIMD and fixed+QEXT SIMD at `af0c9198` |
| Multi-frame DTX encode | 945 speech/fade/duration configurations, silence and recovery | All packet bytes match selected C in scalar/SIMD at `af0c9198` |
| DRED latent traces | Initial mono/stereo 20/40/60 ms, long-frame cadence and model reload | Exact state and latent bits pass DRED and combined DRED+OSCE+QEXT scalar/SIMD at `036cc257` |
| Fixed-QEXT encoder transitions | 2,988 configurations × 40 frames | Scalar/SIMD packet/range gates pass |
| CELT/QEXT primitives | PVQ grid, QEXT extension-band content, native 96 kHz MDCT | Exact packet/float-bit gates pass scalar/SIMD at `e250d4af` |
| Native 96 kHz QEXT | CELT 2.5/5/10/20 ms; mono/stereo; float32/int16/int24; reset, PLC and Hybrid transitions | Focused paired float/fixed-QEXT gates pass scalar/SIMD |
| DRED+QEXT multistream | 96-frame 5.1, 7.1 and first-order ambisonic sequences; reset and simultaneous extensions | Exact packets/ranges and zero warm allocations in scalar/SIMD |
| DRED+OSCE+QEXT reference | Feature/ISA identity and decoder state layout including QEXT history | Warm-up gates pass both lanes; full public SIMD suite passes at `69a94bce` |
| Malformed multi-frame decode | Valid → malformed → valid → PLC → valid; mono/stereo, three formats, padding and frame overruns | All 24 combinations preserve C status, PCM, ranges and recovery; zero allocations |
| Custom modes | Float/fixed, generated geometries, controls, PLC, recovery and reset; all five scaled-band modes mono/stereo | Signalling, finite budgets, defaults, error ordering and reset lifetime pass the full package in all eight local lanes at `17e27246`, subject to the explicit C undefined-behavior boundary below |
| Automatic encoder modes | 432 configurations × 10 frames, identical input/controls/budget and public float API | All packet bytes and final ranges pass all eight local lanes at `a0a9c877` |
| Multistream int16 encode | Coupled/discrete stereo, quad, 5.1 and 7.1 layouts; six persistent frames each | Actual C short-input API packets and ranges pass all eight local lanes at `73347769` |
| QEXT cubic reconstruction | Captured mono leaf and 11 vector boundary sizes; mono/stereo combined and separate packet sequences | Exact primitive/state/public PCM/ranges and zero warm allocations at `77cb9a9d` |
| Allocation and type discipline | Caller-owned encode/decode buffers, extension scratch, fixed integer composition | Warm allocation gates pass; type guard retains 27 recorded findings with no added debt |

At the `50f7cda0` integration checkpoint, the full
13 × 2,500-frame soak, 12,960 decoder format cases, CELT encoder gate and
4,320-frame automatic-mode encoder matrix pass default/fixed+QEXT scalar/SIMD.
The new standard-band and deferred SILK history equivalence/allocation tests
also pass those four lanes.

Full package results provide broader regression coverage alongside these exact
gates. The root public suite passes default scalar/SIMD, fixed SIMD, fixed+QEXT
SIMD (388.052 s), custom+QEXT SIMD (260.261 s), and DRED+OSCE+QEXT SIMD
(315.382 s) at their recorded checkpoints. The full fixed+QEXT multistream
package passes scalar (90.343 s) and SIMD (86.055 s) at the `6aefe867`
transition checkpoint. The complete native AMD64 SIMD package sweep at
`c6dfb561` passes alongside the targeted scalar/SIMD feature matrix. Quality-only
assertions remain separate from exactness proof.

### Correctness cost outside the assembly inventory

The ARM64 transient-analysis recurrence uses the selected C two-state update.
An algebraically reduced recurrence changes float32 rounding. On M4 Max,
Go 1.27.1 SIMD, five 750 ms samples compare the two forms: median 5 ms-frame
analysis is 2,046 → 2,327 ns, and 20 ms analysis is 6,192 → 7,013 ns
(13.7% and 13.3% more time). Both allocate zero bytes. This is a focused
primitive comparison; it does not establish an end-to-end regression. Full CELT,
selected-C CBR and short-frame packet gates pass both scalar/SIMD builds.


## Reference boundary

### Reproduced invalid read

Pinned libopus 1.6.1, float scalar, `CUSTOM_MODES` and `ENABLE_QEXT`,
accepts a 96 kHz, 2048-sample custom mode. Its stateful loss path reads before
the synthesis buffer. AddressSanitizer reports a read fault in
`celt_decode_lost`, `celt/celt_decoder.c:957`.

The mode has a 256-sample short transform, so `qext_scale` is 1 and
`decode_buffer_size` is 2048. With a 2048-sample frame, the source expression
`decode_buffer_size - max_period - N + extrapolation_offset + j` can be
negative. This is an invalid C reference result, not a portable PCM target.
The Go PLC path checks its history bounds and uses noise concealment when
periodic concealment cannot access the required history.

The received-frame comb filter also has no preceding sample history at
`out_syn = decode_mem + decode_buffer_size - N` for this geometry. The observed
2048-sample mono sequence differs on received frame 1 before its first loss;
the loss case has the independently confirmed sanitizer failure. No universal
byte-parity claim covers these results.

### Reproduction

The unmodified source is extracted from `tmp_check/opus-1.6.1.tar.gz` into
`/private/tmp/gopus-custom-qext-asan/opus-1.6.1`. The extracted and repository
`celt_decoder.c` both have SHA-256
`a53393c70ae917d39229f34bce2c16ada535807e095cad00a4580aee48c9bfdb`.

Configure with Clang and:

```sh
CFLAGS='-O1 -g -fsanitize=address -fno-omit-frame-pointer -ffp-contract=off -fno-vectorize -fno-slp-vectorize'
LDFLAGS='-fsanitize=address'
./configure --enable-custom-modes --enable-qext --disable-asm \
  --disable-rtcd --disable-intrinsics --enable-static --disable-shared
make -j4
```

Compile `tools/csrc/libopus_custom_stateful.c` against that instrumented
archive with `-DHAVE_CONFIG_H -fsanitize=address` and its matching headers.
Input uses `customSequenceInput` from the stateful Go oracle, mono, 200-byte
CBR frames, and loss at frames 3 and 4. The Go safety regression is
`TestQEXTFloatCustom2048SafeSequence`; the exact C sequence oracle covers the
other frame geometries.

Local evidence:

- Request: `/private/tmp/gopus-custom-qext-asan/request.bin`.
- Sanitizer output: `/private/tmp/gopus-custom-qext-asan/runtime.log`.
- Symbolized location: `celt_decode_lost (celt_decoder.c:957)`.

### Upstream status and parity scope

Checked on 2026-09-28 against upstream `main`, commit
[`503d81b138d76621aae4b12786e90de48aa8db3a`](https://github.com/xiph/opus/commit/503d81b138d76621aae4b12786e90de48aa8db3a),
dated 2026-09-11. The latest source retains the same
[history read](https://github.com/xiph/opus/blob/503d81b138d76621aae4b12786e90de48aa8db3a/celt/celt_decoder.c#L983),
2048-sample buffer, frame-size acceptance and scale selection. No fix is present
in these paths. The sanitizer reproduction above uses pinned 1.6.1, not a build
of current upstream.

Undefined C reads in this stateful 2048-sample geometry are an explicit
exception to byte/sample parity. Go preserves bounded history access and safe
concealment. First-frame encode/decode and steady-state allocation checks remain
covered; `TestQEXTFloatCustom2048SafeSequence` checks received frames, a loss
burst and recovery for both channel counts. The other custom-mode comparisons
retain exact packet, range, float PCM and int16 PCM checks.

The upstream build rejects fixed-point combined with DRED/OSCE; those neural
feature combinations do not have a matching supported C reference lane.

## Performance

All 53 symbols from the 41 assembly files at baseline `8ac93c85` have Go
replacements. No tracked `.s` or `.S` files remain. Go 1.27 is the minimum.
Ordinary builds and `-tags nosimd` use scalar Go. `GOEXPERIMENT=simd` enables
`simd/archsimd` kernels with CPU checks and scalar fallbacks.
AMD64 references select SSE/AVX2 RTCD (`opus_select_arch=4` for the measured AVX2
lane); ARM64 references bind NEON at compile time. Scalar C disables assembly,
intrinsics and compiler vectorization. Archive, header, compiler and dispatch
stamps are checked before comparison.

### End-to-end codec throughput

#### Native AMD64 end-to-end measurements

Native early artifact `11009635111` compares assembly `8ac93c85` with
SIMD/`nosimd` `c6dfb561` on AMD EPYC 9V74, Go 1.27.1, GCC 13.3.0,
GOAMD64=v1, and PGO enabled. Four interleaved 500 ms samples use `-cpu=1`;
all 72 benchmark samples report 0 B/op and 0 allocs/op. Values are median ns/op.
The [native run](https://github.com/thesyncim/gopus/actions/runs/36506668630)
provides the underlying benchmark logs in artifact `11009635111`.

| Workload | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,222.5 | 13,446 | 17,080 |
| Hybrid decode | 29,924 | 26,080 | 32,961 |
| SILK decode | 23,579.5 | 16,890 | 21,827.5 |
| Caller-buffer encode | 93,138 | 61,203 | 104,182.5 |
| VoIP encode | 99,290.5 | 66,263.5 | 109,582 |
| Low-delay encode | 92,213 | 61,247.5 | 103,673.5 |

Go SIMD takes less time than assembly in all six within-artifact workloads.
`nosimd` is faster than assembly for CELT and SILK decode and slower for the
other four workloads. Do not compare absolute timings across early artifacts
as source changes; use only the within-run variants shown here. The 11 AMD64
kernel rows use complete artifact `11009623177` at `c6dfb561` on EPYC 9V74;
ARM64 rows retain their own measured revisions.

#### Matched libopus 1.6.1 comparison

Same candidate revision and runner; C scalar vs Go scalar and C SIMD vs Go
SIMD. Early artifact `11009635111` uses candidate `c6dfb561` on AMD EPYC 9V74.
Each case has three 250 ms minimum runs. Values are µs/packet (lower is faster).
Every paired Go benchmark row allocates zero; C allocations are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| CELT-FB-20ms-stereo-128k | 197.34 | 191.63 | 145.71 | 123.58 |
| CELT-FB-5ms-mono-64k | 21.59 | 23.66 | 20.36 | 19.71 |
| Hybrid-FB-20ms-mono-64k | 395.42 | 376.56 | 267.38 | 221.67 |
| Hybrid-FB-20ms-stereo-96k | 226.37 | 223.36 | 170.36 | 139.82 |
| SILK-WB-20ms-mono-32k | 762.47 | 669.81 | 421.35 | 321.99 |
| RFC vectors Float32 | 32.85 | 34.01 | 31.01 | 28.27 |
| RFC vectors Int16 | 36.82 | 37.34 | 33.82 | 32.38 |

Go SIMD takes less time than matched C in all seven within-artifact workloads.
Scalar Go takes more time for 5 ms CELT and the two vector-decode cases, and
less time for the other four encode cases. These are workload-specific results
from this runner.

Decoder rows aggregate 20,075 identical packets; encoder rows use identical PCM
and controls. Encoder timings do not establish long-stream packet parity.
The throughput table describes the early artifact; the full A/B gate also
passes at the same revision.

### AMD64 compiler targets

The compiler-target benchmark is an opt-in script for performance-table refreshes;
routine PR CI does not run this matrix or add jobs for it. Go binaries and C
archives are built before the timed rounds. C benchmark helpers compile before
their measurements; builds do not run concurrently with timings. Four rounds
rotate nine Go binaries: old assembly,
scalar Go, and SIMD Go, each compiled at v1, v2, and v3. Every row uses the same
host, controls, Go version, and PGO policy. Within each target, the three Go
implementations use identical input generators. Comparisons between compiler
targets can also reflect compiler effects on those generators; they do not
isolate kernel speed alone. The paired C encoder table verifies identical PCM
hashes across targets and modes. Binary build information,
source revisions, profile hashes, compiler flags and C archive hashes accompany
the results.

| Go compiler target | C compiler baseline |
|---|---|
| `GOAMD64=v1` | `-march=x86-64 -mtune=generic` |
| `GOAMD64=v2` | `-march=x86-64-v2 -mtune=generic` |
| `GOAMD64=v3` | `-march=x86-64-v3 -mtune=generic` |

Scalar C disables intrinsics, assembly and automatic vectorization while keeping
normal scalar floating-point contraction. SIMD C and Go select the supported
native SIMD kernels; a v1 compiler baseline does not restrict runtime dispatch
to SSE2. The target-specific references have separate directories and validated
stamps. Direct C oracle helpers receive the matching target flags too. No
`-march=native` or timing from CPU emulation enters this matrix.

Directly compiled FFT/MDCT oracle sources use the compiler's default C dialect, matching
the archive build. GCC 13.3 disables default contraction under strict C99, so a
strict-C99 helper cannot establish parity with the default-dialect v3 archive.
The focused native kernel audit captures the executed helper binaries and their
disassembly as well as the reference archive's code generation.

The [native target audit at `bc5ddaeb`](https://github.com/thesyncim/gopus/actions/runs/36555425559)
uses Go 1.27.1 and GCC 13.3. Its bounded default-float selection records:

| Target / Go lane | Encode cases exact | Decode cases exact | CBR cases exact | CBR packet differences / 2,175 | CBR range differences / 2,175 | Warm allocation checks |
|---|---:|---:|---:|---:|---:|---|
| v1 / scalar | 60/60 | 24/24 | 19/19 | 0 | 0 | Pass |
| v1 / SIMD | 60/60 | 24/24 | 19/19 | 0 | 0 | Pass |
| v2 / scalar | 60/60 | 24/24 | 19/19 | 0 | 0 | Pass |
| v2 / SIMD | 60/60 | 24/24 | 19/19 | 0 | 0 | Pass |
| v3 / scalar | 40/60 | 8/24 | 8/19 | 432 | 382 | Pass |
| v3 / SIMD | 24/60 | 6/24 | 1/19 | 681 | 517 | Pass |

The [native follow-up at `ede43639`](https://github.com/thesyncim/gopus/actions/runs/36553888841)
passes all six FFT/MDCT live-C suites in each v3 lane (64 leaf cases per lane,
no failed or skipped oracle cases) and the selected FFT/MDCT unit checks.
The dispatch proof accepts presumed AVX2 only with matching compiler metadata
and the required CPU features. Exact pitch comparisons still expose scalar
contraction and SIMD underflow, signed-zero and NaN-tail differences. The SILK
LPC analysis filter, sine window and gain-processing oracles also fail.

The full target matrix at `bc5ddaeb` confirms the packet, PCM and CBR results
listed above, with all 15 warm allocation checks passing per lane and no
skipped exactness cases. It emits no timing table because exactness checks
fail. The FFT/MDCT proof does not establish complete packet or PCM parity.
Open exact failures block v3 exactness and timing claims. Passing v1/v2
selections establish only the listed coverage, not universal byte parity.

The [focused audit at `834221f9`](https://github.com/thesyncim/gopus/actions/runs/36560962348)
passes the strengthened SILK LPC, sine-window and gain-processing checks in
both modes, including the 2 dB sigmoid witness and scalar LPC allocation guard.
Its CBR results are 14/19 exact scalar cases (68 packet and 61 range differences
out of 2,175) and 6/19 exact SIMD cases (321 packet and 196 range differences).
FFT/MDCT, CELT log2/angle math, scalar and SIMD pitch, band-energy, rotation
and unquantization oracles pass. The decoder trace finds its first difference
after comb filtering in both modes. The merged SILK optimization checks expose
a scalar v3 scaled-float-to-int16 rounding mismatch; their SIMD counterparts
pass. Normalized scratch comparisons cover the source-defined active bands;
spectrum and PCM comparisons cover the full output. This focused audit does
not repeat the full public matrix or provide performance measurements.

Each candidate target/mode must pass exact CBR packets/ranges, selected stateful
encode and fresh-state decode cases, dispatch and warm allocation checks.
Compiled binaries verify CPU and OS support at startup; an
unsupported target fails the prerequisite. Missing, skipped or failed required
cases invalidate the corresponding comparison. Six E2E workloads and seven
paired C workloads require complete rows and zero Go allocations.

This matrix covers the default float core. Optional features and the complete
53-symbol inventory keep their separately recorded v1 coverage. The tables above
retain their measured revision and target until a complete compiler-target
artifact supplies replacement data.

On a native Linux amd64 host with AVX2/FMA, run:

```sh
bash scripts/benchmark_goamd64.sh /path/to/assembly-baseline /path/to/candidate /tmp/gopus-amd64-evidence
```

The separate checkouts identify the revisions under comparison. The output
directory contains validation logs, build provenance, JSON and Markdown tables.
The existing **Verify Production Exhaustive** manual workflow accepts
`task=goamd64-benchmark` to run this command instead of release evidence, using
`benchmark_baseline` as the assembly revision. Scheduled runs retain the release
evidence task; routine PR CI has no compiler-target benchmark step.
`task=goamd64-kernel-audit` selects focused v3 kernel, dispatch and CBR checks.

### Per-symbol inventory

Former symbols identify the pre-port assembly entry points. `0` in the
allocation column is limited to directly measured kernels.

Direct timing covers all 51 comparable routines. The two startup
feature-discovery helpers have no
comparable per-call Go operation and are marked n/a with the reason.

| # | Former assembly symbol | Old arch | Go replacement source | Replacement path | Measured timing (ns/op) | Allocs/op | Status |
|---:|---|---|---|---|---|---|---|
| 1 | `combFilterConstNeon` | arm64 | `internal/celt/comb_const_simd_arm64.go`; `internal/celt/comb_const_default.go` | archsimd / scalar | N=480: old asm 115.4 (114.7–116.9) → Go SIMD 109.0 (107.7–113.0); scalar Go 617.7 (592.7–623.2; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.5% faster than asm; exact and zero-alloc checks pass |
| 2 | `cwrsiFastCore` | arm64 | `internal/celt/cwrs_fast_default.go` | scalar Go | live table-covered N=48, K=5: old asm 28.94 (28.85–29.34) → scalar Go 44.02 (43.81–44.12) ns/op | 0 | paired M4 Go 1.27.0; scalar Go is 52% slower than asm; exact output and zero-allocation checks pass |
| 3 | `deemphasisStereoPlanarF32Core` | arm64 | `internal/celt/output_helpers.go` (`deemphasisChannel`) | exact scalar recurrence in every build | b36c1c20 live N=480 diagnostic, scalar / SIMD build: mono 714.4 (657.5–976.2) / 1,552 (1,278–2,100); stereo 1,353 (1,350–1,355) / 1,599 (1,431–2,033). Paired contiguous accumulation overlay on 5e14 source: mono N=480 baseline 1,150 (1,150–1,158) → candidate 882.4 (877.5–891.6) ordinary; baseline 1,150 (1,150–1,165) → candidate 884.9 (879.8–887.0) SIMD build | 0 | M4 / Go 1.27.0, five 300 ms paired samples; unchanged recurrence matches C and zero-allocation gates pass; direct improvement is 23.3%/23.1%; public Hybrid results are in the throughput section; no AMD64 gain is inferred |
| 4 | `expRotation1PassNeon` | arm64 | `internal/celt/exp_rotation_simd_arm64.go`; `internal/celt/exp_rotation_default.go` | archsimd / scalar | old asm → Go → SIMD: len32/stride1 284.3 (283.6–289.3) → 286.9 (285.8–288.9) → 282.6 (280.6–284.3); len64/stride1 583.2 (581.0–584.1) → 579.7 (574.3–584.2) → 578.2 (574.5–592.8); len32/stride2 138.1 (136.6–145.2) → 148.8 (142.6–152.1) → 159.5 (155.8–167.2); len32/stride4 85.03 (84.45–85.53) → 80.54 (80.13–83.10) → 84.20 (83.98–84.38) | 0 | measured; SIMD near assembly except stride2 slower |
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_scalar.go` | archsimd / scalar | original direct N=32 old asm → scalar Go → Go SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72); refined Go-only paired direct N=32: 11.69 → 11.14 median; live wrapper n0=32 (helper groups=16): 11.25 → 8.00 median | 0 | refined SIMD improves 4.7% on the direct N=32 fixture and 29% on the live wrapper fixture; exact parity, full CELT modes, and focused checkptr pass; asm comparison is from an earlier run |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_arm64_nosimd.go` | archsimd / scalar | N=120: old asm 19.28 (19.19–19.31) → scalar Go 74.08 (73.89–74.25; earlier Go 1.27.1 run) → Go SIMD 20.41 (20.40–20.49) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.9% slower than asm and 23% faster than the first SIMD port |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_nosimd.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): N=480: 114.6 (114.5–114.7) → 509.5 (508.9–510.4) → 105.3 (105.1–105.4) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 8.1% less time than asm in this fixture |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly4m1_default.go` | archsimd / scalar | N=128: old asm 103–105, scalar Go 161–166, prior SIMD 157–162 in original paired run; refined SIMD 120.9 median versus prior SIMD 178.0 median in seven paired 500 ms samples | 0 | refined SIMD is 32% faster than prior SIMD in its paired run and about 16% slower than the recorded asm baseline; exact bits, zero alloc, full CELT modes, and focused checkptr pass |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): m=8, N=4: 485.3 (484.3–485.7) → 695.1 (693.6–738) → 159.4 (158.4–159.7) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 67.2% less time than asm in this fixture |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): m=8, N=4: 286.8 (286.6–287.7) → 268.9 (268.1–270.3) → 61.8 (61.7–62.06) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 78.5% less time than asm in this fixture |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): m=8, N=4: 279.4 (279.3–279.6) → 350.4 (348.4–355.6) → 84.43 (83.35–84.75) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 69.8% less time than asm in this fixture |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 103.9–105.9 → scalar Go 183.0–188.2 → Go SIMD 71.3–73.4 ns/op | 0 | SIMD is about 31% faster than asm and 2.6× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.4–72.1 → scalar Go 75.3–75.6 → Go SIMD 34.1–36.0 ns/op | 0 | SIMD is about 50% faster than asm and 2.1× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.5–71.1 → scalar Go 89.2–90.7 → Go SIMD 43.9–45.0 ns/op | 0 | SIMD is about 37% faster than asm and 2.0× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.69 (36.57–36.81) → scalar Go 153.6 (153.4–153.7; earlier Go 1.27.1 run) → Go SIMD 37.18 (37.02–37.28) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.3% slower than asm and about 30% faster than the first SIMD port |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.67 (36.65–38.30) → scalar Go 154.9 (154.8–155.5; earlier Go 1.27.1 run) → Go SIMD 37.35 (37.15–38.31) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.9% slower than asm and about 30% faster than the first SIMD port; samples include one outlier per mode |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8 paired M4 Go 1.27.0: old asm 14.67 (14.58–15.38) → prior SIMD 15.56 (15.54–15.75) → packed SIMD 14.57 (14.50–14.69); scalar Go 109.4 (109.3–109.6) in an earlier fixture | 0 | packed SIMD is 6.4% faster than prior SIMD and at assembly speed; exact old-asm comparison, zero-alloc, and checkptr level 2 pass |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm median 12.71 (run medians 12.69–12.84) → Go SIMD 14.65 (14.64–14.72); prior Go SIMD 15.48 (15.39–15.79); scalar Go 103.4 (103.3–103.9; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 15% slower than asm and about 5% faster than the prior SIMD loop; exact and zero-alloc checks pass |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/celt/pitch_xcorr_tiny_simd_amd64.go`; scalar defaults | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): coarse 240×360: 3470 (3444–3488) → 45402 (45360–45409) → 3318 (3311–3338); half 480×64: 1178 (1171–1195) → 16227 (16207–16330) → 1171 (1164–1183); tiny coarse 5×244: 315.4 (315.1–323.7) → 605 (602.5–606.4) → 130.5 (130.4–130.6); tiny coarse PLC order 5×244: 315.7 (315–317) → 627.5 (627.1–628.1) → 130.4 (130.4–130.6); tiny fine 10×10: 33.11 (33.08–33.51) → 49.54 (48.88–51.06) → 25.38 (25.37–25.45); tiny fine PLC order 10×10: 33.11 (33.08–33.19) → 44.72 (44.7–44.84) → 25.39 (25.36–25.41) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; workload-specific results; each wrapper shape is separate |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | old asm → scalar Go → Go SIMD (ns/op): direct pulse loop 48 dims / 16 pulses: 693.4 (692.4–696.4) → 986.1 (985.7–986.7) → 2307 (2193–2388); production full search 48 dims / 16 pulses: 940.5 (933.5–944.1) → 1088 (1083–1103) → 698.5 (694.4–701.1) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; direct pulse helper is not the finite-input production route; full search is the live comparator |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | original N=48, pulses=16 old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024); live-shaped Go-only paired scalar loop 705.0 (702.5–728.0) → two-position unroll 522.9 (518.5–527.3) | 0 | unroll is 25.8% faster than scalar on the production-shaped fixture; exact scan order, libopus parity, zero alloc, full CELT modes, and checkptr pass; asm comparison uses a different fixture |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | old asm → scalar Go → Go SIMD (ns/op): four varying lanes: 2.469 (2.464–2.473) → n/a → 1.578 (1.575–1.605) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 36.1% less time than asm in this fixture |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | old asm → scalar Go → Go SIMD (ns/op): N=48 varying data: 29.33 (29.28–29.39) → n/a → 28.23 (28.12–28.26) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; exact helper is not the finite-input production route; row 27 is the live comparator; SIMD build takes 3.8% less time than asm in this fixture |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go` | scalar Go in both candidate builds | old asm → scalar Go → Go SIMD (ns/op): N=480: 653.4 (653.3–654.1) → 502.8 (501.7–504.2) → 817.8 (807.5–829.1) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; both Go build modes call the same scalar helper; SIMD build takes 25.2% more time than asm in this fixture |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_scalar_arm64.go`; `internal/celt/tone_lpc_corr_simd_arm64.go` | archsimd / scalar | cnt=480, delays=1/2: recorded old asm 163.4 (163.0–163.5); earlier scalar Go 559.0 (558.3–559.5), earlier SIMD 117.7 (117.5–118.0); current exact-order SIMD 1,109 median (803.8–1,287), five 300 ms samples | 0 | M4/Go 1.27.0 current diagnostic is noisy and slower; earlier SIMD timing uses a reduction that fails selected-C parity, so its speedup is not a current claim; all 26 LPC / 12 tone-detection checks pass in three modes, SIMD warm allocations are zero; controlled performance tuning remains |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_neon_ordered_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480 recorded old asm 189.2 (186.9–190.2), scalar Go 699.3 (697.0–699.9), four-phase SIMD 167.8 (167.5–167.9); current ordered SIMD 402.2 (399.1–419.1), five 300 ms samples | 0 | replacement xcorrKernel4Float32NeonOrdered matches all 21 selected-C raw-bit cases with zero warm allocations; earlier four-phase speedup does not apply to exact arithmetic; current M4/Go 1.27.0 diagnostic is separate from the assembly run, and production batching remains optimization work |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480, scale 1: old asm 23.33 (22.97–23.59) → prior Go SIMD 34.65 (34.32–35.32) → tuned SIMD 27.16 (26.55–27.47). Scale 32768: old asm 23.36 (22.89–23.72) → prior Go SIMD 34.66 (34.24–35.26) → tuned SIMD 34.92 (34.26–35.65). Scalar Go 489.8 (484.9–513.3; earlier Go 1.27.1 fixture). | 0 | paired M4 Go 1.27.0; live pitch path at scale 1 is 22% faster than prior Go and 16% slower than asm; scale 32768 has no measured gain; exact and zero-alloc checks pass |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): N=480: 88.92 (88.53–90.46) → 263.6 (263.1–263.9) → 90.59 (90.49–90.9) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 1.9% more time than asm in this fixture |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | original N=480 old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3); refined Go-only paired prior loop 135.6 → bounds-hoisted loop 107.1 median | 0 | refined Go is 21% faster on the paired fixture; exact bits, zero alloc, full SILK modes, and checkptr pass; asm comparison uses a different fixture |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm 28.81 (28.55–29.86) → prior Go SIMD 33.68 (33.63–34.56) → tuned Go SIMD 24.00 (23.76–24.10); scalar Go 216.4 (216.1–217.3; earlier Go 1.27.1 fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 17% faster than asm and 29% faster than prior SIMD; exact float bits and zero allocations |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_simd_arm64.go`; `internal/silk/lpc_synth_default.go` | archsimd / scalar | subframe=80: original paired M4 old asm 250.0 (228.9–250.5) → Go SIMD 341.9 (340.8–343.5); refined Go-only paired current SIMD 290.4–292.1 → refined SIMD 253.4–255.1; scalar Go 391.9 (387.7–394.4) in a separate run | 0 | refined SIMD is about 13% faster than prior SIMD on the paired fixture and close to the recorded asm baseline; exact parity, zero alloc, full SILK modes, and checkptr level 2 pass |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_simd_arm64.go`; `internal/silk/pitch_xcorr_impl_default.go` | archsimd / scalar | length=240, maxPitch=120: old asm 3,690 (3,665–3,733) → tuned Go SIMD production 2,483 (2,461–2,496); direct SIMD 2,487 (2,473–2,492); prior SIMD 4,892 (4,868–4,910); scalar Go 13,188 (13,043–13,237; earlier fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 33% faster than asm and 49% faster than prior SIMD; exact per-lag bits and zero allocations |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_impl_simd_amd64.go` | archsimd / scalar | old asm → scalar Go → Go SIMD (ns/op): celtPitchXcorrFloat 120×300: 1823 (1818–1827) → 18744 (18723–19055) → 2400 (2398–2402) | 0 | run 36506668630 / artifact 11009623177; asm 8ac93c85 vs Go c6dfb561; five 300 ms samples, AMD EPYC 9V74, Go 1.27.1, GCC 13.3; zero allocations; SIMD build takes 31.7% more time than asm in this fixture |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 107.56 (105.68–148.66) → scalar Go 280.39 (275.17–287.17) → Go SIMD 115.73 (111.11–119.72) | 0 | paired M4 Go 1.27.0; SIMD is 2.4× faster than scalar Go and 7.6% slower than asm; exact and zero-alloc checks pass |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 122.9 (122.5–124.7) → scalar Go 293.8 (293.2–296.0) → Go SIMD production 114.2 (113.2–115.6); direct SIMD core 113.5 (111.5–114.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 7% faster than asm and 2.6× faster than scalar Go; exact and zero-alloc checks pass |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 113.5 (113.2–114.5) → scalar Go 307.0 (304.7–308.7) → Go SIMD production 105.9 (105.7–106.5); direct SIMD core 106.2 (105.1–106.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 6.7% faster than asm and 2.9× faster than scalar Go; exact and zero-alloc checks pass |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 424.7 / exact Go SIMD 131.5 (131.3–132.7); recorded old asm 63.13 (62.17–63.76) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 422.4 / exact Go SIMD 106.9 (106.7–107.0); recorded old asm 52.31 (52.15–52.60) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |

### Measurement method

M4 Max (`darwin/arm64`) A/B measurements use the same host and Go version for
each old-assembly/candidate pair. Initial rows use Go 1.27.1; ARM64 tone LPC
and tuned IMDCT pre-rotate, fold, middle-fold, post-twiddle, and comb filter use
Go 1.27.0. The direct benchmark set uses GOMAXPROCS=4 and five samples per
mode; the initial rows use 100 ms samples and the added inventory wrappers use
200 ms samples.
The tuned PCM and ARM64 SILK xcorr rows use five 250 ms samples; ARM64 tone
LPC uses five interleaved 300 ms samples. All use GOMAXPROCS=4 on the same M4.
The tuned SILK float conversion row uses 20 interleaved samples per mode with
one million calls per sample and the same N=480 input fixture. Pitch detection
uses scale 1; scale 32768 is measured separately.
The tuned SILK 21846 FIR row uses 20 interleaved samples per mode with 400,000
calls per sample and the live nOut=240, bufLen=87 fixture.
The tuned SILK 32768 and 43691 FIR rows use five 300 ms samples per mode on
the same M4 with nOut=240; production-dispatch timings include the wrapper.
The tuned SILK int16-to-float32 row uses five 300 ms samples per mode on the
same M4 with N=480 and compares the production-dispatch path.
The tuned ARM64 SILK pitch-xcorr row uses five paired 300 ms samples per mode
on the same M4 with length=240 and maxPitch=120.
The tuned ARM64 SILK LPC synthesis row uses five paired 300 ms samples per mode
on the same M4 with subframe length 80. One assembly sample is a low outlier;
the other four measure 249.2–250.5 ns/op.
The tuned ARM64 unit PCM conversion row uses five interleaved outer runs with
500,000 calls per mode and n=480; the table gives the mean and range of the
five run means on the same M4 with Go 1.27.0.
The tuned ARM64 MDCT middle-fold row uses five paired 200 ms samples per mode
on the same M4 with n4=64 and blocks=8; the old assembly and both Go SIMD
versions use the same direct fixture and Go 1.27.0.
The tuned ARM64 FFT butterfly rows use three paired 200 ms samples per mode
on the same M4 with Go 1.27.0 and `-cpu=1`. The M1 fixture uses N=128; inner
radix-3/4/5 fixtures use m=8, N=4, and fstride=8 with preallocated work copies.
Candidate timings call the production dispatch wrappers.
The refined ARM64 radix-4 M1 path uses seven paired 500 ms samples on the same
M4 and fixture. Its prior and refined Go SIMD medians are 178.0 and 120.9
ns/op; both report zero allocations. The old-assembly timing is from the
earlier paired comparison, so the 103–105 ns range is a reference, not a
same-run ratio for this refinement.
The ARM64 Haar two-group unroll uses five paired 500 ms samples on M4 with
Go 1.27.0 and zero allocations. The live `haar1` wrapper with n0=32 dispatches
to the stride-4 helper with 16 groups; its current and refined Go SIMD medians
are 11.25 and 8.00 ns/op. A direct helper fixture with 32 groups measures
11.69 and 11.14 ns/op medians. These paired Go-only measurements do not use
the earlier old-assembly fixture.
The ARM64 SILK LPC synthesis refinement uses five paired same-fixture runs
with Go 1.27.0, `GOEXPERIMENT=simd`, GOMAXPROCS=1, and subframe length 80.
The current Go SIMD path measures 290.4–292.1 ns/op and the refined path
253.4–255.1 ns/op, both allocation-free. The old-assembly ~250 ns timing
comes from the earlier paired comparison and is a reference here.
The CELT PVQ two-position unroll uses ten paired samples of a normalized
N=48, K=16 low-pulse input with zeroed work buffers, matching the production
path's input shape. The scalar loop measures 705.0 ns/op median
(702.5–728.0), and the unroll measures 522.9 (518.5–527.3), both zero alloc.
The original old-assembly comparison uses a different fixture; no ratio is
inferred across those runs.
The ARM64 SILK float inner-product bounds refinement uses the same M4 and
Go 1.27.0 fixture for five paired samples: 135.6 ns/op median for the prior
Go loop and 107.1 for the refined loop, both zero alloc. Its old-assembly
comparison uses a different fixture.
The ARM64 pointer-endpoint fixes preserve kernel arithmetic and pass the full
CELT SIMD suite with `-gcflags=all=-d=checkptr=2`. Post-fix spot benchmarks
use Go 1.27.0, `GOEXPERIMENT=simd`, `-cpu=1`, three 200 ms samples on M4.
These spot timings are separate from the paired assembly comparisons in the
matrix: L1 N=480 33.54–33.65; prefilter dual N=240 26.96–27.04; scale
N=480 17.30–17.51; stereo merge N=480 50.18–50.64; tone LPC N=480
76.46–76.62 (earlier four-partial reduction; not the exact tone implementation);
four-output xcorr N=480 133.5–133.7 ns/op.
The ARM64 CWRS row uses five paired 500 ms samples per mode on the same M4
with Go 1.27.0 and GOMAXPROCS=4. N=48, K=5 is table-covered by
`canUseCWRSFast` and reaches the fast decoder path; N=48, K=12 does not.
The ARM64 deemphasis comparison uses five interleaved 300 ms samples per
version on M4 with Go 1.27.0, `GOEXPERIMENT=simd`, and `-cpu=1`. Identical
benchmarks call the live mono and planar-stereo wrappers with N=480, varied
preallocated input, fixed initial memory, and observed output/state. The
reference is the `e6f2b332` implementation through a source overlay; the candidate
uses the exact sequential C recurrence. Public decode binaries use the same
settings and caller-owned output. All samples report zero allocations.
The tuned MDCT post-twiddle row uses three interleaved 15-sample runs; its
reported ranges are the three within-run medians because individual samples
include M4 scheduling outliers. Assembly and both SIMD versions use the same
fixture.
Native AMD64 measurements use real AVX2/FMA hardware. Rosetta is useful for
cross-compilation and scalar checks but does not establish native SIMD behavior.
Kernel benchmark names retain their fixture identities across the baseline and
candidate. The corrected `b.Loop` wrappers observe outputs and vary inputs where
required; zero-valued butterfly buffers keep repeated arithmetic finite.

The AMD64 symbol rows use the completed direct-kernel phases in artifact
`11009623177` from [run 36506668630](https://github.com/thesyncim/gopus/actions/runs/36506668630)
at `c6dfb561`: AMD EPYC 9V74, Go 1.27.1, GCC 13.3.0,
`GOAMD64=v1`, runtime AVX2/FMA dispatch, and five 300 ms samples per mode.
All samples report zero allocations. Each baseline/candidate kernel phase
completes successfully. The direct xcorr rows retain their separate shapes;
the pulse and best-ID helpers differ from the live finite-input PVQ search.
Sequential kernel phases retain host-load and frequency risks.

### Validation and performance follow-up

Native AMD64 CI passes at `c6dfb561`; local ARM64 exactness and allocation
results are recorded above. All 53 inventory rows retain their fixture/compiler
provenance, including current native measurements for all 11 AMD64 symbols.
Measurements from different CPUs or revisions do not establish a source-change
speed ratio. Several direct kernels trail assembly, as recorded in the inventory.

The [compiler audit](#compiler-code-generation) records dispatch, instruction
lowering, and emulated Penryn/Sandy Bridge compatibility checks. Emulation
provides CPU-compatibility evidence, not native SIMD performance evidence.

## Compiler code generation

Scope: the Go SIMD codec paths on PR #505, Go 1.27.0/1.27.1, ARM64 and AMD64.
The audit checks upstream SIMD issues against the actual codec call sites.
The [shared discussion](https://chatgpt.com/share/6aba293b-d7d4-83ed-9c75-5804702ad672)
is inaccessible from this environment; its contents are not used as evidence.

| Upstream issue | Exposure in gopus | Check or correction |
|---|---|---|
| [Partial loads cross a protected page (#81692)](https://github.com/golang/go/issues/81692) | No `Load*Part` or `Store*Part` calls. | Full vector loads require a complete vector; tails use scalar code or explicitly padded scratch. No dependency on the unreleased partial-load fix. |
| [AVX-only `Abs`/`Neg` can emit AVX2 (#81405)](https://github.com/golang/go/issues/81405) | The Go 1.27 lowering affects float sign operations and some 128-bit broadcasts. | Analysis and affected SILK kernels require AVX2. CELT uses loaded sign masks and AVX-compatible shuffle broadcasts, with scalar fallbacks when AVX is absent. Disassembly confirms `VMOVSS`/`VSHUFPS` for the float broadcast helper. |
| [AVX-only preemption corrupts 256-bit vectors (#81209)](https://github.com/golang/go/issues/81209) | 256-bit SILK and CELT paths require a runtime feature boundary. | Rewhitening, LPC analysis and warped autocorrelation have AVX2 wrappers with scalar fallbacks. All eight-lane CELT correlation entry points require AVX2 and FMA. |
| [Feature-dependent zero vectors can move above guards (#81571)](https://github.com/golang/go/issues/81571) | A source-level branch alone is insufficient on affected compilers. | Guard wrappers call separate `//go:noinline` vector bodies. Cross-compiled analysis dispatch has no SIMD instructions before its AVX2 check. |
| [Floating Min/Max is incorrectly commutative (#81468)](https://github.com/golang/go/issues/81468) | CELT uses float extrema; DNN activation clamps also need ordered x86 semantics. | Exact PVQ and DNN clamps use compare/select. Finite-only PVQ observes comparisons, not the sign of equal zero. Preemphasis falls back for NaN samples or initial extrema, with a sequential-equivalence regression test. Integer Min/Max is unaffected. |
| [Legacy SSE/AVX transition overhead (#80835)](https://github.com/golang/go/issues/80835) | Go 1.27.1 emits legacy `MOVUPS` spills/reloads in the compiled AVX2 NSQ kernel. | Code-generation exposure is confirmed; its performance cost is not isolated. The upstream [VEX fix](https://github.com/golang/go/commit/5763a306d2d31111a2ac58b4f67cf5678a17f24e) requires a separate compiler comparison. The reported upstream 65× slowdown is not a measured gopus slowdown. |
| [Portable SIMD export/import failure (#81614)](https://github.com/golang/go/issues/81614) | No portable `simd` import and no SIMD types in public API signatures. | No matching call surface. Internal `archsimd` kernels build with normal public scalar/slice APIs. |
| [AVX-512 mask-register eviction (#81767)](https://github.com/golang/go/issues/81767) | No AVX-512 vector or mask kernels. | No matching register-allocation surface. |
| [ARM64 carryless-multiply dispatch (#80991)](https://github.com/golang/go/issues/80991), [portable shift operands (#81099)](https://github.com/golang/go/issues/81099) | No carryless-multiply intrinsic or portable SIMD operation. | These reported paths are unused. CI uses Go 1.27.1. |

### Validation

The existing native SIMD A/B CI job also runs the same Go binaries under QEMU
with Penryn (no AVX) and Sandy Bridge (AVX without AVX2). It runs selected
scalar-equivalence/allocation checks and short public encode/decode workloads.
This checks CPU compatibility; it does not replace native performance or the
matched-ISA C oracle. C helper subprocesses are excluded from emulation tests
because those subprocesses would execute on the host CPU.

Run [36412029034](https://github.com/thesyncim/gopus/actions/runs/36412029034/job/108894625360)
at `2b8615ba` uses Go 1.27.1 and `GOAMD64=v1`. All five Penryn phases and
all five Sandy Bridge phases pass: CELT dispatch/allocation, analysis, DNN,
SILK, and public encode/decode smoke. The run's overall conclusion is cancelled,
so these phase results do not establish a complete CI pass.

The CELT allocation probe uses a standard 120-coefficient frame with overlap
120 and pinned tables. A separate 32-coefficient / overlap-8 case checks
determinism and finite output; that custom geometry constructs tables and is
outside the steady-state allocation probe.

Local validation cross-compiles Linux AMD64 and ARM64 SIMD test binaries with
Go 1.27.1; AMD64 uses `GOAMD64=v1`. Selected CELT scale/rotation/inner-product/comb/stereo, analysis and DNN
activation checks pass locally on ARM64. Disassembly checks the guarded entry points
and CELT AVX-only broadcast encodings. Native execution is represented by the
existing A/B evidence, and both emulated CPU feature configurations pass their
compatibility phases. Existing native performance measurements
describe their recorded revision; they do not measure these CPU safety changes.
