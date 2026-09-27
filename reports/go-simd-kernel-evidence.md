# Go kernel replacement evidence

This report tracks all 53 symbols from the 41 assembly files in the pre-port
`origin/master` tree. Every entry has a Go replacement. `archsimd` means the
Go 1.27 `simd/archsimd` implementation selected by `GOEXPERIMENT=simd`; ordinary
builds use the listed scalar Go path, and `-tags nosimd` forces the scalar
reference path.

## Correctness status

The live oracle comparisons use libopus 1.6.1 with the same effective
instruction path on the same machine. Go SIMD (`GOEXPERIMENT=simd`) pairs with the SSE/AVX2 RTCD
reference on AMD64 and the NEON reference on ARM64. Ordinary Go and `nosimd`
pair with the scalar reference (`-O3 -fno-tree-vectorize
-fno-tree-slp-vectorize`). Native AMD64 uses GCC 13.3.0; local ARM64 uses Apple
clang 21.0.0. Resolver checks validate the selected reference and its build stamp.
The native AMD64 SIMD helper reports `opus_select_arch=4` (AVX2), while its
scalar counterpart reports zero with no SIMD macros. ARM64 SIMD binds NEON
at compile time; its zero runtime arch value is not a scalar selection.
Wrong-variant, archive/header, and compiler-policy rejection tests pass in all
three local modes. The public and multistream decode helpers use the same
build-aware archive resolver as the strict CBR oracle. Short-frame, CELT header,
band-allocation, variant provenance, and SILK flush comparisons use live C
packets from the selected reference. Every case logs the C build identity and
PCM input hash. Packet bytes, final ranges, and counts are strict in short-frame,
provenance, and flush tests; header/allocation tests require exact stage fields
and counts and also log packet/range differences. Allocation fields have no
percentage allowance. Quality checks retain their normal floors without
cross-feature exceptions. Fixture bytes, hashes, and baselines are unchanged.

Decoder matrix/rate/loss/corpus/transition consumers use build-paired live C
expectations. Frozen packet inputs and fixture-honesty checks retain their
independent roles; frozen numerical expectations require their recorded
producer environment. Long-frame encoder fixture coverage remains under audit.

Malformed code-3 VBR packets are fully prevalidated before Go decodes any frame,
matching `opus_packet_parse_impl`. The stateful selected-C sequence oracle
checks valid→malformed→valid→PLC→valid histories across mono/stereo and float32,
int16, and int24 APIs. All 24 combinations of padding and later-frame overrun
match C status, PCM, and final ranges; the exact raw witness
`4b8302010000` also preserves the preceding range and following decode state.
The focused selector passes 91 events in SIMD, nosimd, and fixed-SIMD, and 92 in
ordinary and fixed-nosimd, without failures or skips. The malformed call
allocates zero after warmup. Existing code-3 one-frame/padding and PLC coverage
remains in the same selector. The sequence oracle uses its v3 wire format;
fresh-decoder v1/v2 probes keep their existing wire semantics. These claims
cover the standard decoder APIs, not cached DRED malformed-packet handling.

### QEXT PVQ refinement byte parity

`opPVQRefineNorm` materializes the product at libopus's `opus_val32` store
before computing rounding residuals. Its floor expression uses C's double
promotion for the unsuffixed `.5` literal in `celt/vq.c:op_pvq_refine`.
The N=4, K=112, up=1023 selected-C regression checks pulse/refinement values,
main/extension bytes and output coefficients. Both scalar and SIMD ARM64
pass the strict 96-frame 5.1, FOA and high-rate 5.1 sequences with exact
packet bytes/ranges and zero warm allocations. This closes the ARM64
frame-10 QEXT payload witness. Native AMD64 still requires validation of this
checkpoint; its preceding capture reports one FOA cycle allocation.
Native-96 encoder/PLC and broader feature-paired oracle validation remain open.

### Combined-extension allocation guards

QEXT PVQ refinement uses reusable pulse, refinement and rounding scratch.
DRED reset clears runtime state while retaining its model-bound backing storage.
On ARM64, the reset-inclusive 96-frame 5.1, FOA and high-rate cycles each
allocate zero in scalar and SIMD. The selected-C PVQ oracle exercises reused
scratch; base 5.1 and FOA retain exact 96-frame packet/range parity including
reset/rearm. The high-rate ARM64 frame-10 payload witness is covered by the
exact PVQ refinement proof above. The existing native CI batch also runs the independent reset-cycle allocation
guard; AMD64 validation of these allocation fixes is pending.

### QEXT float band-energy reduction

`computeQEXTBandAmplitudesF32Into` uses the selected libopus inner-product
reduction and adds the energy floor after that reduction, matching
`celt/bands.c:compute_band_energies`. The live oracle verifies the C dispatch
and compares amplitude and log-energy bits for 32 cases / 96 band energies.
On ARM64, scalar and SIMD candidates match exactly; the sequential-reduction
negative control differs from NEON in 24 of 96 energies. The paired public
QEXT stateful/native-96 regression selectors pass in both modes. Native AMD64
runs the same new oracle inside the existing QEXT CI phase. This kernel proof
does not resolve the separate ARM64 frame-10 combined-extension packet witness.

### Fixed-point analysis and QEXT transforms

At `e8eb7bc5`, local ARM64 validation on an M4 Max with Go 1.27.0 shows that the
fixed-point encoder analysis path matches selected C for 24 rate/channel/
duration combinations: 16, 24, and 48 kHz; mono and stereo; and 5, 20, 40, and
60 ms frames. The oracle compares raw downmix and input-history buffers,
analysis features, and returned state across 12-frame sequences and reset
replay; warmed analysis allocates zero. Public automatic fixed encoding matches
selected C for eight-frame 16 kHz mono SILK and 48 kHz mono Hybrid sequences,
including reset replay, packet bytes, ranges, and zero warmed caller-buffer
allocations in ordinary, SIMD, and `nosimd` builds. The combined fixed+QEXT
build pairs these public cases with the matching `FIXED_POINT`+`ENABLE_QEXT` C
archive while runtime QEXT is off. Native AMD64 confirmation for these analyzer
and public-auto cases is pending. Automatic CELT and active public fixed-QEXT
encoding remain outside this evidence.

The fixed+QEXT Q31 transform oracles at `d58977eb` and `65cc937c` match the
selected `FIXED_POINT`+`ENABLE_QEXT` C implementation exactly in local ARM64
ordinary, SIMD, and `nosimd` builds. Forward FFT/MDCT coverage includes 48 and
96 kHz modes, all tested shifts, and silence/headroom cases. The inverse MDCT
oracle covers eight mode/shift/stride combinations at 48 and 96 kHz. Each
inverse case also passes its warmed zero-allocation check. Native AMD64
confirmation for these transform gates is pending. These results prove
transform-stage parity; they do not establish public 96 kHz decoder parity.

### Fixed encoder controls and QEXT energy math

Local ARM64 / M4 Max / Go 1.27.0 evidence at `ca42ae29` covers selected-C
packet bytes and final ranges for LFE with requested Auto/SILK/Hybrid/CELT,
native Q24 energy masks, explicit mask clearing, mask lifetime across resets,
and short-frame SILK requests that resolve to CELT. Ordinary, SIMD, and
`nosimd` fixed builds pass the focused controls suite. The combined fixed+QEXT
runtime-off checks use the matching C archive; `2885130a` selects that build's
Q31 forward transform. The float short-frame and energy-mask wire checks use
matching ordinary or QEXT-enabled C references.

At `8d9750d0`, fixed+QEXT band energies use the source Q24 logarithm polynomial.
The independent amp2Log2 oracle includes zero, epsilon, large and randomized
amplitudes, both channel counts, and partial-band boundaries. Ordinary fixed
and fixed+QEXT checks pass, as does the SIMD fixed+QEXT oracle. A clean source
archive of this commit passes the combined SIMD controls and energy checks
without any unfinished encoder/decoder integration files. Native AMD64
confirmation is pending. Automatic stereo CELT, public combined-build surround,
and active fixed-QEXT encoding remain outside these focused gates. Received
fixed-QEXT decoder coverage is documented below; complete extension parity
remains open.

At `e3ab22d9`, public 96 kHz int16 and int24 decoding reuses decoder-owned
scratch and preserves integer soft-clip history. Selected float-QEXT C checks
cover mono/stereo output samples and ranges, reset replay, +8 dB gain, and
received→undersized→received histories with exact resumed PCM. Ordinary,
SIMD, and `nosimd` ARM64 builds pass with zero warmed allocations. This
checkpoint covers the float codec build; fixed-QEXT received-frame evidence
appears below. The early QEXT CI selector matches 30 intended test names,
including these integer wrapper gates; its expanded local SIMD gate passes.
At `c29a4833`, a persistent mixed int16/int24 sequence also matches selected C
PCM and ranges at +8 dB gain for mono/stereo, including reset replay and zero
warm allocations for consecutive calls in both formats. Ordinary, SIMD and
`nosimd` ARM64 builds pass; this remains float-QEXT codec evidence.
At `3b8fca64`, fixed-QEXT exponentiation matches selected C for 526 inputs;
paired denormalisation and anti-collapse oracles pass in the combined SIMD
build. The fixed-only denormalisation and anti-collapse gates also pass. These
are local ARM64 kernel results; native AMD64 confirmation is pending. The early
fixed-QEXT gate includes these oracles.

### Fixed-QEXT received-frame decoding

The fixed-QEXT received-frame checkpoint covers all 54 selected-C cases at
8/12/16/24/48/96 kHz across mono, stereo, stereo-packet-to-mono output, and
float32/int16/int24 APIs. Every PCM sample and final range matches. Each case
covers three persistent frames, +5 dB gain, explicit phase-inversion control,
reset replay, and zero warmed allocations. Ordinary, SIMD, and `nosimd` ARM64
builds pass. Three main-only format cases also match, and 18 rate/format cases
verify received→undersized→received state with exact resumed PCM and range.

Independent band oracles check dual-stereo extension budget halving and the
qn==1 branch without angle refinement, including both entropy coder states.
A selected-C cubic leaf oracle checks the zero-resolution branch. Public QEXT
decoder tests select fixed or float C according to the Go codec build; the
native 96 kHz tests include seven received packets per mono/stereo sequence
and five active cross-frame postfilter frames per channel layout.

An isolated source overlay passes the public and PVQ/band oracles without the
unfinished encoder integration. Fixed-only band oracles and the float-QEXT
integer-wrapper regressions pass independently. The type-parity guard retains
its 26 existing findings with no baseline edits. The native CI gate selects
these public tests and paired PVQ/band oracles explicitly.

Native AMD64 at `f2af896b` also passes the received-frame/band gate in SIMD
and `nosimd`: 224 passing test nodes per lane, with no failures or skips.
The same early artifact passes 48 fixed-QEXT analysis nodes, 35 transform
nodes, and 40 automatic-CELT/surround/allocation nodes in each lane. This
validates those bounded checkpoints on AMD64; it predates the loss/recovery
checkpoint and does not establish complete codec or combined-feature parity.

### Fixed-QEXT loss and malformed-packet recovery

The fixed Q31 decoder implements periodic and noise concealment, recovery
energy adjustment, and prefilter folding at 48/96 kHz core geometry. Selected-C
public tests cover eight rate/channel/control configurations spanning every API
rate, stereo downmix, and both stereo phase-inversion settings. Single-loss and
valid one-byte CELT-body sequences pass all 48 format/configuration cases. Six
consecutive losses followed by received→loss recovery pass 24 further cases,
including the noise-PLC state. All compare exact PCM bits and final ranges at
+8 dB gain, replay after reset, and require zero warm loss allocations.

A persistent C decoder also checks received→malformed code-3→received histories
for float32/int16/int24 APIs, including the error status and resumed output.
The status-capable oracle retains failed calls in its sequence. Isolated local
ARM64 SIMD and `nosimd` checks pass together with the 54 received-frame cases
and the independent band oracles; ordinary mode passes the focused loss gates.
The type-parity guard retains 26 findings without baseline changes. Native
AMD64 `54654e25` passes the expanded receive/Hybrid/PLC/band gate in both
instruction lanes: 416 test nodes per lane, without failures or skips.

### Fixed surround and automatic CELT encoding

The selected fixed-reference automatic-CELT sequence matches every packet byte
and final range for eight persistent frames in mono low-space and stereo
automatic-channel configurations, including reset replay and zero warmed
allocations. In a QEXT build, the prefilter uses the Q31 mode window and the
band encoder uses Q31 split gains even with the extension disabled at runtime.
A direct selected-C prefilter oracle checks the coefficient path independently.

Seven multistream surround fixtures match selected C packets and ranges over
six frames with two resets: 3/4/5/6/7 channels at 10 ms, 7.1 at 20 ms CBR,
and 7.1 at 24 kHz/40 ms. The fixed analyzer matches raw band energies, Q24
masks, and per-stream mask routing. The mono QEXT-build surround regression
matches all logical C state across a four-frame sequence with a reset and
active prefilter. These QEXT-build tests keep the runtime extension off.

Isolated ordinary, SIMD and `nosimd` ARM64 checks pass. Fixed-only and default
float surround gates also pass. Public surround/projection warm encode
allocation guards pass for 5.1/7.1 and FOA/SOA/TOA. The native `f2af896b`
early artifact passes this selected gate in SIMD and `nosimd`. Active combined
DRED+QEXT validation remains pending.

### Combined DRED and QEXT reference identity

Combined-feature C helpers use isolated `dred-qext-scalar` and
`dred-qext-simd` archives with ENABLE_DRED, ENABLE_DEEP_PLC and ENABLE_QEXT.
The resolver rejects conflicting feature selections, mismatched instruction
paths, unexpected OSCE/fixed-point flags, and missing or changed pinned model
source hashes. Existing DRED helpers retain runtime QEXT-off unless their
protocol explicitly enables it. The active combined helper enables QEXT in
both implementations and checks the control on each elementary stream.

Isolated reference-selector regressions pass locally in SIMD and `nosimd`.
At 384 kb/s, 96-frame 5.1 and FOA public sequences match every packet and final
range in both lanes, with zero warm per-frame and full-cycle allocations.
Reusable extension-list and padding buffers keep multistream framing allocation-free.
The float QEXT reservation follows C compound-assignment truncation; the direct
mono-child witness matches selected C bytes/ranges in both instruction lanes.

The complete 768 kb/s 5.1 witness remains a hard gate. At frame 10, coupled
stream 1 differs only in its 215-byte QEXT payload, starting at byte 173.
CELT main bytes, DRED bytes, packet lengths, masks, rates, and final ranges match.
The failure reproduces in ARM64 scalar and SIMD. Native AMD64 at `54654e25`
matches all 96 packets/ranges for this witness in both lanes but fails its
allocation guard; FOA also exposes one native allocation. Both failures remain
selected by CI.

### Fixed-QEXT public encoder and Hybrid decoder checkpoint

The active 48 kHz fixed-QEXT encoder matches all eight baseline packets and
24 expanded cases covering frame sizes, CBR/CVBR/VBR, mono/stereo, three input
formats, caller capacity and reset. VBR constraint transitions, including LFE
and energy-mask controls, match selected C. Normal and greater-than-1275-byte
output paths allocate zero after warmup.

Native 96 kHz fixed-QEXT encoding matches selected FIXED_POINT+ENABLE_QEXT C in
96 combinations: two channel counts, four durations, runtime QEXT on/off,
256/4000-byte buffers, and three bitrate modes. Each checks four frames with
mixed int16/float32/int24 input, a reset, exact packets/ranges and zero warm
allocations. The evidence covers CELT-only ApplicationAudio with forced channel counts.
Automatic stereo channel selection remains an identified native-route gap;
other applications and compatibility routes require separate parity evidence.

Hybrid selected-C decode tests cover active/absent/same-packet ignored QEXT,
persistent state and reset, CELT↔Hybrid transitions, loss recovery, lower rates,
stereo downmix, FEC and both explicit redundancy directions. PCM and final
ranges match with zero warm allocations. Redundancy packets use the selected
fixed-QEXT C encoder. Public encoder and Hybrid decoder gates pass local ARM64
ordinary, SIMD and nosimd builds. Native AMD64 `54654e25` passes all 176 encoder
and 416 received/Hybrid/PLC/band test nodes per instruction lane without skips.

### Extension checkpoint

QEXT at 48 kHz (`6e20f360`) passes 60 configurations / 180 sequential frames
with exact packets, encoder/decoder final ranges, lengths, and every float PCM
bit in ordinary, SIMD, nosimd, and purego ARM64 builds. Native 96 kHz QEXT at
`d3f44188` uses the pinned C window, FFT, and MDCT tables plus selected-C comb
arithmetic. Seven received frames in mono and stereo match all 40,320 PCM
samples and final ranges per local build. Active cross-frame postfilter cases
also match five frames each in mono/stereo with zero warm decode allocations.
All four local builds pass the 17-node focused suite without failures/skips;
the eight valid direct comb oracle cases pass. Native AMD64 at `08a11a0f`
passes 104 QEXT test nodes in SIMD and nosimd, including native 96 kHz,
without failures or skips.

Wide custom modes at `736a2d27` pass 154 stateful configurations / 2,046 steps
per local build: 946 received packets and 1,100 loss steps, with exact packet
bytes, encoder/decoder ranges, float PCM bits, and int16 PCM. All 22 mode/channel
pairs allocate zero after warmup through encode/decode and periodic/noise
concealment/recovery. The full custom package passes ordinary, SIMD, nosimd,
and purego (385 passing nodes, seven existing capability/duplicate-family
skips). New geometry, stateful, and allocation leaves have no skips. Native
AMD64 at `8bc2ed7c` passes 384 test nodes plus seven existing skips in each
SIMD/nosimd lane, with no failures.

OSCE public PCM matches all 24 sequential packets in seven configurations:
mono/stereo LACE, NoLACE, BWE, and stereo NoLACE+BWE. Every return count and
float PCM bit matches in ordinary, SIMD, and nosimd ARM64 builds; resetting
replays the complete exact history. Warm public single-stream and active
multistream decoding allocate zero. Callback cleanup is checked after a
malformed packet. Both Go modes use scalar OSCE DNN arithmetic and the same
OSCE-enabled scalar C reference; compiler contraction and intermediate C
float/double widths are preserved. The independent feature oracle matches all
372 feature values, two bit-count values, and four periods over 16 frames at
each LPC order (10 and 16), with zero warm allocations in all three builds.
The full SIMD root/OSCE/multistream suite passes 20,279 test nodes with four
existing opt-in skips (two DRED quality and two OSCE trace diagnostics), and
no failures. The focused ordinary/nosimd checkpoints pass 97 nodes each plus
two existing trace skips; the additional feature gate passes separately.
Native AMD64 at `08a11a0f` passes the exact-PCM OSCE gate (36 nodes).

AMD64 LPCNet production analysis routes pitch correlation, inner products, and
the `dnn/lpcnet_enc.c` `celt_fir` stage through the selected C arithmetic. The
FIR seeds four sums from input samples before calling `xcorr_kernel`; x86 maps
the SSE and later dispatch slots to SSE even/odd accumulators, including arch
4. Go uses the existing SSE-order kernel only in the amd64 SIMD
build. A live selected-C `celt_fir` oracle checks all 160 outputs with order 16
and nonzero seeds; local ARM64 ordinary/SIMD/nosimd paths match exactly with
zero warm allocations. The native 16-frame full-prefix state gate passes in
capture `36311118585` at `6c730f47`: AMD64 SIMD neural analysis has 242 passing
events, three existing skips, and zero failures. This includes the full-state
oracle and selected-C FIR checks. The earlier `08a11a0f` and `6ce253b2`
artifacts recorded frame-0 differences in `exc_buf[352]`, `lp_buf[353]`, and
`xcorr_features[188]`; the `6c730f47` capture passes all 16 frames. Native
nosimd neural checks pass 224 nodes with three existing skips.

LPCNet at `9faa9bc1` matches all 224 raw correlations, DNN pitch, and 36 feature
bits across 40 frames in ordinary, SIMD, and nosimd ARM64 builds, with zero warm
stage allocations. At `3eb38788`, selected-C Burg, predictor, and neural fade
arithmetic matches all 119 concealed mono 20 ms frames in the sustained-loss
quality history. At `e53ca99c`, the full timeline covers all 220 slots: 100
received frames and 120 concealed frames, including ordinary PLC for the final
lost packet without a recovery carrier. Every PCM bit, frame index/kind, return
length, and final range matches the selected C decoder in all three ARM64
modes. The 119 loss frames and decoded PCM splice remain unchanged; the clean
reference frame indices account for encoder lookahead.
Thirty mono/stereo cases cover five API rates and 20/40/60 ms nil/recovery
requests, with zero warm allocations during active carrier/loss/recovery
cycles. Both explicitly enabled sustained-loss quality gates pass with their
thresholds unchanged;
Go-versus-C concealed PCM has correlation and RMS ratio 1.0. The quality
references account for the confirmed restricted-low-delay encoder delay: live
C `OPUS_GET_LOOKAHEAD` returns 120 samples at 48 kHz, and the mode-paired
DRED-enabled C helper's no-loss decode of the same 220 generated packets has
its best source alignment at 120 samples (correlation 0.953347 versus
-0.277066 at zero offset). Both quality builders shift only their clean reference slices; decoded PCM, packet
bytes, loss schedule, frame counts, final ranges, and thresholds are unchanged.
On M4 Max / Go 1.27.0 / Apple clang 21.0.0, ordinary and nosimd use scalar
Go and scalar C helpers; their zero-offset diagnostic PLC/DRED envelopes
0.26871/0.29676 become 0.50838/0.71983 with the source offset. The SIMD Go
lane pairs with the ARM64 NEON C reference; its envelopes change from
0.24121/0.28734 to 0.55517/0.70999. The two enabled quality gates pass in all
three modes, and the full 220-slot and 119-loss raw-bit selectors still pass
unchanged. In native capture `36311118585` at `6c730f47`, SIMD DRED passes 41
events including the full sequence, loss, and zero-offset quality gates;
nosimd raw PCM also passes both sequences. The native early capture
`36312921742` at `49595303` passes 42 DRED events in both SIMD and nosimd with
no failures or skips. This includes timing, 119-loss and full 220-frame
raw-PCM gates, and both quality leaves with the C-confirmed 120-sample
reference alignment. Go and C envelopes match at 0.52895/0.70459 in SIMD and
0.51639/0.71825 in nosimd. The reference slice offset retains all loss frames
and decoded slots.

The strict full-sequence DRED encoder oracle feeds the same 220 caller-supplied
PCM frames to Go and the selected C archive, with matching controls and a
shared 4,000-byte packet budget. Ordinary, SIMD, and nosimd each match all 220
packet byte strings and final ranges for mono and stereo; 219 packets per
sequence carry DRED. The selected archive supplies both C's embedded models
and the Go model blob. After warming the active model and controls, three full
220-frame public `Encode` cycles allocate zero times in both channel layouts.
DRED bitrate reservation feeds the source-matched mode/channel decision; the
original per-frame budget remains in use for extension planning and packet
padding. The earlier captures at `08a11a0f`, `6ce253b2`, and `6c730f47` retain
their own zero-offset quality, state, and measurement provenance.

Fixed-point C references at `67731222` pair scalar and SIMD feature builds,
validate archive/header/compiler identity, and reject mismatched neural/QEXT
features. At `20901221`, strict decoding covers 1,440 received configurations
× four frames, 864 loss/recovery configurations × nine frames, 7,200 rate
configurations × four frames, and 30 sustained-loss/rate cases × 19 frames.
All 9,534 configurations / 42,906 steps per lane match float32 PCM bits,
int16/int24 samples, counts, and final ranges in ordinary, SIMD, and nosimd
ARM64 builds. Thirty additional Hybrid→CELT transition histories cover five
API rates, both channel counts, and three decode gains over nine steps.
The focused suite reports 11,202 passing events per lane without failures or
skips. All 60 active loss/recovery allocation cases and four transition
allocation cases allocate zero after warmup. Native AMD64 at `8bc2ed7c`
passes the 9,534-configuration integer-output matrix in both SIMD/nosimd.
Native AMD64 at `08a11a0f` passes all 11,801 expanded fixed stateful-decode
nodes in each SIMD/nosimd lane, including float output, transitions, and FEC,
without failures or skips. The full SILK packet API at `0dcd8581` matches the linked C `silk_Encode`
packet bytes, lengths, final ranges, and entropy bit counts for all 108 existing
mono/stereo cases plus 12 six-packet complexity/reset histories in ordinary,
SIMD, and nosimd ARM64 builds. Configured analysis lookahead and NLSF
interpolation follow `silk_setup_complexity`; buffer insertion retains its
fixed lookahead. The signed16 transport is independently packet/range-neutral
for the original Go input. Four existing warm allocation cases pass at zero.
The full SILK package passes 1,086 ordinary / 1,100 SIMD nodes with five
existing optional skips per mode; the new API gates have no skips.

Fixed FEC at `69792f0e` matches selected C output bits, counts, and final
ranges through mode/rate/channel/gain/reset histories. The focused suite passes
577 events in each ordinary, SIMD, nosimd, and purego ARM64 mode, without
failures/skips. Coverage includes 90 FEC matrix cases, four 40/60 ms histories,
and 16 no-LBRR CELT→SILK recovery histories. Caller-owned SILK FEC and integer
Hybrid concealment retain fixed-width state. Warm FEC, transitions, and active
SILK receive/loss cycles allocate zero; malformed framing preserves decoder
state. The float build passes its 458-event focused suite in ordinary and SIMD.
Native AMD64 at `08a11a0f` passes the expanded fixed decode/FEC gate in both
SIMD/nosimd lanes. The ARM64 fixed-point outer-encode checkpoint below covers
Audio and LowDelay. A selected forced-CELT VoIP follow-up covers the integer
high-pass path. Local checkpoints `966f9682` and `681e24ac` pass 140 forced
SILK/Hybrid public frames, nine packets in one persistent forced-mode
transition sequence, and six strict matched-float cases in SIMD/nosimd;
default shared-path gates also pass. At `e8eb7bc5`, public automatic fixed
encoding additionally matches selected C for the tested SILK and Hybrid
sequences described above. Four fresh 96 kHz float-QEXT packet/range cases
pass ordinary, SIMD, and `nosimd`. Automatic fixed CELT and surround evidence
appears above. The fixed-QEXT public encoder checkpoint above covers active 48 kHz and native
96 kHz CELT encoding. Combined DRED+QEXT retains the explicit high-rate residual
above; full codec/extension parity is not established.

[Native run 36284981747](https://github.com/thesyncim/gopus/actions/runs/36284981747)
at `8bc2ed7c` supplies completed extension and benchmark phases; the full
workflow is canceled and supplies no complete CI pass. SIMD neural gates at
that revision fail four direct-kernel leaves, 41 analysis leaves, and six
initial-latent leaves. These failures remain hard gates. AMD64 neural fixes
at `da837716` use the actual selected C activation/linear implementation, AVX2
unsigned quantization and Subias, and runtime CPU guards. Sparse-float RDOVAE
at `a3488fc1` follows the selected C ordered FMA updates. Four local ARM64
modes each pass 240 selected-C nodes without failures/skips, with zero warm
allocations; native AVX2 confirmation remains pending. Lower x86 dispatch
slots have a safe scalar fallback without an exactness claim. The bounded
IF/FARGAN correction at `48f0435f` matches raw IF, gain, and full continuity
state in three ARM64 modes; the sustained-loss DRED gate above uses the full
concealed PCM sequence.
Coverage mappings and immutable fallback inputs address the earlier full-job
coverage failures without changing tolerances or fixtures.

[Native run 36286208931](https://github.com/thesyncim/gopus/actions/runs/36286208931)
at `c4f4b3cc` passes the fixed SILK packet API (128 test nodes per SIMD/nosimd
lane), fixed integer decoding, QEXT, and custom modes. The neural references
fail during concurrent shared-source extraction/configure, before numerical
comparison; the nosimd build-config job fails for the same reason. The full
workflow is canceled. The common process lock and complete source/build
publication at `79b25887` address this race; native confirmation remains open.
Local process exclusion, owner-exit release, incomplete-source rejection, and
a cold concurrent C build pass; Linux/Windows oracle packages cross-compile.

[Native run 36288212304](https://github.com/thesyncim/gopus/actions/runs/36288212304)
at `3ef3d5e1` supplies completed SIMD phases: 11,201 fixed decode tests, 128
fixed SILK API tests, 103 QEXT tests, 384 custom tests plus seven existing skips,
3,777 multistream strict decode tests, and 181 encode-budget tests. Each
completed phase exits zero. The artifact ends during neural tests, without
numerical results for that phase or nosimd/performance phases. It does not
establish a complete CI pass or update the performance measurements.

[Native run 36296680326](https://github.com/thesyncim/gopus/actions/runs/36296680326)
at `1e2dbe77` passes the SIMD default-tag full suite: 27,548 passing test
nodes, 25 existing skips, and zero failures. Both SIMD/nosimd pass fixed
decode/FEC (11,774 nodes), fixed SILK API (128), QEXT (103), custom modes
(384 plus seven existing skips), multistream strict decode (3,777), public
valid decode (1,441), native-rate/DTX (376), low delay (121), and multistream
encode budgets (181). Direct neural kernels and initial DRED latents pass.
This run records two SIMD LPCNet analysis leaves failing (1920/2880 samples):
the first pitch feature differs despite all 16 same-input PitchDNN stage
records matching. Its DRED quality measurements use zero-offset clean-reference
windows; the C-confirmed 120-sample alignment and later native results are
described below. The `6c730f47` capture passes the LPCNet full-state and
raw-DRED PCM gates; its nosimd quality result uses the zero-offset reference.
The corrected native alignment passes in run `36312921742` at `49595303`.
With the two source-audited multistream surround-trim API retirements
recorded, the full artifact comparator passes against the captured artifacts;
the 16 comparator unit tests also pass. Fixture regeneration intentionally
exits nonzero.

[Native early run 36309916340](https://github.com/thesyncim/gopus/actions/runs/36309916340)
at `08a11a0f` uses Intel Xeon 8573C, Go 1.27.1, and GCC 13.3.0. SIMD and
nosimd each pass 5,718 fixed-encode events, including the selected forced-CELT
VoIP short/mixed/long/reset/low-space gates, with no failures or skips. Both
lanes also pass 11,801 fixed stateful-decode events, fixed SILK 129, QEXT 104,
and OSCE exact-PCM 36; DRED initial-latent gates pass eight events in each
lane, and neural primitive gates pass 44 SIMD / 36 nosimd events. Nosimd neural
analysis passes 224 nodes with three existing skips. The full-state frame-0
differences and DRED raw-PCM mismatch in this capture are superseded by the
passing 16-frame and full raw-bit gates at `6c730f47`. The DRED quality figures
in this artifact use zero-offset clean-reference windows; aligned native
quality gates pass in run `36312921742` at `49595303`. This early artifact is not a full CI
pass.

The latest native six-workload early E2E capture is run `36331924965` at
`f2af896b` on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3.0. It uses four interleaved
500 ms samples per workload and build; all 72 samples report zero allocations.
Run `36319060882` at `17e48afb`, full artifact `10931588043`, supplies the eleven direct AMD64
symbol rows below, with five 300 ms samples per mode and zero allocations. All
baseline, scalar Go, and SIMD benchmark phases exit successfully. The six-row
table is independent of that full artifact's three-sample E2E phase. Earlier
captures at `6c730f47`, `49595303`, and `5e14f0d8` retain their own revision and
CPU labels; their ratios do not establish cross-host gains. The earlier complete
direct capture at `1e2dbe77` on EPYC 9V45 also measures four interleaved
caller-buffer samples, 51,667.5 → 44,372 ns/op (14.1% less time), with zero
allocations; Hybrid decode takes 6.1% more time.

A separate early capture at `fe0f867d`, [run 36308001239](https://github.com/thesyncim/gopus/actions/runs/36308001239),
measures caller-buffer encoding on AMD EPYC 7763 / Go 1.27.1. Four interleaved
500 ms samples give assembly `8ac93c85` a 92,040.5 ns/op median (91,856–92,321)
and SIMD `fe0f867d` an 86,354.5 ns/op median (86,173–87,616): 6.18% less time,
with 0 B/op and 0 allocs/op for every sample. This caller-buffer pair is
separate from the complete six-row table at `1e2dbe77`. The `6ce253b2` early
capture, [run 36308889854](https://github.com/thesyncim/gopus/actions/runs/36308889854),
measures four interleaved 500 ms caller-buffer samples on the same EPYC 7763
and Go 1.27.1 toolchain: assembly `8ac93c85` has a 91,695 ns/op median
(90,737–91,775), and SIMD `6ce253b2` has a 70,920 ns/op median
(70,327–71,161), 22.66% less time. All samples use 0 B/op and 0 allocs/op.
The `08a11a0f` early capture, [run 36309916340](https://github.com/thesyncim/gopus/actions/runs/36309916340),
measures four interleaved 500 ms samples on an Intel Xeon 8573C / Go 1.27.1:
assembly `8ac93c85` has a 95,858.5 ns/op median (95,794–96,701), and SIMD
`08a11a0f` has a 64,380.5 ns/op median (63,954–65,605), 32.84% less time.
All samples also use 0 B/op and 0 allocs/op. These early measurements are
caller-buffer only, on separate CPUs, and do not establish a cross-host
revision gain or a VoIP-specific performance result. The complete six-row
table remains at `1e2dbe77`; the early native script collects four
interleaved 500 ms samples for all six E2E benchmarks across baseline, SIMD,
and nosimd binaries.

The integrated multistream API writes into caller-owned output and passes
7,342 package results in each local ordinary/SIMD lane. Final-stream CBR
framing pads short DTX packets to the remaining budget, matching libopus
`opus_multistream_encode_native`. Three 120-frame silence/mixed/recovery
histories match every packet/range; 72 direct C repacketizer cases cover
ordinary/noncanonical padding, extensions, multiple frame layouts, and length
boundaries. All three local modes pass these gates at zero warm allocations.
Wide custom modes bypass standard 21-band surround scratch; both ordinary
and SIMD full custom suites pass 385 events with seven existing capability
skips. Native confirmation of this integration remains pending.

Strict matched CBR oracle, 19 configurations and 2,175 packets per lane:

| Lane | C reference | Exact configurations |
|---|---|---:|
| amd64 Go SIMD | SSE/AVX2 RTCD, gcc | 19 / 19 |
| amd64 ordinary Go | scalar, gcc | 19 / 19 |
| amd64 `nosimd` | scalar, gcc | 19 / 19 |
| arm64 Go SIMD | NEON, Apple clang 21 | 19 / 19 |
| arm64 ordinary Go | scalar, Apple clang 21 | 19 / 19 |
| arm64 `nosimd` | scalar, Apple clang 21 | 19 / 19 |

Each lane has zero packet-byte and final-range differences. All three native
AMD64 lanes also pass at `036c4d51`.
Local ARM64 results include the Hybrid transient gate and explicit gain-fade
contraction. This CBR subset does not establish full codec parity.

The current ARM64 SIMD live variant audit passes all 92 cases with exact
packet bytes and final ranges and no severe quality gaps. The separate
16-case CELT short-frame variant sweep passes ordinary, SIMD and `nosimd`,
with no failures or skips. Both use matched live references and identical
input hashes. These gates do not establish broader extension parity.
The resolver/rejection matrix passes in all three local modes. An actual SIMD
test invocation with a scalar C override fails before encoding as required;
unchanged fixture coverage, hashes, and stable ordering also pass.

The Hybrid low-complexity gate checks all eight frames of mono 10 ms and stereo
20 ms SWB 48 kbps VBR at complexities 0 and 1, including packet bytes and final
ranges. It passes in all three local modes, as does the actual-C `gain_fade`
float-bit oracle across 13 in-place mono/stereo cases at 48/24 kHz. Warmed
caller-buffer complexity-0 encode checks report zero allocations.

CELT deemphasis runs the exact sequential libopus recurrence, including
`VERY_SMALL` and memory updates on silence. All three local modes pass the six
live-C helper tests, signal/silence state carry and downsample checks, 40 public
silence decode configurations, and both PLC stage cases. Multistream fade-out
requires actual decoded Hybrid history; exact live-C mono/stereo tests cover
fresh SILK, Reset after nonzero Hybrid history, and real Hybrid-to-SILK
transitions. Warmed single-stream public decode
and mono/stereo silence-transition guards report zero allocations. The touched
root, CELT, and multistream packages compile for Linux AMD64 with SIMD;
that cross-compilation provides no native runtime parity evidence.

Transition concealment uses the pinned C 5 ms bound and applies recursive
output gain before crossfading; the completed outer frame applies gain again.
The fade rounds the window square, subtraction, and second product separately,
then follows the selected C build's first-product/add contraction. Periodic
PLC energy follows each paired target's reduction and FMA order. All local
lanes match the standalone gain probes and all five complete frames at gains
0 and ±768. The strict 30-case transition/recovery matrix covers both
Hybrid/CELT directions, five API rates, mono/stereo, and three gains: ordinary
and `nosimd` pass 30/30, as does SIMD. A separate 60-case same-history
standalone PLC matrix passes 60/60 in all three local modes. SIMD FIR tail
products round separately before ordered addition, matching the selected C
kernel. Eight direct FIR and four IIR cases also pass each local mode.
Per-step lengths, ranges, and every PCM bit come from the matched stateful C
decoder. Native `f6952200` passes all 30 transition cases, all 60 standalone
stages, and the gain/replay probes in both lanes. The ARM64 FIR tail checkpoint
is `f975f635`; its source change has no AMD64 arithmetic effect.

SILK loss synthesis uses the current frame's subframe geometry while random
excitation selection uses the saved good frame's geometry and full excitation
history. The multistream decoder preserves the preceding packet's TOC duration
as its PLC chunk bound and synthesizes at least 10 ms of SILK before trimming
a shorter requested prefix. At `7d97ed7e`, both original scalar residual packet
histories match all five frames and their independent 5/10 ms PLC probes in
all three local modes. Another 18 mono/stereo NB/MB/WB loss-and-recovery cases
match lengths, ranges, and every sample for 20→10, 10→20, and 10→15 ms requests.
Native `af24b0e1` passes the same history and duration cases in both modes.
General fractional CELT loss requests and concealment following transition
redundancy need further coverage; the focused passes do not establish full
decoder parity.

The permanent public encode-then-decode gate at `6bdc651a` requires exact
output lengths and every float32 bit for all three public output formats:

| ARM64 lane | Exact configurations | Encoded packets | Exact format decodes |
|---|---:|---:|---:|
| SIMD | 1,440 / 1,440 | 4,320 | 12,960 |
| Ordinary scalar | 1,440 / 1,440 | 4,320 | 12,960 |
| `nosimd` scalar | 1,440 / 1,440 | 4,320 | 12,960 |

All lanes have zero failures or skips. NEON int16 conversion rounds exact
half ties away from zero in full 16-sample blocks and ties to even in the
scalar remainder, matching C. Actual-C boundary tests cover ties, adjacent
representable values, NaNs, infinities, clipping, and block tails; unit-range
and saturating conversion paths allocate zero in steady state.

Multistream decode assertions require exact float bits and integer samples,
including mode transitions. The complete 3,640-case strict ARM64 sweep at
`b3c81a20` has no failures or skipped cases:

| ARM64 lane | Passing cases | Failing cases | Surround / discrete / projection / Go-encoded failures |
|---|---:|---:|---:|
| SIMD | 3,640 | 0 | 0 / 0 / 0 / 0 |
| Ordinary scalar | 3,640 | 0 | 0 / 0 / 0 / 0 |
| `nosimd` scalar | 3,640 | 0 | 0 / 0 / 0 / 0 |

The rotation coefficient producer rounds gain-squared, multiplication by 0.5,
and the complement separately, matching the selected C translation unit.
Actual-C rotation outputs and two complete packet regressions check every
sample and final range. Independent root runs repeat the full matrix after
merging the incoming transition-order and scalar-correlation commits. These
counts complement the public decoder gate above.

The scalar encoder pitch path uses C's ascending four-lag accumulation. ARM
SIMD uses one ordered vector chain across lags and the selected NEON inner
product for fine candidates. All 21 actual-C raw-bit cases, four near-tie
pitch cases, and warm allocation checks pass. All 48 mono stateful transition
configurations pass 40 packet/range frames in all three ARM modes. Native
confirmation of these new encoder gates is pending.

Initial encoder channel state and reset high-pass memory match C. Direct
auto/forced-SILK checks compare three packets and ranges each; projection
checks five whole packets, all 25 elementary packets, and ranges, both fresh
and after reset. All three ARM modes pass.

The live decoder fixture consumers use the build-paired C reference on the
same frozen input packets and require exact lengths. Separate producer checks
reproduce 201 frozen PCM cases byte-for-byte (29 matrix, 18 loss, 24 corpus,
130 API-rate cases) on their recorded ARM producer configuration. A missing
producer compiler/platform is a strict availability failure, not a codec
mismatch. Current Linux CI does not match the historical Debian GCC 12.2
producer for its frozen matrix/loss fixtures.

Native AMD64 early evidence at `f3176763`, from
[run 36262586916](https://github.com/thesyncim/gopus/actions/runs/36262586916),
uses AMD EPYC 7763, Go 1.27.1, and GCC 13.3. SIMD C reports AVX2 dispatch
(`opus_select_arch=4`); scalar C reports zero and no SIMD features.
The strict 3,640-case multistream/projection sweep has no skips:

| AMD64 lane | Passing cases | Failing cases | Surround / discrete / projection / Go-encoded failures |
|---|---:|---:|---:|
| SIMD | 3,640 | 0 | 0 / 0 / 0 / 0 |
| `nosimd` scalar | 3,640 | 0 | 0 / 0 / 0 / 0 |

Both complete native sweeps are exact. Both lanes also pass all 108 DTX and
260 native-rate encode configurations with strict bytes and final ranges;
those passes do not apply to the six ARM64 DTX residuals. The SSE postfilter
uses one arithmetic prefix across its history/current-frame storage boundary,
matching C. Its 754 failing configurations at `af24b0e1` all pass at `f3176763`
on the same CPU model. Eight constant-boundary cases, two ramp/offset cases,
and both PLC stage sequences pass against live C, including complete
postfilter input, incoming deemphasis memory, direct output, and in-place PCM.

Native CELT raw-bit xcorr checks pass 216/216 leaves; SILK passes 24/24.
The cases cover signed zeros, short binary32 FMA rounding, distinct NaN
payloads, signaling NaNs, operand priority, horizontal reductions, and scalar
tails. The cold replay follows the linked C instruction order while finite
results retain the SIMD path. All 38 CELT production cases and the SILK
finite/exceptional warm allocation guard report zero allocations. The actual
C archive, build identity, primitive binary, and disassembly are retained in
the artifact; linked full-kernel results determine operand-priority behavior.

Both lanes pass the six deemphasis helpers, state/downsample coverage,
40 public silence cases, eight FIR and four IIR cases, all 30 full transition
cases, 60 standalone stages, 18 SILK loss-duration cases, the original scalar
residual histories, gain/replay probes, and budget/projection gates.
Single-stream and SSE postfilter warm allocation guards report zero allocations.
The multistream guard passes its existing allowance of eight allocations per
call; that is not a zero-allocation result. This early subset is not a complete
codec mismatch inventory. ARM64 parity, other native test families, reference
pairing, and multistream allocations remain separate acceptance work.

The completed native `036c4d51` encoder differential family checks all
1,788 configurations × eight frames with zero packet, range, framing, or
cadence differences. The same capture completes 2,268 surround encode
configurations with 287 failing leaves and 432 projection encode
configurations with 159 failing leaves. The projection harness also logs
mode/float residuals under legacy allowances, so passing leaves in that
family are not all exactness proofs. The current multistream budget path uses C's per-stream CVBR bound and actual
self-delimited repacketizer lengths when accounting for available packet space.
It rejects insufficient capacity before advancing analysis state. The focused
live-C gate covers caller capacities, CVBR bursts, CBR framing through 120 ms,
and surround/projection layouts, comparing every packet byte and final range.
All 12 focused budget cases match all 36 packet/range records in each local
mode. Projection analysis consumes original caller PCM while coding consumes
matrix-mixed PCM, matching the C callback contract; the dedicated CVBR/CBR
regression matches another 12 records in each mode. Broader projection
configurations retain exact failures. The framing helper allocates zero times
after warmup, and the too-small-buffer state test passes in all three modes. All 20 selected CELT/encoder
bitrate and VBR test families pass in each mode. Completed families within the
interrupted full native run are usable evidence, not a complete codec total.

Full parity remains incomplete. The complete native AMD64 SIMD sweep at
`e6f2b332` contains 1,819 unique failing leaf cases across 24 test families:
1,732 multistream/projection, 20 stateful encode, and 67 other checks. The
`b0c9c56a` comparison has 2,057 failures; 238 cases pass at `e6f2b332`.
Those counts describe that revision and include fixture evidence failures;
they are neither current totals nor counts of independent bugs.

The ARM64 tone-analysis checkpoint matches the selected C translation unit for
all 26 LPC cases and 12 tone-detection assertions in ordinary, SIMD, and
`nosimd` builds. Independent root checks also pass the empty correlation,
retry, allocation, and three paired hybrid-combine oracles. The 16-case public
short-frame sweep still has two one-packet differences per lane, with matching
final ranges. Scalar chirp has one differing packet out of 201. These results
cover the tone helper; they do not establish complete encoder parity.

Native `f3176763` full scalar CI executes all 2,988 stateful configurations:
48 fail, all auto-mode mono 40/60 ms at complexity 5, across 24/32/48/64 kbit/s,
three VBR settings, and both DTX settings. It records 166 payload-byte and 60
final-range diagnostics, with zero TOC flips or cadence errors. The projection
sweep executes 432 cases and has one fatal 9-channel 10 ms / 64 kbit/s CBR
configuration: stream 4 returns an invalid mode/bandwidth/frame-size error on
frame 2. Its legacy output also records 95 mode and 544 float residuals;
these tolerated differences are not counted as exact parity. The native
build-config CI job fails on these encoder tests; the exact decoder sweep
remains 3,640/3,640 in both matched lanes.

The complete ARM stateful encoder sweep at `371537d5` passes all 2,988
configurations in ordinary, SIMD and `nosimd`, with zero failures or skips.
Each configuration checks 40 frames. Stereo fade preserves the selected C
rounding boundaries before updating the channels. Nine direct C stage cases
and a 40-frame packet/range witness pass in all three modes, with zero warm
stage allocations. The same stage gate fails eight of nine cases against
the pre-fix source. Native AMD64 confirmation remains pending.

The low-delay oracle requires automatic bandwidth and channel selection on
both sides. Its permanent gate requires exact packet bytes and final ranges
on every architecture. At `fb8cd904`, restricted low-delay applications run the channel and bandwidth decisions
while selecting CELT at the application-mode step. Independent current-source
runs pass all 120 low-delay cells, 50 packet/range frames each, in ordinary,
SIMD and `nosimd`; zero failures, skips, or residual allowances. VBR/CVBR
checks that share the C helper pass in each lane. Four explicit-control and
reset cases add 48 complete packet/range records per lane. Native AMD64
confirmation remains pending.

The strict ARM64 composite encoder audit at `fb8cd904` checks every packet
byte and final range, including cases whose legacy tests allow differences:

| Lane | Surround exact | Projection exact | Fixed-layout projection exact |
|---|---:|---:|---:|
| Ordinary scalar | 2,230 / 2,268 | 320 / 432 | 16 / 16 |
| SIMD | 2,226 / 2,268 | 319 / 432 | 16 / 16 |
| `nosimd` scalar | 2,230 / 2,268 | 320 / 432 | 16 / 16 |

These are separate test families, not a count of independent bugs. The first
short-frame surround failure also reproduces in the elementary encoder with
identical C/Go input and delay history. Fixes remain under investigation.

Native early evidence at `a3af0aee`, from
[run 36270404758](https://github.com/thesyncim/gopus/actions/runs/36270404758),
passes all 3,640 strict multistream/projection decode cases and all 48 mono
stateful configurations in both SIMD and `nosimd`. CELT pitch/rotation,
deemphasis/PLC, and multistream budget/init/reset gates also pass with no
skips. The SILK NaN edge gate compares 24 additional raw-bit leaves directly
with the selected C kernel: all pass, including the previously reported NaN
payload mismatch. This brings the two SILK raw-bit gate families to 48 leaves.
This revision predates the stereo-fade, int16 rounding and low-delay fixes;
their native confirmation remains pending.

Native `1ad86da2` confirmation, from
[run 36271193110](https://github.com/thesyncim/gopus/actions/runs/36271193110)
on EPYC 7763 / Go 1.27.1 / GCC 13.3, passes both SIMD and `nosimd` lanes:
all 1,440 public exact-decode configurations (4,320 packets and 12,960 output
format decodes per lane), all 120 low-delay configurations, all 48 mono
stateful configurations, and all 3,640 multistream/projection decode cases.
The direct stereo-fade gate and public transition witness also pass, alongside
native-rate, DTX, PLC, allocation and budget guards. Every early phase exits
zero, with no skipped/failing case in these JSON captures. The 2,988-case full
stateful encoder matrix has local ARM64 evidence; this native early capture
executes the 48-case mono subset and does not establish the full native total.

The stereo-width estimator at `58831869` matches the selected C return value
and all five state fields for 11 sequences of eight frames in ordinary, SIMD
and `nosimd`: correlated/opposite/unequal channels, silence after activity,
seeded smoothing, 12 kHz remainders and 40/60/120 ms frames. XY contraction,
width rounding and the long-frame smoothing rate follow C. Each warm path
allocates zero. Native confirmation of this estimator gate is pending.

At `c740f561`, VoIP high-pass feedback follows the selected C product
rounding and contraction order. All 40 direct filter cases (four successive
frames, five API sample rates, mono/stereo, both input paths and seeded state)
match every output and state bit in all three ARM64 modes, with zero warm
allocations. These tests supply identical integer cutoff frequencies to the
C and Go coefficient/filter stages. The public DTX gate passes all 108
configurations × 26 frames per ARM mode, with exact packet bytes, ranges and
cadence. The pre-fix SIMD source fails 107 of those 108 configurations and
all 40 filter cases. Native confirmation of the new filter gate is pending.

The coded-channel silence scan at `d75d9948` matches C's raw input prefix and
saved overlap state while preemphasis consumes physical channels. Sixteen
selected-C stage cases and four five-frame surround/projection byte/range
witnesses pass in all three ARM modes, with zero warm stage allocations.
The strict composite audit after this correction has 22/2,268 surround and
40/432 projection configurations differing in scalar/`nosimd`, and 26/2,268
and 41/432 in SIMD. All 216 float projection configurations are exact per
lane; the remaining projection differences occur in the int16 family.
The full multistream package passes ordinary and `purego`; the CELT package
retains missing frozen opusdec input-hash failures under active investigation.

The transient history checkpoint `745dcbc6` uses the mode stride for saved
right-channel energy while current frame energies use the active band stride.
All 72 selected-C transient decisions, input/history immutability and warmed
zero-allocation checks pass in each ARM64 mode. The narrowband quad witness
matches all five packet bytes and final ranges. The low-space checkpoint
`9da0f1d9` clears the single-stream final range at native encode entry, including
TOC-only returns. A five-frame 7.1 witness requires the preceding active child
range to be nonzero and subsequent low-space ranges to be zero; all three ARM
modes pass.

PVQ projection at `91cafc25` follows the selected ARM SIMD C object's 16- and
4-term rounded-product blocks with ascending scalar addition, then fused tail
terms. The captured six-value, 72-pulse live band matches C's pulse vector and
squared norm. All 24 boundary cases, the 4,680-vector C matrix and the high-pulse
near-tie case pass each ARM mode without residual allowances or skips; warm
projection allocation checks are zero. All four corresponding complete packet
witnesses pass each mode. The strict differential surround sweep passes
2,268/2,268 cases in all three modes. A separate fixed surround matrix still
has one packet difference in every mode. Native AMD64 confirmation is pending.

Frozen opusdec honesty at `81a25b94` replays 11 verified historical Ogg inputs,
covering all 11 scenario names and 11 of 18 retained frozen hashes. Every input
matches the existing expected hash. The archived PCM JSON is byte-identical
to the existing Linux fixture; no expected PCM or tolerance changes. The
recorded decoder is Ubuntu libopus 1.4, so this is historical compatibility
coverage, separate from the matched live libopus 1.6.1 gates. All 11 live
opusdec comparisons and the integrity check pass locally. The complete CELT
ordinary and `purego` suites each pass 3,039 test nodes, with three existing
capability/producer skips and zero failures. Native confirmation is pending.

Short multistream encoding at `32c923bd` uses original int16 analysis input,
ordered projection mixing and per-call short-input LSB depth. The analyzer's
ARM64 rounded four-bin epilogue matches all 202 live-C cases. Caller-owned
MDCT/TF scratch gives zero warm allocations for the tested six-channel encode
and eight-channel low-space path. All 228 focused test nodes pass each ARM
mode, including 24-frame short/float transitions, projection, ownership and
capacity-rejection state checks. The strict broad composite sweep passes
the full 2,268-case differential matrix and all 112 fixed surround cases in
each local lane. The low-space high-pass state at `8d39f40d` matches C before
and after a one-byte child packet, with a strict six-frame 7.1 CBR witness.
All 432 five-frame projection cases are exact. Longer and broader composite coverage remains open.

SILK PLC at `7a48ad92` clears all 16 cached LPC coefficients when the channel
is in its first frame after reset, as `silk_PLC_conceal` requires. Captured
native AMD64 40/60 ms packet histories match every prefix and FEC output bit,
length and range against the live paired C decoder in all three ARM modes.
Removing only the reset clear fails both histories. The broader FEC/PLC/side
channel sweep passes 72 test nodes per lane, without failures or skips;
8/12/16 kHz reset concealment checks allocate zero after warmup. Native AMD64
SIMD and nosimd each pass all 13 FEC test nodes at `04e9628c` and `3f846cc4`,
including both captured histories.

### Optional-feature exactness audit

Full optional-feature parity remains incomplete. Every feature oracle selects
and validates its feature configuration and scalar/SIMD instruction lane.

QEXT uses build-wide float theta gains (`536ce5b5`), fixed entropy storage,
main-residual backup, multiframe CBR padding (`5789af08`), and C's per-subframe
extension reservation (`2473de2e`). The matched 60-configuration/180-frame
encoder matrix at `b91e2937` is **60/60 configurations, 180/180 frames byte-and-range exact**
in ordinary, SIMD, nosimd and purego on ARM64. The QEXT target-byte conversion
truncates the integer bit budget exactly as `celt_encoder.c` does.
The strict witnesses cover 40/60 ms CBR, a 477-byte output cap, and VBR caps
on either side of the 254-byte reservation boundary. Received-silence synthesis
at `00c16855` clears main and extension spectra while preserving coder and
energy history. Twelve coded/output-channel and extension-size cases plus two
three-frame good/silence/recovery histories match every C PCM bit and range;
removing only the extension clear fails all fourteen leaves. Focused QEXT
suites pass 160 events including package completions in ordinary, SIMD,
nosimd and purego, without failures/skips. Reverting only target-byte
truncation fails the seven captured matrix configurations. Active encode allocation witnesses remain zero.

Custom mode-owned FFT/MDCT tables match actual C twiddles, trig, windows and
forward/inverse transforms, including shared base twiddles for short blocks.
The two-tap preemphasis recurrence at `49634d64` matches C's rounded second
product and first-product/subtraction contraction. Four three-frame input
filter cases and 24 output-filter cells compare exact output/state and warm
zero allocations; reverting their contraction fails all respective leaves.
Custom suites require exact packet bytes, final ranges, and PCM bits. Wider
layouts at `736a2d27` use the mode's band count in history, energy prediction,
analysis, and scratch. Custom comb filters use the mode-owned window even when
its length equals a standard window; custom 96 kHz pitch limits retain the C
custom-mode scale. Concealment uses active geometry and C's `1 << LM` duration
units. The 154-history matrix covers 2,046 steps (946 received packets and 1,100
losses), including reset replay, periodic/noise concealment, and recovery.
Ordinary, SIMD, nosimd, and purego pass all exact outputs and ranges, and all
22 mode/channel pairs are allocation-free after warmup, including loss/recovery.
The seven existing custom-suite capability/duplicate-family skips are separate
from these new cases. Native AMD64 SIMD/nosimd at `8bc2ed7c` also pass the
full custom-mode suite without failures.

DRED at `a03103c9` pairs the neural kernels as well as the codec with the
selected C build. Ordinary/nosimd use scalar activations and RDOVAE kernels;
ARM64 SIMD uses NEON. The strict duration sweep passes all 80 configurations in
each ARM lane: 78 exact emitted payload/index/offset cases and two exact
640-frame no-emission cases. Packet/kernel checks pass 40/40 and touched
internal suites pass 309/309 per lane; ordinary purego also passes those
internal checks and all 105 focused public DRED nodes. Native AMD64 SIMD at `0330c9ca` has four failing direct neural
oracle leaves. Local LPCNet stage fixes at `9faa9bc1` pass ordinary/SIMD/nosimd;
the full-sequence gate at `e53ca99c` matches all 220 mono time slots: 100
received and 120 concealed frames, including trailing PLC. PCM bits, frame
kinds, return counts, and final ranges match selected C in ordinary, SIMD, and
nosimd. Thirty mono/stereo rate/duration cases match exact nil/recovery PCM
with zero warm allocations. Both enabled sustained-loss quality gates pass;
the original 119-loss comparison has correlation/RMS ratio 1.0. Native AMD64
confirmation remains open.

Fixed-point references at `67731222` pair feature and instruction variants.
The 162 integer CELT kernel configurations (five captured input payloads each)
pass exact payload lengths/bytes in three local modes. At `20901221`, all
9,534 strict decode configurations / 42,906 stateful steps match float32,
int16, and int24 outputs in three local modes. Hybrid→CELT transition/gain
histories and active concealment/recovery also pass exactness and zero warm
allocation gates. The full SILK packet API at `0dcd8581` matches C bytes,
ranges, and bit counts for 108 cases plus 12 complexity/reset histories.
Fixed FEC at `69792f0e` passes 577 focused events per ARM64 mode across
ordinary, SIMD, nosimd, and purego, including gain/reset, long recovery, malformed
framing, and zero warm allocations. The outer encode checkpoint passes 2,428
focused test events in each ordinary, SIMD, nosimd, and purego build, with no
failures or skips. Its 3,240 Q8 differential configurations match selected C
packet bytes and final ranges in ordinary, SIMD, and nosimd builds. Six-frame
histories apply at 48 kHz; lower-rate probes use one frame. The public fixed
short-input tests cover Audio and LowDelay, mono and stereo, and int16, float32,
and int24 input paths. The long CELT matrix covers 108 packets across 40/60 ms,
mono/stereo, three applications, and three rate modes. VoIP mixed-input histories
cover four mono/stereo and complexity 0/10 cases across nine int16/float32/int24
calls each, including reset replay. A 120 ms `EncodeInt16`
expert-duration case also matches three 20 ms C packets and ranges. CBR raw-tail
storage, stereo fade, DC-reject Q8 state, and nonpositive-allocation budgets
have direct selected-C checks; warm allocation checks remain zero. The selected
fixed C `hp_cutoff` path matches output Q8 samples and all four `hp_mem` words
for 20 histories (five sample rates, mono/stereo, seeded/unseeded state), each
with four sequential cutoffs and fractional/full-scale boundaries. Public
forced-CELT VoIP `EncodeInt16` packets at 48 kHz match selected fixed libopus
bytes and ranges for six frames in mono and stereo. The mixed-input VoIP
histories match packet bytes/ranges across reset and all three public input
formats in ordinary, SIMD, and nosimd builds. Reset replay from an elevated
cutoff matches a fresh encoder; low-space state and warm allocations remain
exact. This covers forced CELT only; VoIP SILK and Hybrid paths remain open. The
96 kHz QEXT case is also outside this fixed outer-encode matrix.

The broad fixed-tag package sweep is not a bit-parity result: some of its test
helpers pair fixed-point Go with the float libopus fixture. Its failures remain
unresolved diagnostics and are not counted as passing or waived parity cases.
Native AMD64 at `08a11a0f` passes 5,718 fixed-encode events in both SIMD and
nosimd without failures or skips, including the forced-CELT VoIP short,
mixed-input, long-duration, reset, and low-space selectors. VoIP SILK and
Hybrid high-pass parity and 96 kHz remain outside this claim. The complete
six-row performance table uses the `54654e25` early capture described below.

Remaining strict investigations include:

- Longer surround/projection state sequences and native AMD64 confirmation
  of each new extension fix.
  Counts above belong to their named matrices and revisions, not a global
  count of independent defects.
- Broader QEXT, DRED/OSCE, custom-mode and public fixed-point feature parity,
  with matching neural and codec dispatch on each side.
- Broader multistream encode allocation coverage and optional-feature
  allocation coverage. Owned-output convenience APIs allocate returned slices.
- Legacy tests that accept numerical/packet differences are not evidence of
  exact parity. Strict replacement gates retain every selected case.

## Measurement method

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

The current AMD64 symbol rows use the completed direct-kernel phases in full
artifact `10931588043` for [run 36319060882](https://github.com/thesyncim/gopus/actions/runs/36319060882)
at `17e48afb`: AMD EPYC 9V74, Go 1.27.1, GCC 13.3.0, `GOAMD64=v1`, and
runtime AVX2/FMA dispatch. Each mode has five 300 ms samples; all report zero
allocations, and the baseline assembly, candidate scalar Go, and candidate Go
SIMD phases exit successfully. The direct xcorr rows retain separate coarse,
half, and tiny fixtures; the SIMD result varies by shape, so they do not imply a
blanket xcorr speedup. The PVQ pulse and best-ID helper rows identify separate
paths from the finite-input production full search. Sequential kernel phases
retain host load and frequency as measurement risks; ratios compare only modes
within this run.

## End-to-end codec throughput

The latest native six-workload early capture is documented in the [54654e25
E2E section](#latest-early-end-to-end-capture-at-54654e25-amd-epyc-7763): it
uses four interleaved 500 ms samples per workload and build, with zero
allocations in all 72 samples. The capture is separate from the full artifact's
three-sample E2E phase and does not establish a complete CI pass.

The six public encode/decode benchmarks run on one Apple M4 Max with Go
1.27.0, `-cpu=1`, three interleaved 300 ms samples per mode, and preallocated
caller buffers. Old is the pre-port `ef5a9fe74` default assembly build;
Go SIMD and `nosimd` use `1b18fbf0` with `GOEXPERIMENT=simd`. The table gives
median ns/op; lower is faster. Every sample reports 0 B/op and 0 allocs/op.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 6,972 | 6,999 | 10,344 |
| Hybrid decode | 12,694 | 12,278 | 13,623 |
| SILK decode | 10,369 | 9,207 | 10,378 |
| Caller-buffer encode | 38,918 | 34,517 | 52,475 |
| VoIP encode | 46,067 | 38,627 | 61,210 |
| Low-delay encode | 40,303 | 34,744 | 54,005 |

These samples have substantial timing spread (for example, old caller-buffer
encode spans 35,914–43,802 ns/op and Go SIMD spans 33,682–40,909 ns/op).
They establish current allocation counts and measured ranges; small throughput
changes are inconclusive. The earlier quieter `cd62a758` run measures a 2.8%
SIMD improvement by equal-fixture geometric mean. The native AMD64 A/B
workflow records the same three-mode end-to-end benchmarks on one x86 runner.
These tables compare Go implementations with old Go assembly, independently
of the matched libopus scalar/SIMD correctness comparisons.

### ARM64 exact deemphasis: paired decode cost

Five interleaved 300 ms samples on M4, Go 1.27.0, SIMD, `-cpu=1`.
Both revisions use the same benchmark fixture. All samples are 0 B/op and
0 allocs/op. These are Go-to-Go timings, independent of the assembly tables.

| Caller-buffer decode | `e6f2b332` ns/op | Exact recurrence ns/op | Time change |
|---|---:|---:|---:|
| CELT mono | 6,841 | 7,915 | +15.7% |
| Hybrid mono | 11,912 | 13,210 | +10.9% |
| CELT stereo | 9,166 | 10,197 | +11.2% |

The exact path preserves C rounding and executes deemphasis on silence.
The paired live-wrapper measurements are in row 3. No native AMD64 performance
change is inferred from this ARM64 comparison.

### ARM64 contiguous deemphasis accumulation

Five interleaved 300 ms samples on Apple M4 Max / Go 1.27.0 / `-cpu=1`
compare two binaries built from the published `5e14f0d8` sources with only the
contiguous accumulation loop varied by source overlay. The loop preserves the
per-sample arithmetic and recurrence order. Every sample reports 0 B/op and
0 allocs/op.

| Fixture | Baseline ns/op (range) | Contiguous loop ns/op (range) | Median change |
|---|---:|---:|---:|
| Direct mono N=480 recurrence, ordinary | 1,150 (1,150–1,158) | 882.4 (877.5–891.6) | 23.3% less time |
| Direct mono N=480 recurrence, SIMD build | 1,150 (1,150–1,165) | 884.9 (879.8–887.0) | 23.1% less time |
| Public Hybrid decode, ordinary | 17,478 (17,417–17,724) | 17,205 (16,952–17,560) | 1.6% less time |
| Public Hybrid decode, SIMD | 14,066 (13,908–14,097) | 13,539 (13,439–13,709) | 3.7% less time |

The direct kernel improvement is clear in both modes. The ordinary public
ranges overlap; the SIMD public ranges do not. This is local ARM64 evidence,
not an AMD64 result. `TestDeemphasisMatchesLibopus`,
`TestDeemphasisHybridLowbandEdgesMatchLibopus`, and the warmed zero-allocation
test pass in ordinary, SIMD, and `nosimd` modes.

### Native AMD64 candidate

Run 36031595048 uses one AMD EPYC 9V74 runner, Go 1.27.1, `-cpu=1`, and
three 300 ms samples per mode. All samples report zero bytes and allocations.
The candidate is `cd62a758`; its unresolved correctness failures are listed
above. These measurements do not establish packet correctness.

| Fixture | Old assembly | Go SIMD candidate | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,203 | 20,227 | 22,560 |
| Hybrid decode | 29,753 | 29,829 | 31,696 |
| SILK decode | 23,528 | 23,630 | 23,987 |
| Caller-buffer encode | 92,583 | 123,113 | 118,525 |
| VoIP encode | 98,724 | 129,079 | 124,775 |
| Low-delay encode | 91,661 | 122,326 | 118,035 |

Go SIMD decode is within 0.5% of assembly on these fixtures; encode takes
30.7–33.5% more time. Ratios compare only modes on this same runner.

### Native AMD64: Intel Xeon 6973P-C

Run [36056914422](https://github.com/thesyncim/gopus/actions/runs/36056914422)
at `54300227` uses the same benchmark settings as the EPYC table above.
Every sample reports zero bytes and allocations.

| Fixture | Old assembly | Go SIMD candidate | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 17,207 | 14,095 | 15,591 |
| Hybrid decode | 21,354 | 18,969 | 20,069 |
| SILK decode | 15,332 | 14,729 | 14,929 |
| Caller-buffer encode | 69,964 | 65,658 | 82,568 |
| VoIP encode | 73,721 | 69,483 | 87,243 |
| Low-delay encode | 69,369 | 65,033 | 82,216 |

Go SIMD encode takes 5.7–6.3% less time and decode 3.9–18.1% less time
within this run. CPU and revision differ from the EPYC run; compare modes
within each run rather than inferring changes across these hosts.

### Native AMD64: AMD EPYC 9V45

Run [36257213952](https://github.com/thesyncim/gopus/actions/runs/36257213952)
at `2ccd85af`, Go 1.27.1, three 300 ms samples per mode, `-cpu=1`.
The assembly baseline is `8ac93c85`. All samples report zero allocations;
timings use sequential phases and the table reports median ns/op. These
benchmark phases complete successfully; the separate parity capture is
partial after cancellation.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 11,309 | 9,371 | 12,882 |
| Hybrid decode | 15,749 | 14,818 | 17,960 |
| SILK decode | 12,930 | 12,525 | 13,370 |
| Caller-buffer encode | 52,431 | 47,600 | 66,622 |
| VoIP encode | 56,504 | 51,376 | 71,373 |
| Low-delay encode | 51,498 | 46,979 | 66,582 |

Within this run, Go SIMD takes 8.8–9.2% less time for encode and 3.1–17.1%
less for decode. Other host/revision tables are separate measurements;
no cross-run ratio establishes an improvement. These timings include the
exact-deemphasis path and do not establish complete parity.

### Native AMD64: AMD EPYC 7763, exact xcorr and postfilter

Run [36262586916](https://github.com/thesyncim/gopus/actions/runs/36262586916)
at `f3176763`, Go 1.27.1, GCC 13.3, `GOAMD64=v1`. The assembly baseline is
`8ac93c85`. Every kernel benchmark phase completes with five one-second samples;
the six public benchmarks complete with three 300 ms samples and `-cpu=1`.
Every sample reports 0 B/op and 0 allocs/op. The separate full parity capture
is incomplete after cancellation; benchmark phase exits and sample counts are
independently checked.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,196 | 16,937 | 22,747 |
| Hybrid decode | 28,350 | 26,327 | 31,703 |
| SILK decode | 22,607 | 21,759 | 22,871 |
| Caller-buffer encode | 93,055 | 89,281 | 117,611 |
| VoIP encode | 99,740 | 95,622 | 125,419 |
| Low-delay encode | 92,591 | 88,256 | 117,007 |

Go SIMD takes 3.8–16.1% less decode time and 4.1–4.7% less encode time than
assembly on these fixtures. The direct matrix still exposes slower kernels:
CELT xcorr takes 2.0–4.2 times assembly time, SILK xcorr 4.0 times, and the
SILK inner product 2.0 times. Selected AVX2/FMA dispatch and exact xcorr bits
are verified in the same capture. These costs remain optimization work; the
public benchmark gain does not imply every kernel is faster.

### Native AMD64: AMD EPYC 9V74

[Run 36274171857](https://github.com/thesyncim/gopus/actions/runs/36274171857)
compares assembly `8ac93c85` with `9056116d` on AMD EPYC 9V74, Go 1.27.1,
GCC 13.3, `GOAMD64=v1`, `-cpu=1`. All three end-to-end benchmark phases
complete; medians use three 300 ms samples per fixture and mode. Every sample
reports 0 B/op and 0 allocs/op. The separate full-parity capture is incomplete
and contains failures, including native stereo multiframe FEC.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 15,761 | 13,097 | 17,446 |
| Hybrid decode | 23,129 | 21,779 | 25,828 |
| SILK decode | 18,349 | 17,751 | 18,546 |
| Caller-buffer encode | 72,211 | 64,385 | 86,752 |
| VoIP encode | 77,092 | 68,717 | 91,332 |
| Low-delay encode | 71,413 | 63,608 | 86,357 |

Within this run SIMD takes 3.3–16.9% less decode time and 10.8–10.9% less
encode time than assembly. This capture includes all 11 comparable AMD64
symbols: five complete samples each for assembly, ordinary Go and Go SIMD,
with zero kernel allocations. CELT coarse/half correlations take 24.9%/31.9%
more time than assembly, SILK pitch correlation 26.7% more, and tone LPC
83.2% more. Tiny CELT correlations take 24.5–59.5% less time; the SILK inner
product is within 1%. The unused direct PVQ pulse helper remains slower;
the production full search takes 16.8% less time. Different hosts do not
establish revision-to-revision gains.

### Interleaved AMD64 encode and profiles

Run [36248080529](https://github.com/thesyncim/gopus/actions/runs/36248080529)
at `e6f2b332` compares assembly `8ac93c85` with Go SIMD on AMD EPYC 7763,
Go 1.27.1, using four interleaved 500 ms samples, `-cpu=1`, matching PGO
settings and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 91,926 | 91,812–92,040 | 0 |
| Go SIMD | 87,662.5 | 87,406–88,504 | 0 |

Go SIMD takes 4.6% less time in this pair. It has no `nosimd` measurement.
Separate 3-second profiles at `b0c9c56a` put pitch search at 10.67% cumulative samples in
Go SIMD versus 5.51% in assembly; the one-pass xcorr helper has 4.67% flat
samples versus 2.20% for assembly xcorr. The SSE-order dual prefilter takes
4.67% cumulative samples in Go SIMD versus 8.59% in assembly. These shares
identify live paths; they do not establish each kernel's contribution to the
end-to-end timing difference.

### EPYC 9V45 interleaved encode

[Run 36257213952](https://github.com/thesyncim/gopus/actions/runs/36257213952)
compares assembly `8ac93c85` with `2ccd85af` on AMD EPYC 9V45,
Go 1.27.1, using four interleaved 500 ms samples, `-cpu=1`, matching PGO
settings, and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 52,067 | 51,593–52,426 | 0 |
| Go SIMD | 47,190.5 | 47,132–47,494 | 0 |

Go SIMD takes 9.4% less time in this same-run pair. The `036c4d51` pair on
EPYC 7763 measures 91,870 → 88,977 ns/op (3.1% less time); the `301be749`
pair on Xeon Platinum 8573C measures 94,975.5 → 81,601.5 ns/op (14.1% less).
Different hosts cannot establish a revision-to-revision gain or loss.
The completed `2ccd85af` benchmark phases supply the three-mode decode table
and all 11 measurable AMD64 symbol rows at that revision.

### Xeon 8370C interleaved encode

[Run 36260351550](https://github.com/thesyncim/gopus/actions/runs/36260351550)
compares assembly `8ac93c85` with `f6952200` on Intel Xeon Platinum 8370C,
Go 1.27.1, using four interleaved 500 ms samples, `-cpu=1`, matching PGO
settings, and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 109,926 | 109,358–110,772 | 0 |
| Go SIMD | 86,576.5 | 86,461–86,748 | 0 |

Go SIMD takes 21.2% less time in this same-run pair. This early capture has
no `nosimd` timing. The full capture completes only the baseline benchmark
phases before cancellation, so it supplies no new three-mode ratio. The
53-row matrix retains the completed measurements and their recorded revisions.


### Native AMD64 interleaved encode at 9056116d

[Run 36274171857](https://github.com/thesyncim/gopus/actions/runs/36274171857)
compares assembly `8ac93c85` with `9056116d` on AMD EPYC 9V74, Go 1.27.1,
using four interleaved 500 ms samples, `-cpu=1`, matching PGO settings,
and preallocated caller buffers.

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 72,028 | 71,950–72,144 | 0 |
| Go SIMD | 64,325 | 64,272–64,494 | 0 |

Go SIMD takes 10.7% less time in this same-run pair. The EPYC three-mode table records this revision; the current AMD64 symbol
rows use the separately qualified Xeon capture below.
The earlier EPYC 7763 pair at `1ad86da2` measures 91,445.5 → 88,448.5 ns/op
(3.3% less time); its different host does not establish a revision comparison.

### Native AMD64 at 8bc2ed7c

[Run 36284981747](https://github.com/thesyncim/gopus/actions/runs/36284981747)
compares assembly `8ac93c85` with `8bc2ed7c` on Intel Xeon Platinum 8573C,
Go 1.27.1, GCC 13.3, GOAMD64=v1, runtime AVX2/FMA. Four interleaved
500 ms caller-buffer encode samples give:

| Caller-buffer encode | Median ns/op | Sample range | Allocs/op |
|---|---:|---:|---:|
| Old assembly | 95,804.5 | 95,686–95,996 | 0 |
| Go SIMD | 82,857.5 | 82,746–82,970 | 0 |

Go SIMD takes 13.5% less time within this pair. The three-mode capture uses
three 300 ms samples per fixture, `-cpu=1`, and matching PGO settings. Values
are median (min–max) ns/op; every sample reports 0 B/op and 0 allocs/op.
All benchmark phases complete successfully; the workflow is canceled before
full parity completes. This capture has five 300 ms samples per mode for the eleven comparable
AMD64 rows; the inventory uses the newer source-qualified EPYC capture.

| Fixture | Old assembly | Go SIMD | nosimd | SIMD vs assembly |
|---|---:|---:|---:|---:|
| CELT decode | 23,601 (23,596–23,631) | 15,700 (15,694–15,722) | 21,574 (21,459–22,537) | 33.5% less time |
| Hybrid decode | 30,077 (30,029–30,363) | 26,479 (26,437–26,522) | 32,223 (32,153–32,228) | 12.0% less time |
| SILK decode | 21,086 (21,080–21,128) | 19,157 (19,146–19,182) | 20,462 (20,407–20,480) | 9.1% less time |
| Caller-buffer encode | 95,849 (95,687–98,093) | 82,512 (82,387–82,577) | 97,182 (97,140–97,308) | 13.9% less time |
| VoIP encode | 100,862 (100,850–101,154) | 87,452 (87,450–87,635) | 102,327 (102,240–102,470) | 13.3% less time |
| Low-delay encode | 95,268 (95,258–95,791) | 82,334 (82,009–83,463) | 97,019 (97,010–97,142) | 13.6% less time |

Within this run SIMD takes 9.1–33.5% less decode time and 13.3–13.9% less
encode time than assembly. These measurements precede the neural and fixed
decoder checkpoints; they do not establish current-head performance.
Different captures do not establish revision-to-revision gains.

### Native AMD64 at c4f4b3cc

[Run 36286208931](https://github.com/thesyncim/gopus/actions/runs/36286208931)
compares assembly `8ac93c85` with `c4f4b3cc` on AMD EPYC 7763, Go 1.27.1,
GCC 13.3, GOAMD64=v1, runtime AVX2/FMA. Four interleaved 500 ms caller-buffer
encode samples measure 92,196 (91,751–92,799) → 91,080.5 (90,617–91,803) ns/op,
1.2% less time with zero allocations.

The six end-to-end rows use three 300 ms samples per mode, `-cpu=1`, and
matching PGO settings. Values are median (min–max) ns/op; every sample reports
0 B/op and 0 allocs/op. All benchmark phases complete successfully, while the
workflow is canceled without full parity completion. This capture supplies five 300 ms samples per mode for the eleven AMD64
symbol rows at its recorded revision.

| Fixture | Old assembly | Go SIMD | nosimd | SIMD vs assembly |
|---|---:|---:|---:|---:|
| CELT decode | 20,479 (20,454–21,772) | 16,509 (16,448–16,547) | 22,263 (22,249–22,275) | 19.4% less time |
| Hybrid decode | 28,347 (28,340–28,408) | 29,413 (29,395–29,454) | 35,340 (35,039–36,415) | 3.8% more time |
| SILK decode | 22,418 (22,383–22,710) | 21,620 (21,619–21,737) | 22,834 (22,809–22,890) | 3.6% less time |
| Caller-buffer encode | 92,875 (92,319–93,065) | 91,099 (91,080–91,439) | 112,402 (112,210–112,439) | 1.9% less time |
| VoIP encode | 98,884 (98,832–98,979) | 97,384 (97,219–97,652) | 118,717 (118,690–118,753) | 1.5% less time |
| Low-delay encode | 91,777 (91,580–91,803) | 90,086 (89,773–90,565) | 111,563 (111,445–111,781) | 1.8% less time |

Hybrid decode takes 3.8% more time than assembly; CELT and SILK decode take
19.4% and 3.6% less time. Encode takes 1.5–1.9% less time in these sequential
phases. These are same-run comparisons before the fixed decoder and
multistream checkpoints; earlier Xeon results do not establish a revision gain.

### Native AMD64 at 1e2dbe77

[Run 36296680326](https://github.com/thesyncim/gopus/actions/runs/36296680326)
compares assembly `8ac93c85` with `1e2dbe77` on AMD EPYC 9V45, Go 1.27.1,
GCC 13.3, GOAMD64=v1, runtime AVX2/FMA. Four interleaved 500 ms caller-buffer
encode samples measure 51,667.5 (51,452–51,947) → 44,372 (44,279–45,005) ns/op,
14.1% less time. All samples report 0 B/op and 0 allocs/op.

The six end-to-end rows use three 300 ms samples per mode, `-cpu=1`, and
matching PGO settings. Values are median (min–max) ns/op; every sample reports
0 B/op and 0 allocs/op. All benchmark phases complete successfully; optional
extension parity failures keep CI open. The current eleven AMD64 symbol rows
use five 300 ms samples per mode from this capture.

| Fixture | Old assembly | Go SIMD | nosimd | SIMD vs assembly |
|---|---:|---:|---:|---:|
| CELT decode | 11,627 (11,244–12,264) | 9,357 (9,214–9,422) | 12,786 (12,438–12,832) | 19.5% less time |
| Hybrid decode | 15,477 (15,469–15,940) | 16,417 (16,413–16,429) | 19,257 (19,194–19,530) | 6.1% more time |
| SILK decode | 12,837 (12,787–12,914) | 12,469 (12,391–12,489) | 13,201 (13,115–13,526) | 2.9% less time |
| Caller-buffer encode | 51,978 (51,130–52,287) | 45,654 (44,561–49,083) | 57,936 (57,471–60,331) | 12.2% less time |
| VoIP encode | 55,061 (54,813–56,660) | 49,293 (48,825–50,529) | 60,923 (60,679–61,888) | 10.5% less time |
| Low-delay encode | 52,326 (51,247–52,576) | 44,719 (44,220–46,370) | 58,566 (58,564–64,370) | 14.5% less time |


Hybrid decode takes 6.1% more time than assembly; CELT and SILK decode take
19.5% and 2.9% less time. Encode takes 10.5–14.5% less time in the sequential
phases. The EPYC 7763 capture uses a different CPU and establishes no revision
comparison.

### Completed native end-to-end benchmark phases at 08a11a0f

The completed full artifact `10928708319` for [run
36309916340](https://github.com/thesyncim/gopus/actions/runs/36309916340)
compares assembly `8ac93c85` with candidate SIMD/nosimd `08a11a0f` on Intel
Xeon 8573C, Go 1.27.1, GCC 13.3, GOAMD64=v1 with runtime AVX2/FMA. Each row is
the median and range from three 300 ms samples at `-cpu=1`; every sample
reports 0 B/op and 0 allocs/op. It is separate from the full six-row table and
11 comparable AMD64 symbol rows at `1e2dbe77`; the 53-row inventory retains
each symbol's recorded revision.

| Fixture | Old assembly | Go SIMD | `nosimd` | SIMD vs assembly |
|---|---:|---:|---:|---:|
| CELT decode | 23,595 (23,557–23,622) | 14,656 (14,651–14,668) | 21,455 (21,435–21,468) | 37.89% less time |
| Hybrid decode | 30,034 (30,034–30,159) | 26,479 (26,323–27,025) | 32,178 (32,172–32,191) | 11.84% less time |
| SILK decode | 21,091 (21,051–21,116) | 19,243 (19,240–19,292) | 20,446 (20,421–20,611) | 8.76% less time |
| Caller-buffer encode | 95,334 (95,219–95,657) | 64,120 (63,981–64,372) | 98,026 (97,801–98,043) | 32.74% less time |
| VoIP encode | 100,419 (100,316–100,731) | 69,433 (69,177–69,449) | 103,420 (103,319–104,235) | 30.86% less time |
| Low-delay encode | 94,922 (94,780–94,969) | 64,014 (63,850–64,774) | 97,760 (97,517–99,567) | 32.56% less time |

### Native early end-to-end capture at 6c730f47

[Run 36311118585](https://github.com/thesyncim/gopus/actions/runs/36311118585)
compares assembly `8ac93c85` with candidate SIMD/nosimd `6c730f47` on AMD EPYC
7763, Go 1.27.1, GCC 13.3, GOAMD64=v1 with runtime AVX2/FMA. Each row is the
median and range from four interleaved 500 ms samples at `-cpu=1`; all samples
report 0 B/op and 0 allocs/op. This early artifact records the E2E phases; it
does not report the full A/B workflow result.

| Fixture | Old assembly | Go SIMD | `nosimd` | SIMD vs assembly |
|---|---:|---:|---:|---:|
| CELT decode | 20,299 (20,174–20,430) | 15,448 (15,399–15,564) | 22,136 (22,047–22,236) | 23.9% less time |
| Hybrid decode | 28,378 (28,323–28,452) | 29,283.5 (29,203–29,290) | 35,398 (35,293–35,639) | 3.2% more time |
| SILK decode | 22,660 (22,534–22,767) | 21,704.5 (21,620–21,720) | 23,060 (22,961–23,192) | 4.2% less time |
| Caller-buffer encode | 91,919 (91,797–92,380) | 71,867 (71,573–72,034) | 114,042 (113,945–114,143) | 21.8% less time |
| VoIP encode | 98,407 (98,068–98,882) | 77,797 (77,475–77,865) | 120,566.5 (120,336–120,734) | 20.9% less time |
| Low-delay encode | 91,222.5 (90,783–92,064) | 71,331 (71,110–71,619) | 113,596.5 (113,460–113,959) | 21.8% less time |

A separate caller-buffer-only capture in the same run has four 500 ms samples:
assembly `8ac93c85` measures 91,641.5 ns/op median and SIMD `6c730f47` measures
71,613 ns/op, with zero allocations in each sample.

### Intel Xeon 8573C end-to-end capture at 49595303

[Run 36312921742](https://github.com/thesyncim/gopus/actions/runs/36312921742)
compares assembly `8ac93c85` with candidate SIMD/nosimd `49595303` on Intel
Xeon 8573C, Go 1.27.1, GCC 13.3.0, and GOAMD64=v1 with runtime AVX2/FMA.
Each median and range uses four interleaved 500 ms samples at `-cpu=1`. All
72 samples report 0 B/op and 0 allocs/op. The Hybrid row is 11.5% faster than
assembly on Xeon; the EPYC 7763 capture above measures 3.2% more time for
Hybrid. These are separate CPU-specific results.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 21,023.5 (20,895–21,654) | 13,096 (13,044–13,125) | 19,136.5 (19,051–19,191) |
| Hybrid decode | 26,653.5 (26,398–26,813) | 23,589 (23,374–23,656) | 29,095.5 (28,734–30,222) |
| SILK decode | 18,773 (18,701–18,878) | 17,133.5 (17,045–17,205) | 18,207.5 (18,174–18,257) |
| Caller-buffer encode | 84,891.5 (84,375–89,974) | 56,906.5 (56,428–57,054) | 87,453 (87,084–87,953) |
| VoIP encode | 89,261 (88,880–89,514) | 61,404 (61,235–62,155) | 91,614 (90,972–92,943) |
| Low-delay encode | 84,465.5 (84,301–85,092) | 56,966 (56,641–58,557) | 87,121 (86,620–87,418) |

### Early end-to-end capture at 5e14f0d8 (AMD EPYC 9V74)

The early artifact for [run 36314688914](https://github.com/thesyncim/gopus/actions/runs/36314688914)
compares assembly `8ac93c85` with candidate SIMD/nosimd `5e14f0d8` on AMD
EPYC 9V74, Go 1.27.1, GCC 13.3.0, `GOAMD64=v1`, and runtime AVX2/FMA.
Each median and range uses four interleaved 500 ms samples at `-cpu=1`. All
72 samples report 0 B/op and 0 allocs/op. The early artifact adds native
full-sequence DRED encoder packet/range confirmation; it is not a complete CI
pass. The Hybrid result is 4.4% slower than assembly here, distinct from the
11.5% faster Xeon result at `49595303` and the 3.2% slower EPYC 7763 result at
`6c730f47`.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,214.5 (20,151–20,261) | 14,991 (14,900–15,093) | 21,936 (21,887–21,994) |
| Hybrid decode | 29,724 (29,657–29,807) | 31,019.5 (30,904–31,173) | 36,676 (36,639–36,745) |
| SILK decode | 23,533 (23,514–23,601) | 22,735.5 (22,723–22,760) | 23,746 (23,696–23,809) |
| Caller-buffer encode | 92,658.5 (92,546–93,027) | 66,356 (66,120–66,681) | 115,163.5 (115,007–115,487) |
| VoIP encode | 99,028.5 (98,794–99,252) | 72,499.5 (72,242–72,742) | 120,617.5 (120,545–120,677) |
| Low-delay encode | 91,788.5 (91,618–93,461) | 66,251 (66,092–66,498) | 114,643.5 (114,524–114,675) |


### Early end-to-end capture at 1372df2e (AMD EPYC 9V74)

Early artifact `10931238005` for [run 36317174950](https://github.com/thesyncim/gopus/actions/runs/36317174950)
compares assembly `8ac93c85` with candidate SIMD/nosimd `1372df2e` on AMD
EPYC 9V74, Go 1.27.1, GCC 13.3.0, `GOAMD64=v1`, and runtime AVX2/FMA.
Each median and range uses four interleaved 500 ms samples at `-cpu=1`. All
72 samples report 0 B/op and 0 allocs/op. This is an early artifact, not a
complete CI pass. Hybrid decode is 4.4% slower than assembly in this capture;
the M4 deemphasis measurement below does not establish an AMD64 gain.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,183 (20,155–20,311) | 14,977 (14,948–15,082) | 21,944 (21,887–21,969) |
| Hybrid decode | 29,793 (29,728–29,884) | 31,100 (30,975–31,256) | 36,948.5 (36,841–37,015) |
| SILK decode | 23,728 (23,523–23,907) | 22,714.5 (22,662–22,769) | 23,889 (23,879–24,124) |
| Caller-buffer encode | 92,859.5 (92,385–92,910) | 66,324.5 (66,000–66,473) | 115,785.5 (115,575–116,160) |
| VoIP encode | 99,241 (98,901–100,825) | 72,325 (72,273–72,461) | 121,692.5 (121,507–122,085) |
| Low-delay encode | 91,891.5 (91,549–92,053) | 65,979 (65,915–66,177) | 115,150.5 (115,122–115,566) |

### Early end-to-end capture at 17e48afb (AMD EPYC 9V74)

Early artifact `10932180956` for [run 36319060882](https://github.com/thesyncim/gopus/actions/runs/36319060882)
compares assembly `8ac93c85` with candidate SIMD/nosimd `17e48afb` on AMD
EPYC 9V74, Go 1.27.1, GCC 13.3.0, `GOAMD64=v1`, and runtime AVX2/FMA.
Each median and range uses four interleaved 500 ms samples at `-cpu=1`. All
72 samples report 0 B/op and 0 allocs/op. This is an early artifact, not a
complete CI pass. Hybrid decode is 3.9% slower than assembly in this capture;
the 1372 EPYC capture was 4.4% slower, while the separate Xeon capture was
11.5% faster. The native CELT/SILK xcorr exact-oracle and warm zero-allocation
gates pass; the full artifact at this revision supplies direct timings in rows
25 and 47 below.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 15,697.5 (15,644–15,848) | 11,624 (11,572–11,643) | 16,985.5 (16,978–17,061) |
| Hybrid decode | 23,079.5 (23,060–23,173) | 23,990 (23,967–24,060) | 28,650 (28,563–28,660) |
| SILK decode | 18,299.5 (18,288–18,656) | 17,759 (17,658–17,850) | 18,535.5 (18,465–18,654) |
| Caller-buffer encode | 71,695.5 (71,639–71,772) | 51,381.5 (51,232–51,452) | 89,639 (89,457–90,076) |
| VoIP encode | 76,544 (76,476–76,765) | 56,245 (56,138–56,361) | 94,358 (93,997–100,544) |
| Low-delay encode | 71,871.5 (71,016–73,470) | 51,228.5 (51,154–51,392) | 89,314 (89,146–89,440) |

### Latest early end-to-end capture at 0d10beb6 (AMD EPYC 7763)

Early artifact `10938258969` for [run 36339608359](https://github.com/thesyncim/gopus/actions/runs/36339608359)
compares assembly `8ac93c85` with SIMD/nosimd `0d10beb6` on AMD EPYC 7763,
Go 1.27.1, GCC 13.3.0, GOAMD64=v1. Four interleaved 500 ms samples use
`-cpu=1`; all 72 E2E samples report 0 B/op and 0 allocs/op. Values are
median (min–max) ns/op. The early correctness gate is failing combined-extension
allocation checks; these benchmark results do not establish complete parity.

| Workload | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,277.5 (20,209–20,319) | 14,710 (14,678–14,836) | 22,390.5 (22,325–22,434) |
| Hybrid decode | 28,387 (28,340–28,564) | 24,368.5 (24,327–24,587) | 34,879 (34,810–34,963) |
| SILK decode | 22,414.5 (22,373–22,558) | 16,946.5 (16,909–17,012) | 22,469.5 (22,423–22,685) |
| Caller-buffer encode | 92,290.5 (91,615–92,335) | 66,548 (66,271–67,296) | 111,311 (111,129–111,871) |
| VoIP encode | 98,085.5 (97,931–98,428) | 72,697.5 (71,979–73,091) | 118,129.5 (117,529–118,255) |
| Low-delay encode | 91,148 (90,773–91,436) | 66,141 (66,101–66,289) | 111,061.5 (110,282–111,379) |

Within this capture, SIMD takes 14.2–27.9% less time than assembly. Ratios
compare modes on this runner; the different CPU prevents revision-to-revision
claims from the f2af896b capture. Direct 53-symbol timings retain their own
recorded revisions and provenance.

Both native instruction lanes pass 176 fixed-QEXT encoder and 416 received/
Hybrid/PLC/band test nodes without failures or skips. All three combined
DRED+QEXT fixtures match 96 packets/ranges per fixture in both lanes, including
768 kb/s 5.1. Combined allocation gates fail: FOA records one allocation and
the high-rate 96-frame cycle records 24,760; ordinary 5.1 records zero.
The ARM64 frame-10 QEXT payload residual remains a separate active witness.

### Early end-to-end capture at f2af896b (AMD EPYC 9V74)

Early artifact `10936121257` for [run 36331924965](https://github.com/thesyncim/gopus/actions/runs/36331924965)
compares assembly `8ac93c85` with SIMD/nosimd `f2af896b` on AMD EPYC 9V74,
Go 1.27.1, GCC 13.3.0, `GOAMD64=v1`, and runtime AVX2/FMA. Medians and
ranges use four interleaved 500 ms samples per workload/build at `-cpu=1`.
All 72 samples report 0 B/op and 0 allocs/op, and all 93 recorded phases exit
successfully. This early artifact does not establish a complete CI pass.
All six SIMD workloads take less time than assembly on this runner: CELT
28.0%, Hybrid 12.6%, SILK 26.0%, caller-buffer encode 28.6%, VoIP 27.1%,
and low-delay encode 28.2%. These are within-run comparisons; CPU and revision
labels remain part of each result. The separate `c5537a97` / EPYC 7763 capture
in artifact `10935810754` measures 14.4% less time for Hybrid; it does not
establish a cross-host revision ratio.

| Fixture | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 15,694.5 (15,674–15,734) | 11,303.5 (11,281–11,324) | 17,067.5 (17,044–17,110) |
| Hybrid decode | 23,130.5 (23,085–23,197) | 20,222.5 (20,155–20,276) | 28,534 (28,405–28,569) |
| SILK decode | 18,274.5 (18,270–18,314) | 13,519 (13,491–13,729) | 18,176 (18,153–18,207) |
| Caller-buffer encode | 71,885.5 (71,821–72,144) | 51,323.5 (51,066–51,553) | 89,088.5 (88,839–89,232) |
| VoIP encode | 76,751.5 (76,652–76,832) | 55,930.5 (55,562–56,061) | 93,680 (93,622–93,732) |
| Low-delay encode | 71,158 (70,911–71,187) | 51,104 (50,899–51,288) | 88,736.5 (88,481–88,807) |

### ARM64 correctly rounded FMA

At `3e93446d`, public `opusmath.FMA32` uses a non-inlined FMADDS/RET on ARM64.
The call boundary prevents Go constant folding from rounding through a wider
intermediate. The unchanged exact tie witness and 200,000 finite cases pass;
other architectures retain the correctly rounded software implementation.
Five interleaved 300 ms samples on M4/Go 1.27.0 compare the software helper
with this native helper, using otherwise identical SIMD binaries:

| Fixture | Software FMA ns/op | Native FMA ns/op | Change | Allocs/op |
|---|---:|---:|---:|---:|
| CELT decode | 8,419 | 8,377 | -0.5% | 0 |
| Hybrid decode | 16,115 | 16,223 | +0.7% | 0 |
| SILK decode | 10,223 | 10,030 | -1.9% | 0 |
| Caller-buffer encode | 47,771 | 47,192 | -1.2% | 0 |
| VoIP encode | 53,208 | 52,067 | -2.1% | 0 |
| Low-delay encode | 48,235 | 46,748 | -3.1% | 0 |

Small deltas are near measurement noise. These are Go-to-Go measurements;
they do not replace the assembly comparison or establish a new AMD64 result.

## Per-symbol inventory

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
| 5 | `haar1Stride1NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 9.865 (9.791–14.28) → 14.37 (14.34–14.47) → 8.095 (8.057–8.133) | 0 | measured; SIMD 18% faster than asm median; asm range is noisy |
| 6 | `haar1Stride2NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | N=32, old asm → Go → SIMD: 18.73 (14.25–20.18) → 25.88 (25.56–26.26) → 10.59 (10.55–10.80) | 0 | measured; SIMD 43% faster than asm median; asm range is noisy |
| 7 | `haar1Stride4NEON` | arm64 | `internal/celt/haar1_simd_arm64.go`; `internal/celt/haar1_neon_default.go` | archsimd / scalar | original direct N=32 old asm → scalar Go → Go SIMD: 14.47 (14.40–14.63) → 41.43 (41.35–41.53) → 17.70 (17.64–17.72); refined Go-only paired direct N=32: 11.69 → 11.14 median; live wrapper n0=32 (helper groups=16): 11.25 → 8.00 median | 0 | refined SIMD improves 4.7% on the direct N=32 fixture and 29% on the live wrapper fixture; exact parity, full CELT modes, and focused checkptr pass; asm comparison is from an earlier run |
| 8 | `imdctPostRotateF32FromKiss` | arm64 | `internal/celt/imdct_post_kiss_simd_arm64.go`; `internal/celt/imdct_post_kiss_default.go` | archsimd / scalar | N=120: old asm → Go → SIMD: 57.00 (56.43–60.80) → 56.72 (56.41–57.17) → 57.09 (56.93–57.23) | 0 | measured; SIMD within 0.2% of asm |
| 9 | `imdctPreRotateFMA32Kiss` | arm64 | `internal/celt/imdct_pre_kiss_simd_arm64.go`; `internal/celt/imdct_pre_kiss_arm64_nosimd.go` | archsimd / scalar | N=120: old asm 19.28 (19.19–19.31) → scalar Go 74.08 (73.89–74.25; earlier Go 1.27.1 run) → Go SIMD 20.41 (20.40–20.49) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 5.9% slower than asm and 23% faster than the first SIMD port |
| 10 | `imdctTDACWindowFMA32` | arm64 | `internal/celt/imdct_tdac_simd_arm64.go`; `internal/celt/imdct_tdac_default.go` | archsimd / scalar | overlap=120/count=60: old asm → Go → SIMD: 24.96 (24.95–25.44) → 95.45 (95.02–96.67) → 25.22 (25.00–25.43) | 0 | measured; SIMD within 1.0% of asm |
| 11 | `celtInnerProd8FMA32` | arm64 | `internal/celt/inner_prod_fma_simd_arm64.go`; `internal/celt/inner_prod_fma_simd_amd64.go`; `internal/celt/inner_prod_fma_default.go` | archsimd / scalar | N=16: 5.94–6.00 → 3.49; N=64: 20.94–21.00 → 6.13–6.43; N=176: 56.24–56.30 → 19.96–20.04 | 0 | measured; faster on M4 |
| 12 | `celtInnerProdSSEStyleAsm` | amd64 | `internal/celt/innerprod_sse_simd_amd64.go`; `internal/celt/innerprod_sse_default.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 89.10 (89.01–89.23) → 393.9 (393.6–394.6) → 81.66 (81.44–81.74) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all three modes allocate zero; SIMD is 8.3% less time than assembly |
| 13 | `kfBfly4M1Core` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly4m1_default.go` | archsimd / scalar | N=128: old asm 103–105, scalar Go 161–166, prior SIMD 157–162 in original paired run; refined SIMD 120.9 median versus prior SIMD 178.0 median in seven paired 500 ms samples | 0 | refined SIMD is 32% faster than prior SIMD in its paired run and about 16% slower than the recorded asm baseline; exact bits, zero alloc, full CELT modes, and focused checkptr pass |
| 14 | `kfBfly5Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 376.0 (375.8–376.1) → 538.2 (537.2–541.4) → 128.5 (127.3–132.1) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; SIMD is 65.8% less time than assembly |
| 15 | `kfBfly3Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 222.5 (222.3–222.8) → 208.2 (208.1–208.3) → 47.99 (47.93–48.03) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; SIMD is 78.4% less time than assembly |
| 16 | `kfBfly4Inner` | amd64 | `internal/celt/kf_bfly_simd_amd64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4, old asm → scalar Go → Go SIMD: 216.6 (216.4–216.7) → 271.3 (270.4–275.3) → 64.99 (64.11–66.34) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; SIMD is 70.0% less time than assembly |
| 17 | `kfBfly5Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 103.9–105.9 → scalar Go 183.0–188.2 → Go SIMD 71.3–73.4 ns/op | 0 | SIMD is about 31% faster than asm and 2.6× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 18 | `kfBfly3Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.4–72.1 → scalar Go 75.3–75.6 → Go SIMD 34.1–36.0 ns/op | 0 | SIMD is about 50% faster than asm and 2.1× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 19 | `kfBfly4Inner` | arm64 | `internal/celt/kf_bfly_simd_arm64.go`; `internal/celt/kf_bfly_default.go` | archsimd / scalar | m=8, N=4 paired M4: old asm 70.5–71.1 → scalar Go 89.2–90.7 → Go SIMD 43.9–45.0 ns/op | 0 | SIMD is about 37% faster than asm and 2.0× faster than scalar Go; exact old-asm/FMA parity and zero-alloc checks pass |
| 20 | `l1AbsSumNeon` | arm64 | `internal/celt/l1_abs_sum_simd_arm64.go`; `internal/celt/l1_abs_sum_default.go` | archsimd / scalar | N=480: old asm → Go → SIMD: 123.2 (122.4–123.3) → 493.0 (475.0–529.3) → 50.59 (50.48–50.78) | 0 | measured; SIMD 59% faster than asm, scalar Go 4.0× slower |
| 21 | `mdctFold1StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.69 (36.57–36.81) → scalar Go 153.6 (153.4–153.7; earlier Go 1.27.1 run) → Go SIMD 37.18 (37.02–37.28) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.3% slower than asm and about 30% faster than the first SIMD port |
| 22 | `mdctFold3StoreNeon` | arm64 | `internal/celt/mdct_fold_simd_arm64.go`; `internal/celt/mdct_fold_default.go` | archsimd / scalar | n4=64, blocks=8: old asm 36.67 (36.65–38.30) → scalar Go 154.9 (154.8–155.5; earlier Go 1.27.1 run) → Go SIMD 37.35 (37.15–38.31) | 0 | measured on M4 with Go 1.27.0; SIMD is 1.9% slower than asm and about 30% faster than the first SIMD port; samples include one outlier per mode |
| 23 | `mdctMidFoldStoreNeon` | arm64 | `internal/celt/mdct_mid_fold_simd_arm64.go`; `internal/celt/mdct_mid_fold_default.go` | archsimd / scalar | n4=64, blocks=8 paired M4 Go 1.27.0: old asm 14.67 (14.58–15.38) → prior SIMD 15.56 (15.54–15.75) → packed SIMD 14.57 (14.50–14.69); scalar Go 109.4 (109.3–109.6) in an earlier fixture | 0 | packed SIMD is 6.4% faster than prior SIMD and at assembly speed; exact old-asm comparison, zero-alloc, and checkptr level 2 pass |
| 24 | `mdctPostTwiddleNeon` | arm64 | `internal/celt/mdct_post_twiddle_simd_arm64.go`; `internal/celt/mdct_post_twiddle_default.go` | archsimd / scalar | n4=64, pairBlocks=8: old asm median 12.71 (run medians 12.69–12.84) → Go SIMD 14.65 (14.64–14.72); prior Go SIMD 15.48 (15.39–15.79); scalar Go 103.4 (103.3–103.9; earlier Go 1.27.1 run) | 0 | measured on M4 with Go 1.27.0; Go SIMD is 15% slower than asm and about 5% faster than the prior SIMD loop; exact and zero-alloc checks pass |
| 25 | `xcorrKernelAVX8` | amd64 | `internal/celt/pitch_xcorr_kernel_simd_amd64.go`; `internal/celt/pitch_xcorr_tiny_simd_amd64.go`; scalar defaults | archsimd / scalar | CELT pitch wrappers, old asm → scalar Go → Go SIMD: coarse 240×360 2,686 (2,671–2,741) → 35,226 (35,221–35,241) → 3,446 (3,445–3,450); half 480×64 914.6 (907.3–924.4) → 12,573 (12,565–13,022) → 1,321 (1,318–1,322); tiny coarse 5×244 244.7 (244.5–245.9) → 468.2 (467.1–470.4) → 99.77 (99.73–100.1); tiny coarse PLC 5×244 244.6 (244.4–245.6) → 486.8 (486.4–493.6) → 99.81 (99.72–99.85); tiny fine 10×10 25.69 (25.67–25.72) → 38.32 (38.18–38.37) → 20.48 (20.47–20.50); tiny fine PLC 10×10 25.69 (25.67–25.70) → 33.12 (33.03–33.31) → 20.49 (20.48–20.56) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; results are fixture-specific and make no blanket xcorr speed claim |
| 26 | `prefilterDualInnerProdAsm` | arm64 | `internal/celt/prefilter_dual_inner_prod_simd_arm64.go`; default and nosimd variants | archsimd / scalar | N=240, old asm → Go → SIMD: 85.35 (85.08–86.27) → 237.0 (236.7–238.7) → 41.24 (41.10–41.56) | 0 | measured; SIMD 52% faster than asm; scalar Go 2.8× slower |
| 27 | `pvqSearchPulseLoopAVX` | amd64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | Direct helper old asm → scalar Go → SIMD build: 537.6 (536.3–539.6) → 766.3 (764.9–768.9) → 2,073 (2,028–2,094); production full search: 730.4 (725.3–733.4) → 839.6 (838.7–840.8) → 546.6 (544.8–559.6) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; direct pulse helper is not the finite-input production route, so full search is the live PVQ comparator |
| 28 | `pvqSearchPulseLoop` | arm64 | `internal/celt/pvq_search.go`; `internal/celt/pvq_search_default.go` | scalar Go | original N=48, pulses=16 old asm → Go → SIMD build: 552.6 (516.9–567.2) → 997.9 (964.7–1,006) → 1,013 (994.8–1,024); live-shaped Go-only paired scalar loop 705.0 (702.5–728.0) → two-position unroll 522.9 (518.5–527.3) | 0 | unroll is 25.8% faster than scalar on the production-shaped fixture; exact scan order, libopus parity, zero alloc, full CELT modes, and checkptr pass; asm comparison uses a different fixture |
| 29 | `x86RcpApprox4` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | Four varying lanes, old asm → Go SIMD: 1.916 (1.912–1.924) → 1.224 (1.223–1.228); no scalar direct benchmark | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; SIMD allocates zero and takes 36.1% less time than assembly |
| 30 | `x86PVQSearchBestIDSSE2` | amd64 | `internal/celt/pvq_search_x86_sse2.go` | archsimd | N=48 varying data, old asm → Go SIMD exact helper: 22.81 (22.76–23.60) → 22.24 (22.19–22.26); no scalar direct benchmark | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; SIMD allocates zero; direct exact helper is not the finite-input production route, so row 27 remains the live PVQ comparator |
| 31 | `scaleFloat32IntoNEON` | arm64 | `internal/celt/scale_into_simd_arm64.go`; `internal/celt/scale_into_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 3.286 (3.274–3.301) → 7.656 (7.618–7.791) → 2.716 (2.703–2.719); N=64 7.715 (7.689–7.741) → 27.34 (26.86–29.65) → 5.107 (5.038–5.152); N=176 16.31 (16.18–16.41) → 80.68 (80.36–81.15) → 11.52 (11.40–11.53); N=480 33.87 (33.69–34.02) → 200.7 (200.1–201.0) → 26.73 (26.27–26.77) | 0 | measured; SIMD 17–34% faster than asm, scalar Go 2.3–5.0× slower |
| 32 | `stereoMergeRescaleNEON` | arm64 | `internal/celt/stereo_merge_simd_arm64.go`; `internal/celt/stereo_merge_default.go` | archsimd / scalar | old asm → Go → SIMD: N=16 8.392 (8.366–8.427) → 12.79 (12.66–12.92) → 6.203 (6.169–6.215); N=64 17.61 (17.52–17.70) → 45.57 (45.49–46.16) → 12.05 (11.99–12.07); N=176 31.89 (31.78–31.91) → 121.7 (121.6–122.0) → 27.20 (27.08–27.30); N=480 71.40 (71.05–71.49) → 329.1 (328.5–329.5) → 70.09 (69.60–73.70) | 0 | measured; SIMD 2–32% faster than asm; scalar Go 1.5–4.6× slower |
| 33 | `toneLPCCorrAVXFMA` | amd64 | `internal/celt/tone_lpc_corr_default.go` | scalar Go in both candidate builds | N=480, old asm → ordinary scalar Go → same scalar helper in SIMD build: 506.9 (506.7–507.2) → 390.1 (389.0–390.3) → 944.6 (932.2–954.4) | 0 | run 36319060882 / full artifact 10931588043; both Go builds call the scalar helper, but the native build-mode timing gap remains unexplained and is not a SIMD-kernel regression claim; all samples allocate zero |
| 34 | `toneLPCCorr` | arm64 | `internal/celt/tone_lpc_corr_scalar_arm64.go`; `internal/celt/tone_lpc_corr_simd_arm64.go` | archsimd / scalar | cnt=480, delays=1/2: recorded old asm 163.4 (163.0–163.5); earlier scalar Go 559.0 (558.3–559.5), earlier SIMD 117.7 (117.5–118.0); current exact-order SIMD 1,109 median (803.8–1,287), five 300 ms samples | 0 | M4/Go 1.27.0 current diagnostic is noisy and slower; earlier SIMD timing uses a reduction that fails selected-C parity, so its speedup is not a current claim; all 26 LPC / 12 tone-detection checks pass in three modes, SIMD warm allocations are zero; controlled performance tuning remains |
| 35 | `xcorrKernel4Float32Neon4Acc` | arm64 | `internal/celt/xcorr_kernel_f32_neon_ordered_simd_arm64.go`; `internal/celt/xcorr_kernel_f32_default.go` | archsimd / scalar | N=480 recorded old asm 189.2 (186.9–190.2), scalar Go 699.3 (697.0–699.9), four-phase SIMD 167.8 (167.5–167.9); current ordered SIMD 402.2 (399.1–419.1), five 300 ms samples | 0 | replacement xcorrKernel4Float32NeonOrdered matches all 21 selected-C raw-bit cases with zero warm allocations; earlier four-phase speedup does not apply to exact arithmetic; current M4/Go 1.27.0 diagnostic is separate from the assembly run, and production batching remains optimization work |
| 36 | `cpuid` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper returns raw CPUID registers, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 37 | `xgetbv` | amd64 | Go runtime CPU feature flags used by `simd/archsimd` | startup feature discovery | n/a | n/a | not comparable; the old helper reads OS vector state during initialization, while Go dispatch consumes cached feature flags and has no per-call replacement |
| 38 | `reciprocalEstimate32` | arm64 | `internal/dnnmath/reciprocal_estimate_default.go` | scalar Go | input set of 64 normal float32 values, old asm → Go → SIMD build: 1.405 (1.398–1.428) → 2.021 (1.918–2.285) → 1.932 (1.915–2.308) | 0 | measured; Go emulation is 44% slower than FRECPE asm; SIMD build uses the same scalar routine |
| 39 | `fma32` | arm64 | `internal/lpcnetplc/fma32_arm64.go`; `internal/lpcnetplc/fma32_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.915 (1.913–1.916) → 0.5506 (0.5505–0.5517) → 0.5491 (0.5489–0.5509) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 40 | `gruFMA32` | arm64 | `internal/osce/lace/gru_fma_arm64.go`; `internal/osce/lace/gru_fma_default.go` | Go float32 expression / scalar | 64 varying input triples, old asm → Go → SIMD build: 1.914 (1.912–1.916) → 0.5497 (0.5494–0.5504) → 0.5502 (0.5488–0.5515) | 0 | measured; Go FMADD expression is 3.5× faster than the out-of-line asm call |
| 41 | `floatToInt16ScaledCore` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/float_to_int16_default.go` | archsimd / scalar | N=480, scale 1: old asm 23.33 (22.97–23.59) → prior Go SIMD 34.65 (34.32–35.32) → tuned SIMD 27.16 (26.55–27.47). Scale 32768: old asm 23.36 (22.89–23.72) → prior Go SIMD 34.66 (34.24–35.26) → tuned SIMD 34.92 (34.26–35.65). Scalar Go 489.8 (484.9–513.3; earlier Go 1.27.1 fixture). | 0 | paired M4 Go 1.27.0; live pitch path at scale 1 is 22% faster than prior Go and 16% slower than asm; scale 32768 has no measured gain; exact and zero-alloc checks pass |
| 42 | `innerProductFLPAVX2` | amd64 | `internal/silk/inner_product_flp_simd_amd64.go`; `internal/silk/inner_product_flp_amd64.go` | archsimd / scalar | N=480, old asm → scalar Go → Go SIMD: 68.85 (67.85–69.37) → 204.4 (203.5–204.9) → 69.69 (69.63–70.14) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; SIMD is 1.2% slower than assembly |
| 43 | `innerProductFLPArm64` | arm64 | `internal/silk/inner_product_flp_arm64.go` | scalar Go | original N=480 old asm → Go → SIMD build: 126.6 (126.5–126.7) → 205.5 (205.3–212.8) → 201.6 (200.8–211.3); refined Go-only paired prior loop 135.6 → bounds-hoisted loop 107.1 median | 0 | refined Go is 21% faster on the paired fixture; exact bits, zero alloc, full SILK modes, and checkptr pass; asm comparison uses a different fixture |
| 44 | `writeInt16AsFloat32Core` | arm64 | `internal/silk/convert_simd_arm64.go`; `internal/silk/int16_float32_default.go` | archsimd / scalar | N=480: old asm 28.81 (28.55–29.86) → prior Go SIMD 33.68 (33.63–34.56) → tuned Go SIMD 24.00 (23.76–24.10); scalar Go 216.4 (216.1–217.3; earlier Go 1.27.1 fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 17% faster than asm and 29% faster than prior SIMD; exact float bits and zero allocations |
| 45 | `synthesizeLPCOrder16Core` | arm64 | `internal/silk/lpc_synth_simd_arm64.go`; `internal/silk/lpc_synth_default.go` | archsimd / scalar | subframe=80: original paired M4 old asm 250.0 (228.9–250.5) → Go SIMD 341.9 (340.8–343.5); refined Go-only paired current SIMD 290.4–292.1 → refined SIMD 253.4–255.1; scalar Go 391.9 (387.7–394.4) in a separate run | 0 | refined SIMD is about 13% faster than prior SIMD on the paired fixture and close to the recorded asm baseline; exact parity, zero alloc, full SILK modes, and checkptr level 2 pass |
| 46 | `celtPitchXcorrFloatImplASM` | arm64 | `internal/silk/pitch_xcorr_impl_simd_arm64.go`; `internal/silk/pitch_xcorr_impl_default.go` | archsimd / scalar | length=240, maxPitch=120: old asm 3,690 (3,665–3,733) → tuned Go SIMD production 2,483 (2,461–2,496); direct SIMD 2,487 (2,473–2,492); prior SIMD 4,892 (4,868–4,910); scalar Go 13,188 (13,043–13,237; earlier fixture) | 0 | paired M4 Go 1.27.0; tuned SIMD is 33% faster than asm and 49% faster than prior SIMD; exact per-lag bits and zero allocations |
| 47 | `xcorrKernelAVX8` | amd64 | `internal/silk/pitch_xcorr_kernel_simd_amd64.go`; `internal/silk/pitch_xcorr_impl_simd_amd64.go` | archsimd / scalar | Live `celtPitchXcorrFloat` wrapper 120×300, old asm → scalar Go → Go SIMD: 1,412 (1,410–1,431) → 14,544 (14,531–14,550) → 1,766 (1,765–1,767) | 0 | run 36319060882 / full artifact 10931588043, five 300 ms samples on AMD EPYC 9V74 / Go 1.27.1 / GCC 13.3; all modes allocate zero; SIMD takes 25.1% more time than assembly in this fixture |
| 48 | `firInterpol21846Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 107.56 (105.68–148.66) → scalar Go 280.39 (275.17–287.17) → Go SIMD 115.73 (111.11–119.72) | 0 | paired M4 Go 1.27.0; SIMD is 2.4× faster than scalar Go and 7.6% slower than asm; exact and zero-alloc checks pass |
| 49 | `firInterpol32768Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 122.9 (122.5–124.7) → scalar Go 293.8 (293.2–296.0) → Go SIMD production 114.2 (113.2–115.6); direct SIMD core 113.5 (111.5–114.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 7% faster than asm and 2.6× faster than scalar Go; exact and zero-alloc checks pass |
| 50 | `firInterpol43691Core` | arm64 | `internal/silk/resample_fir_simd_arm64.go`; `internal/silk/resample_fir_default.go`; `internal/silk/resample_libopus.go` | archsimd / scalar | nOut=240: old asm 113.5 (113.2–114.5) → scalar Go 307.0 (304.7–308.7) → Go SIMD production 105.9 (105.7–106.5); direct SIMD core 106.2 (105.1–106.2) | 0 | paired M4 Go 1.27.0; production Go SIMD is 6.7% faster than asm and 2.9× faster than scalar Go; exact and zero-alloc checks pass |
| 51 | `up2HQCore` | arm64 | `internal/silk/up2hq_core_default.go`; `internal/silk/resample_libopus.go` | scalar Go | N=240: old asm → Go → SIMD build: 1,120 (1,096–1,176) → 1,133 (1,126–1,137) → 1,166 (1,154–1,171) | 0 | measured; scalar and SIMD Go are within 4% of asm |
| 52 | `convertFloat32ToInt16UnitBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 424.7 / exact Go SIMD 131.5 (131.3–132.7); recorded old asm 63.13 (62.17–63.76) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |
| 53 | `convertFloat32ToInt16SaturatingBlocks` | arm64 | `pcm_convert_simd_arm64.go`; `pcm_convert_arm64_nosimd.go` | archsimd / scalar | N=480, b36c1c20 M4/Go 1.27.0: scalar 422.4 / exact Go SIMD 106.9 (106.7–107.0); recorded old asm 52.31 (52.15–52.60) | 0 | five 300 ms samples per build; exact C tie/tail/invalid-lane checks and zero allocations pass; assembly timing is an earlier run with different rounding semantics and does not establish an exact-output speed ratio |

## Native AMD64 parity and quality comparison

The complete native default-tag run [36296680326](https://github.com/thesyncim/gopus/actions/runs/36296680326)
at `1e2dbe77` passes 27,548 nodes with 25 existing skips and no failures. The
captured artifact comparator passes; the comparator's 16 unit tests also pass.
This earlier revision supplies historical end-to-end and AMD64 symbol
measurements. The latest six-row E2E table is the early `54654e25` EPYC 7763
capture; the latest direct AMD64 symbol inventory uses full artifact
`10931588043` at `17e48afb`. Native feature evidence at `08a11a0f` includes 5,718 fixed-encode
events and 11,801 fixed stateful-decode events in both SIMD/nosimd lanes, plus
fixed SILK 129, QEXT 104, OSCE exact-PCM 36, and DRED initial-latent eight per
lane. Its early artifact is not a full CI pass.

Native early capture `36312921742` at `49595303` passes the LPCNet full-state
SIMD oracle and 42 DRED events in both SIMD and nosimd, including the
120-sample quality alignment. Its completed early artifact runs on Intel Xeon
8573C with Go 1.27.1 and GCC 13.3.0. The subsequent early artifact for run
`36314688914` at `5e14f0d8` also passes
`TestDREDLowDelayFullSequenceEncoderMatchesLibopus` in SIMD and nosimd: mono
and stereo each match all 220 packet byte strings and final ranges (440 packet
results total), with 219 DRED packets per sequence and zero warm allocations.
This is an early artifact, not a complete CI pass. The `17e48afb` early
artifact completes all recorded E2E benchmark phases with zero allocations and
passes native CELT/SILK xcorr exact-oracle and warm zero-allocation gates in
SIMD and nosimd; its completed full artifact also supplies the current direct
timings below. Its E2E performance rows appear above. The earlier captures at
`08a11a0f`, `6ce253b2`, and `6c730f47` retain their own zero-offset, state,
and measurement provenance; the `49595303` capture resolves the open native
neural and quality checks. The `e6f2b332` coverage/name and fixture status does
not describe the `1e2dbe77` comparator.

## Measurement follow-up

The 53-row inventory retains each measured revision and fixture. All 51
comparable routines have direct allocation measurements; startup CPU helpers
are not comparable per-call operations. The latest six-row E2E table is the
early `54654e25` capture on AMD EPYC 7763. SIMD takes less time than assembly
in all six workloads, including 14.1% less time for Hybrid decode. At
`17e48afb` on EPYC 9V74, Hybrid takes 3.9% more time; the Xeon 8573C
capture at `49595303` takes 11.5% less. These comparisons stay within each
run and CPU; they do not isolate the effect of a source change across hosts.

The 11 latest direct AMD64 symbol rows use full artifact `10931588043` from run
`36319060882` at `17e48afb` on EPYC 9V74, with five 300 ms samples per mode
and zero allocations. The finite-input production PVQ search is faster than
assembly; the direct pulse and best-ID helper rows measure different paths.
The direct CELT xcorr fixtures show the measured differences by shape, while
the SILK live wrapper takes 25.1% more time than assembly in this run. The
`17e48afb` xcorr follow-up places legacy scalar NaN/tail work after
`VZEROUPPER` and keeps partial gathers on integer/AVX instructions. Linux/amd64
cross-compilation, disassembly, native exact-oracle, and warm zero-allocation
gates pass; the timings in rows 25 and 47 are the current direct measurements.
Ratios remain specific to their fixtures and CPU.
