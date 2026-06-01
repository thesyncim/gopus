# gopus vs libopus performance baseline

Performance scoreboard comparing gopus encode+decode throughput against the
pinned libopus 1.6.1 reference (`tmp_check/opus-1.6.1`), plus a CPU-profile
ranking of the gopus encode/decode hot paths. This is a **measurement baseline**
for the perf-optimization agents — nothing here is optimized.

Captured on Apple M4 Max, `darwin/arm64`, Go default (no PGO override beyond the
committed `default.pgo`). Absolute ns differ by host; the **gopus/libopus ratio
(`g/l`)** is the portable figure.

`g/l < 1.00x` => gopus is FASTER than libopus. `g/l > 1.00x` => gopus is slower.

> **⚠️ FAIRNESS CAVEAT — the `g/l` ratios below are NOT a real-world comparison.**
> The libopus side links the pinned `tmp_check/opus-1.6.1/.libs/libopus.a`, which is
> the bit-exact **parity reference**: its `config.h` has every SIMD path disabled
> (`OPUS_ARM_MAY_HAVE_NEON`/`PRESUME_NEON`, `OPUS_ARM_ASM`, `OPUS_X86_*`, `OPUS_HAVE_RTCD`
> all `#undef`) so its math is deterministic scalar C. gopus, by contrast, runs its
> hand-written arm64 NEON / amd64 asm kernels. So these numbers compare
> **gopus-with-SIMD vs libopus-with-no-SIMD** and OVERSTATE gopus — the apparent
> "decode faster" result does not hold against a real libopus. A default
> `./configure` on aarch64 enables `OPUS_ARM_PRESUME_NEON_INTR`; a fair comparison
> must link a SIMD-enabled libopus build (kept separate from the scalar parity lib).
> Byte-parity is anchored in the pure-Go (`purego`) build vs the scalar reference;
> performance must be measured vs the SIMD build. **Re-benchmark against a NEON/SSE
> libopus before quoting any `g/l` figure.** The gopus-side absolute ns/op and the
> hot-path profile below remain valid (they are pure-gopus measurements).

> **FAIR RESULT (measured, M4 Max darwin/arm64, vs a NEON-enabled libopus).** With a
> SIMD libopus linked via `GOPUS_BENCH_LIBOPUS_A`, the honest reset+batch aggregates
> over 56 configs are **DECODE geomean g/l ≈ 1.35× (median 1.27×)** and **ENCODE
> geomean g/l ≈ 1.60× (median 1.60×)** — gopus is ~35 % slower on decode and ~60 %
> slower on encode, and slower than NEON libopus on **every** config tested (0 wins).
> The earlier "decode ~17 % faster" was purely the SIMD-disabled artifact (NEON
> libopus decode is ~1.6× the scalar reference, flipping the sign). Closest: SILK
> decode (~1.05–1.24×) and SILK-8k encode (~1.05×); worst: CELT (decode up to ~2.1×,
> encode up to ~2.5×). The bit-exact asm + zero-alloc work narrowed specific kernels
> but did not close the gap to hand-tuned SIMD C.
>
> **Reproduce the fair comparison** (the pinned parity lib must stay scalar, so build
> a separate SIMD libopus): `cp -R tmp_check/opus-1.6.1 /tmp/opus-neon && cd
> /tmp/opus-neon && ./configure CFLAGS="-O3 -DNDEBUG"` then `make -j ACLOCAL=true
> AUTOCONF=true AUTOMAKE=true AUTOHEADER=true MAKEINFO=true` (the `cp` breaks autotools
> timestamps; the tool-overrides no-op the regen since autoconf/automake aren't
> installed), then `GOPUS_BENCH_LIBOPUS_A=/tmp/opus-neon/.libs/libopus.a go test -tags
> gopus_libopus_bench -run TestScoreboardSummary -v .`

## Harness

- Go benchmarks: `benchmark_libopus_scoreboard_test.go` (build tag
  `gopus_libopus_bench`; the default build never compiles it and never links the
  reference toolchain).
- libopus reference: `tools/csrc/libopus_codec_bench.c`, a self-timing
  encode/decode helper built once against the pinned static `libopus.a`. It times
  the codec work inside one process, so its `ns/packet` excludes process spawn,
  file I/O, and decoder/encoder construction. **NOTE:** that pinned `libopus.a` is
  the SIMD-disabled parity reference (see the fairness caveat above) — the fair
  build links a separately-configured NEON/SSE libopus, not this one.
- Matched work: both sides encode/decode the same frame count at the same
  duration. gopus frame sizes are 48 kHz-relative (`F ∈ {120,480,960,2880}` =
  2.5/10/20/60 ms) and gopus consumes `F` samples of 48 kHz-rate PCM per frame;
  libopus consumes the rate-R equivalent (`F·R/48000` samples) per frame. Both
  use the same `testsignal` corpus class and duration (audio is the same signal
  class, not bit-identical). Complexity 10, CBR, forced mode per row.

Reproduce:

```
# Per-config Go benchmarks (gopus ns/op + libopus_ns/pkt + g/l ratio metrics):
go test -tags gopus_libopus_bench -run XXX -bench BenchmarkScoreboardEncode -benchtime 200x .
go test -tags gopus_libopus_bench -run XXX -bench BenchmarkScoreboardDecode -benchtime 200x .

# Full matrix scoreboard table in one shot (self-timed both sides):
go test -tags gopus_libopus_bench -run TestScoreboardSummary -v .

# CPU profiles (pure gopus, default build, no cgo):
go test -run XXX -bench 'BenchmarkEncoderEncode_(CallerBuffer|Stereo|VoIP|LongPacketCELT|LongPacketSILK)$' \
  -benchtime 2000x -cpuprofile enc.prof -o enc.test .
go test -run XXX -bench 'BenchmarkDecoderDecode_(CELT|Hybrid|SILK|Stereo|MultiFrame)$' \
  -benchtime 5000x -cpuprofile dec.prof -o dec.test .
go tool pprof -top -nodecount=20 enc.test enc.prof
go tool pprof -top -nodecount=20 dec.test dec.prof
```

## Scoreboard (matched configs, M4 Max)

Config matrix gated to combos both encoders accept:
CELT 8/16/24/48 kHz × 2.5/10/20 ms; SILK 8/16/24/48 kHz × 10/20/60 ms;
Hybrid 24/48 kHz × 10/20 ms; each × mono/stereo. `ns` columns are per-packet.

| config | enc gopus | enc libopus | enc g/l | dec gopus | dec libopus | dec g/l |
|---|--:|--:|--:|--:|--:|--:|
| CELT/mono/8k/2.5ms | 141595 | 93100 | 1.52x | 27083 | 25295 | 1.07x |
| CELT/mono/8k/10ms | 315536 | 271190 | 1.16x | 101234 | 69339 | 1.46x |
| CELT/mono/8k/20ms | 421267 | 488002 | 0.86x | 282676 | 190766 | 1.48x |
| CELT/mono/16k/2.5ms | 366627 | 299985 | 1.22x | 33206 | 41900 | 0.79x |
| CELT/mono/16k/10ms | 730529 | 466057 | 1.57x | 78901 | 101450 | 0.78x |
| CELT/mono/16k/20ms | 2139518 | 996335 | 2.15x | 456077 | 300891 | 1.52x |
| CELT/mono/24k/2.5ms | 560876 | 224477 | 2.50x | 38751 | 65345 | 0.59x |
| CELT/mono/24k/10ms | 500341 | 430053 | 1.16x | 117094 | 97700 | 1.20x |
| CELT/mono/24k/20ms | 1255100 | 599930 | 2.09x | 204404 | 176031 | 1.16x |
| CELT/mono/48k/2.5ms | 80445 | 98066 | 0.82x | 66039 | 30399 | 2.17x |
| CELT/mono/48k/10ms | 463113 | 338947 | 1.37x | 44170 | 105349 | 0.42x |
| CELT/mono/48k/20ms | 616634 | 876340 | 0.70x | 102788 | 116655 | 0.88x |
| CELT/stereo/8k/2.5ms | 130377 | 112515 | 1.16x | 18831 | 18128 | 1.04x |
| CELT/stereo/8k/10ms | 254852 | 395280 | 0.64x | 137439 | 95785 | 1.43x |
| CELT/stereo/8k/20ms | 2918803 | 1319287 | 2.21x | 819236 | 554460 | 1.48x |
| CELT/stereo/16k/2.5ms | 968327 | 403538 | 2.40x | 66656 | 96986 | 0.69x |
| CELT/stereo/16k/10ms | 3704361 | 1138545 | 3.25x | 181299 | 326732 | 0.55x |
| CELT/stereo/16k/20ms | 2772715 | 1572340 | 1.76x | 908715 | 592460 | 1.53x |
| CELT/stereo/24k/2.5ms | 382155 | 326310 | 1.17x | 86994 | 77063 | 1.13x |
| CELT/stereo/24k/10ms | 1824747 | 1309880 | 1.39x | 338342 | 298720 | 1.13x |
| CELT/stereo/24k/20ms | 7287118 | 2835100 | 2.57x | 775341 | 772945 | 1.00x |
| CELT/stereo/48k/2.5ms | 1290237 | 339961 | 3.80x | 104914 | 69637 | 1.51x |
| CELT/stereo/48k/10ms | 2067839 | 1362230 | 1.52x | 457430 | 269236 | 1.70x |
| CELT/stereo/48k/20ms | 4762122 | 2001510 | 2.38x | 894714 | 525195 | 1.70x |
| SILK/mono/8k/10ms | 1911760 | 1993950 | 0.96x | 34007 | 57591 | 0.59x |
| SILK/mono/8k/20ms | 3586728 | 5369760 | 0.67x | 35730 | 85827 | 0.42x |
| SILK/mono/8k/60ms | 5826051 | 11295235 | 0.52x | 50703 | 272381 | 0.19x |
| SILK/mono/16k/10ms | 6146479 | 2625730 | 2.34x | 48880 | 83929 | 0.58x |
| SILK/mono/16k/20ms | 10294619 | 9246080 | 1.11x | 61772 | 139699 | 0.44x |
| SILK/mono/16k/60ms | 19762419 | 13796765 | 1.43x | 697929 | 535578 | 1.30x |
| SILK/mono/24k/10ms | 2792417 | 2323180 | 1.20x | 109361 | 167313 | 0.65x |
| SILK/mono/24k/20ms | 12754832 | 3716940 | 3.43x | 166190 | 239938 | 0.69x |
| SILK/mono/24k/60ms | 17512419 | 12949059 | 1.35x | 378636 | 556085 | 0.68x |
| SILK/mono/48k/10ms | 3629663 | 2248220 | 1.61x | 102729 | 128026 | 0.80x |
| SILK/mono/48k/20ms | 7045299 | 4534580 | 1.55x | 187008 | 222182 | 0.84x |
| SILK/mono/48k/60ms | 11654250 | 10765000 | 1.08x | 218936 | 368667 | 0.59x |
| SILK/stereo/8k/10ms | 1206769 | 2103940 | 0.57x | 31120 | 36005 | 0.86x |
| SILK/stereo/8k/20ms | 5107583 | 4878580 | 1.05x | 70859 | 88106 | 0.80x |
| SILK/stereo/8k/60ms | 3105582 | 7542265 | 0.41x | 171698 | 254714 | 0.67x |
| SILK/stereo/16k/10ms | 2541924 | 2685450 | 0.95x | 38631 | 99301 | 0.39x |
| SILK/stereo/16k/20ms | 9404124 | 7635700 | 1.23x | 89989 | 188895 | 0.48x |
| SILK/stereo/16k/60ms | 16970618 | 14495529 | 1.17x | 259637 | 468156 | 0.55x |
| SILK/stereo/24k/10ms | 3493870 | 2200680 | 1.59x | 67709 | 109294 | 0.62x |
| SILK/stereo/24k/20ms | 8695905 | 3508770 | 2.48x | 195388 | 322587 | 0.61x |
| SILK/stereo/24k/60ms | 23909181 | 9918118 | 2.41x | 480321 | 1050255 | 0.46x |
| SILK/stereo/48k/10ms | 4175752 | 3287990 | 1.27x | 126527 | 88044 | 1.44x |
| SILK/stereo/48k/20ms | 8245162 | 5154140 | 1.60x | 199709 | 301652 | 0.66x |
| SILK/stereo/48k/60ms | 21414466 | 16989353 | 1.26x | 572550 | 892471 | 0.64x |
| Hybrid/mono/24k/10ms | 1173737 | 1505420 | 0.78x | 124096 | 135830 | 0.91x |
| Hybrid/mono/24k/20ms | 2676221 | 2352130 | 1.14x | 300597 | 318100 | 0.94x |
| Hybrid/mono/48k/10ms | 1668204 | 1953495 | 0.85x | 104078 | 190421 | 0.55x |
| Hybrid/mono/48k/20ms | 2040713 | 2629180 | 0.78x | 228047 | 195386 | 1.17x |
| Hybrid/stereo/24k/10ms | 4104307 | 2055050 | 2.00x | 222257 | 210922 | 1.05x |
| Hybrid/stereo/24k/20ms | 3238133 | 5158670 | 0.63x | 332947 | 407062 | 0.82x |
| Hybrid/stereo/48k/10ms | 3520833 | 1962420 | 1.79x | 153488 | 228438 | 0.67x |
| Hybrid/stereo/48k/20ms | 5466782 | 3693880 | 1.48x | 453469 | 500192 | 0.91x |

### Aggregate (n = 56 configs each direction)

| direction | geomean g/l | median g/l | verdict |
|---|--:|--:|---|
| ENCODE | 1.338x | 1.352x | gopus ~34% SLOWER than libopus |
| DECODE | 0.830x | 0.818x | gopus ~17% FASTER than libopus |

### Where gopus already ≥ libopus (g/l ≤ 1.00x) vs behind

- **Decode: gopus generally wins.** 37 of 56 decode configs are at/under 1.00x.
  Strongest decode wins are SILK (every SILK decode row except the two 48 kHz
  stereo cases is faster; SILK/mono/8k/60ms is 0.19x = ~5x faster) and Hybrid
  48 kHz. gopus loses decode mainly on CELT at very short frames / 48 kHz
  resampled output (CELT/mono/48k/2.5ms 2.17x, several CELT stereo rows ~1.5–1.7x).
- **Encode: gopus generally loses.** 41 of 56 encode configs are above 1.00x.
  gopus is consistently behind on **CELT encode** (most rows 1.2–3.8x; stereo and
  the 16/24 kHz resampled rates are the worst) and on **SILK 16/24 kHz encode**
  (the input resampler + NSQ del-dec stack), and on **stereo Hybrid encode**.
- **Encode wins** cluster at **low-rate SILK** (SILK/stereo/8k/60ms 0.41x,
  SILK/mono/8k/60ms 0.52x, SILK/stereo/8k/10ms 0.57x — gopus NSQ del-dec beats
  libopus there) and a few CELT 48 kHz / 8 kHz rows (CELT/mono/48k/20ms 0.70x,
  CELT/stereo/8k/10ms 0.64x).

Caveats: short-frame configs (2.5 ms) and the non-48 kHz CELT rates show the most
run-to-run variance because per-frame work is tiny relative to the per-pass
`Reset()` and the gopus 48 kHz↔API-rate resampler dominates; treat individual
short-frame rows as indicative, the aggregate as the headline.

## CPU profile — encode hot paths (top 20 flat)

`BenchmarkEncoderEncode_{CallerBuffer,Stereo,VoIP,LongPacketCELT,LongPacketSILK}`,
2000x. Flat % is of in-process samples. Encode is dominated by **SILK NSQ
del-dec** and **CELT PVQ band quantization**; warped-autocorrelation and the SILK
input resampler are next.

| # | flat% | function |
|--:|--:|---|
| 1 | 13.70% | `silk.noiseShapeQuantizerDelDec` |
| 2 | 7.67% | `runtime.asyncPreempt` (scheduler artifact) |
| 3 | 5.18% | `silk.warpedARFeedback24States4` |
| 4 | 4.85% | `silk.warpedAutocorrelationFLP32` |
| 5 | 3.34% | `runtime.pthread_cond_signal` (scheduler artifact) |
| 6 | 3.08% | `celt.xcorrKernel4Float32` |
| 7 | 3.08% | `silk.silkNLSFDelDecQuant` |
| 8 | 2.56% | `encoder.(*TonalityAnalysisState).tonalityAnalysis` |
| 9 | 2.36% | `silk.innerProductF32Libopus` |
| 10 | 2.10% | `celt.mdctFMA32` |
| 11 | 1.90% | `internal/opusmath.roundFloat32ToInt32Even` |
| 12 | 1.77% | `celt.celtInnerProdNeonStyleNorm` |
| 13 | 1.57% | `celt.(*Encoder).transientAnalysisMonoFloat32` |
| 14 | 1.44% | `celt.mdctForwardOverlapF32Scratch` |
| 15 | 1.44% | `celt.pvqSearchPulseLoop` |
| 16 | 1.11% | `celt.opPVQSearchScratchNormWithInputMutation` |
| 17 | 1.11% | `encoder.silkResamplerDown2HPScaled` |
| 18 | 1.05% | `celt.expRotation1Norm` |
| 19 | 1.05% | `rangecoding.(*Encoder).normalize` |
| 20 | 0.98% | `celt.quantPartitionEncodeWithExtBudget` |

Top cumulative roll-ups (encode): `silk.(*Encoder).EncodeFrame` 45.9%,
`celt.(*Encoder).EncodeFrame` 34.9%, `silk.computeNSQExcitation` 24.2%,
`silk.NoiseShapeQuantizeDelDec` 23.9%, `celt.quantAllBandsEncodeScratchWithMode`
13.4%, `encoder.tonalityAnalysis` 7.0%.

**Encode optimization targets (ranked):** (1) SILK NSQ del-dec
(`noiseShapeQuantizerDelDec` + `warpedARFeedback24States4` +
`silkNLSFDelDecQuant` ≈ 22% combined); (2) CELT PVQ + MDCT
(`xcorrKernel4Float32`, `mdctFMA32`, `pvqSearchPulseLoop`,
`opPVQSearchScratchNormWithInputMutation`, `expRotation1Norm`); (3) warped
autocorrelation; (4) tonality analysis; (5) the SILK 48 kHz↔internal-rate
resampler (`silkResamplerDown2HPScaled`), which is why the 16/24 kHz CELT and SILK
encode rows are the worst `g/l`.

## CPU profile — decode hot paths (top 20 flat)

`BenchmarkDecoderDecode_{CELT,Hybrid,SILK,Stereo,MultiFrame}`, 5000x. Decode is
much cheaper (total in-process samples ~1.06s), so flat percentages are small but
the ranking is stable: **CELT band decode + IMDCT + deemphasis** dominate.

| # | flat% | function |
|--:|--:|---|
| 1 | 9.43% | `celt.mdctFMA32` |
| 2 | 5.66% | `runtime.asyncPreempt` (scheduler artifact) |
| 3 | 4.72% | `celt.(*Decoder).applyDeemphasisAndScaleMonoFloat32ToFloat32` |
| 4 | 4.72% | `celt.imdctPreRotateF32Spectrum` |
| 5 | 3.77% | `celt.(*Decoder).applyDeemphasisAndScaleToFloat32` |
| 6 | 3.77% | `silk.synthesizeLPCOrder16Core` |
| 7 | 3.77% | `silk.up2HQCore` |
| 8 | 2.83% | `celt.ncwrsUrow` |
| 9 | 2.83% | `celt.stereoMerge` |
| 10 | 2.83% | `celt.unext` |
| 11 | 2.83% | `math.Float32frombits` |
| 12 | 1.89% | `celt.antiCollapseGLogMode` |
| 13 | 1.89% | `celt.copyFloat32ToSig` |
| 14 | 1.89% | `celt.cwrsi32` |
| 15 | 1.89% | `celt.deemphasisStereoPlanarF32Core` |
| 16 | 1.89% | `celt.imdctPostRotateF32FromKiss` |
| 17 | 1.89% | `celt.mdctMulAddMix` |
| 18 | 1.89% | `celt.quantAllBandsDecodeWithScratchWithMode` |
| 19 | 1.89% | `celt.quantBandDecodeNoExtFast` |
| 20 | 1.89% | `celt.renormalizeVectorWithEnergy` |

Top cumulative roll-ups (decode): `celt.(*Decoder).DecodeFrame...` 78.3%,
`celt.quantAllBandsDecodeWithScratchWithMode` 39.6%,
`celt.(*Decoder).synthesizeDecodedFrame` 33.0%, `celt.quantBandDecodeNoExtFast`
27.4%, `celt.imdctOverlapWithPrevScratchF32Output32` 18.9%, `hybrid` decode 13.2%.

**Decode optimization targets (ranked):** (1) CELT IMDCT/MDCT
(`mdctFMA32`, `imdctPreRotateF32Spectrum`, `imdctPostRotateF32FromKiss`,
`mdctMulAddMix` ≈ 15% combined); (2) deemphasis + scale
(`applyDeemphasisAndScale*` + `deemphasisStereoPlanarF32Core` ≈ 11%); (3) CELT PVQ
band decode (`cwrsi32`, `ncwrsUrow`, `quantBandDecodeNoExtFast`); (4)
`stereoMerge`; (5) SILK LPC synthesis + up2HQ resampler for SILK/Hybrid streams.
Decode already beats libopus on aggregate, so these are for extending the lead
(notably the CELT 48 kHz / short-frame rows where gopus currently trails).
