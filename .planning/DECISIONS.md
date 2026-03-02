# Investigation Decisions

Last updated: 2026-03-02

Purpose: record durable keep/skip decisions to avoid re-running solved investigations.

Older decision entries were intentionally pruned on 2026-03-01 to keep this file compact.

## Entry Template

```text
date: YYYY-MM-DD
topic: <short scope name>
decision: <what to keep/stop doing>
evidence: <test name(s), command(s), fixture(s), or CI links>
do_not_repeat_until: <condition that invalidates this decision>
owner: <handle>
```

## Current Decisions

date: 2026-03-02
topic: Analysis MLP float32 weight-cache path and transient float32 scratch path
decision: Keep the analysis MLP fast path that preconverts global int8 dense/GRU weights to float32 once at init (`initAnalysisMLPWeightCaches`) and uses `gemmAccumF32` during `ComputeDense`/`ComputeGRU`. Keep transient analysis scratch in float32 (`transientTmp`, `transientEnergy`, `transientEnergyPairsF32`) to avoid float64<->float32 conversion churn in `celt.(*Encoder).transientAnalysisScratch`.
evidence: Quality/parity stayed green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `go test ./celt -run 'Test(Transient|PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|Tone)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). Perf evidence: root encode microbench (`go test . -run '^$' -bench 'BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$' -benchmem -benchtime=2s -count=5`) improved int16 cluster from ~`55k ns/op` to ~`50-51k ns/op` best samples; `make bench-guard` passed with encoder samples around ~`50.9-54.7k ns/op`, `0 allocs/op`. CPU profile comparison (`-cpuprofile` on `BenchmarkEncoderEncode`) showed `transientAnalysisScratch` flat share dropping from ~`7.3%` to ~`5.2%`.
do_not_repeat_until: analysis MLP topology/weights change, transient detector math order changes, or parity fixtures indicate regression tied to these fast paths.
owner: codex

date: 2026-03-02
topic: Tonality analysis redundant-energy scan and sqrt/log reuse
decision: Keep the tonality-analysis hot-path update in `encoder/analysis.go` that (1) computes `log(bandE)` once per band and reuses it for `logE` and `bandLog2`, (2) persists per-frame `sqrt(E)` into `SqrtE` for stationarity accumulation reuse across history frames, and (3) reuses first-pass `bandERaw` sums in bandwidth-mask evaluation instead of rescanning per-band bins.
evidence: A/B microbenchmark (`GOMAXPROCS=1 go test ./encoder -run '^$' -bench 'BenchmarkAnalysisBandEnergy(Legacy|Current)$' -benchmem -benchtime=2s -count=6 -cpu=1`) shows legacy `~528.7-539.5 ns/op` vs current `~400.1-402.5 ns/op` (~24-26% faster, `0 allocs/op`) for the optimized section. Quality/parity remained green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v`, `23 passed, 0 failed`). End-to-end perf gate stayed green (`make bench-guard`).
do_not_repeat_until: tonality-analysis band-accumulation math, stationarity definition, or bandwidth-mask sequencing changes in ways that invalidate this section-level A/B benchmark.
owner: codex

date: 2026-03-02
topic: Pitch downsample factor-2 specialization and state-rounding skip in prefilter
decision: Keep `pitchDownsample()` specialized fast path for `factor=2` mono/stereo in `celt/prefilter.go`, and keep conditional skipping of `prefilterMem`/overlap re-rounding in the default float32 prefilter path while retaining explicit rounding in debug/alternate-precision modes (`tmpSkipPrefInputRoundEnabled` or `tmpPrefilterF64Enabled`).
evidence: A/B microbenchmark (`go test ./celt -run '^$' -bench 'BenchmarkPitchDownsample(Current|Legacy)(Mono|Stereo)$' -benchmem -benchtime=2s -count=5`) shows stereo improvement from legacy `~2471-2519 ns/op` to current `~2252-2316 ns/op` (~8-10% faster); mono remains neutral/slightly improved (`~1931-2010 ns/op` legacy vs `~1920-1994 ns/op` current). Parity/compliance stayed green (`go test ./celt -run 'Test(PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|TransientAnalysis)' -count=1`; `go test ./celt -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`), full runnable package sweep passed, and `make bench-guard` passed.
do_not_repeat_until: prefilter pitch-downsample input layout/factor usage changes, float-precision debug flag semantics change, or libopus prefilter parity fixtures show rounding-behavior drift.
owner: codex

date: 2026-03-02
topic: Pitch search fine-stage candidate-window optimization
decision: Keep the `pitchSearch()` fine-stage rewrite in `celt/prefilter.go` that preserves libopus-equivalent candidate coverage (`±2` around both coarse winners) while replacing the full-range `abs()`-gated scan with explicit window loops and a single `clear(xcorr[:halfPitch])`. Keep the dedicated A/B benchmark fixture (`celt/pitch_search_bench_test.go`) to guard this hotspot against regressions.
evidence: Direct A/B benchmark on representative prefilter dimensions (`go test ./celt -run '^$' -bench 'BenchmarkPitchSearch(Current|Legacy)$' -benchmem -benchtime=2s -count=5`) showed current `~3764-3828 ns/op` vs legacy `~4088-4159 ns/op` (~8% faster) at `0 allocs/op`. Quality parity remained green (`go test ./celt -run 'Test(PrefilterPitchXcorr|RunPrefilterParityAgainstLibopusFixture|TransientAnalysis)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Perf guard stayed green (`make bench-guard` passed, encoder rows around `57.2/56.9 us/op`).
do_not_repeat_until: libopus pitch-search candidate semantics, prefilter pitch range geometry, or arm64/x86 correlation kernels change in ways that alter this hotspot's cost model.
owner: codex

date: 2026-03-02
topic: Prefilter mono fast path and selective input rounding
decision: Keep `celt/prefilter.go` mono fast-path gather/scatter (`copy`-based) in `runPrefilter`, and keep selective input rounding of only appended frame samples when prefilter history is already float32-quantized (`!tmpSkipPrefMemRoundEnabled`) while preserving full-buffer rounding fallback when debug flags bypass history rounding.
evidence: Libopus parity fixture remained exact after changes (`go test ./celt -run 'TestRunPrefilterParityAgainstLibopusFixture' -count=1 -v`: `cases=300`, mismatch counters `0`, `maxGainDiff=0.000000`). Additional guards passed: `go test ./celt -run 'Test(TransientAnalysis|PrefilterPitchXcorr)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`; `make bench-guard` passed. Conservative benchmark evidence on arm64 from guard-style runs shows `BenchmarkEncoderEncode` improving from roughly `~55.2 us/op` to `~54.6 us/op` and `BenchmarkEncoderEncodeInt16` from `~55.8 us/op` to `~54.8 us/op` (with expected run-to-run variance in standalone benchmark probes).
do_not_repeat_until: prefilter input precision policy, `tmpSkipPref*` debug semantics, or libopus prefilter fixture behavior changes.
owner: codex

date: 2026-03-02
topic: Transient analysis direct PCM consumption in hot path
decision: Keep `celt/transient.go` `transientAnalysisScratch` consuming channel samples directly from `pcm` during HP filtering (mono and stereo paths) instead of copying into a per-channel scratch slice first.
evidence: CELT transient/tone tests and parity remained green (`go test ./celt -run 'Test(Transient|Tone|PatchTransientDecision)' -count=1`; `go test ./celt -run 'TestTransientAnalysis' -count=1 -v`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Performance improved on arm64 (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$|BenchmarkEncoderEncode_VoIP$|BenchmarkEncoderEncode_LowDelay$)' -benchmem -count=5 -cpu=1`): `BenchmarkEncoderEncode` ~`55.7-55.9 us/op` -> ~`54.6-55.1 us/op`; `BenchmarkEncoderEncodeInt16` ~`56.4-56.6 us/op` -> ~`55.2-55.7 us/op`; `BenchmarkEncoderEncode_VoIP` ~`51.2-51.6 us/op` -> ~`50.2-50.3 us/op`. `make bench-guard` passed; `make verify-production` remained blocked only by known local `tmp_check` cgo-disabled setup.
do_not_repeat_until: transient-analysis input layout/control flow changes or libopus parity fixtures indicate a regression in transient decisions.
owner: codex

date: 2026-03-02
topic: Tonality analysis hot-path scratch hoist and bin-energy reuse
decision: Keep `encoder/analysis.go` tonality hot-path temporaries (`FFT in/out`, tonality/noisiness working arrays) as persistent `TonalityAnalysisState` scratch, and keep reuse of precomputed raw FFT bin energies in the later bandwidth pass instead of recomputing from `out[]`.
evidence: Quality/parity checks remained green (`go test ./encoder -run 'Test(Analysis|RunAnalysis|TonalityAnalysis|UpdateOpusVADReusesFreshAnalysis|AnalysisTraceFixtureParityWithLibopus)' -count=1`; `GOPUS_TEST_TIER=parity go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v` with `23 passed, 0 failed`). Performance improved on arm64 (`go test . -run '^$' -bench '^(BenchmarkEncoderEncode$|BenchmarkEncoderEncodeInt16$|BenchmarkEncoderEncode_VoIP$|BenchmarkEncoderEncode_LowDelay$)' -benchmem -count=5 -cpu=1`): `BenchmarkEncoderEncode` ~`56.6-57.4 us/op` -> ~`55.7-55.9 us/op`; `BenchmarkEncoderEncodeInt16` ~`56.6-57.8 us/op` -> ~`56.4-56.6 us/op`; `make bench-guard` passed. Profile evidence: tonality-analysis cum share `17.43% -> 14.21%`, `runtime.morestack` `3.49% -> 2.19%`.
do_not_repeat_until: tonality-analysis algorithm/control-flow changes or new libopus parity evidence requires rework of this path.
owner: codex

date: 2026-03-01
topic: Cross-arch ratchet hardening (SILK/Hybrid weak lanes)
decision: Keep tightened floors for `SILK-WB-20ms-mono-32k|am_multisine_v1` and `SILK-WB-40ms-mono-32k|am_multisine_v1` at `min_gap_db=-0.02` on both baselines. Keep tightened weak-lane floors for `HYBRID-SWB-40ms-mono-48k|impulse_train_v1` at default `-0.05` / amd64 `-0.12`, `SILK-WB-40ms-mono-32k|impulse_train_v1` at default `-0.03` / amd64 `-0.11`, and `SILK-NB-40ms-mono-16k|am_multisine_v1` at default `-0.03` / amd64 `-0.08`.
evidence: Focused repeated subtest probes across 3 runs per arch were deterministic for the three weak lanes: arm64 `+0.06/+0.03/-0.00 dB` and amd64 `-0.10/-0.09/-0.06 dB` (`HYBRID-SWB-40ms impulse`, `SILK-WB-40ms impulse`, `SILK-NB-40ms am`). Full variant parity and provenance remained green on arm64 + amd64 after tightening; compliance summary runs completed with no test failures; `make bench-guard` passed; `make verify-production` failed only on expected local `tmp_check` cgo-disabled setup.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK/Hybrid packetization/control-flow changes materially alter these lane distributions.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms amd64 ratchet floor hardening
decision: Keep tightened amd64 floors for `SILK-WB-60ms-mono-32k|am_multisine_v1` and `SILK-WB-60ms-mono-32k|impulse_train_v1` at `min_gap_db=-0.03`; keep default floors unchanged (`am_multisine=-0.03`, `impulse_train=-0.04`) due arm64 impulse stability at `-0.04 dB`.
evidence: Focused repeated subtest probes: arm64 `impulse_train` stayed `gap=-0.04 dB` (5 runs), amd64 `impulse_train` stayed `gap=-0.00 dB` (5 runs), arm64/amd64 `am_multisine` stayed `gap=0.00 dB` (5 runs). After tightening amd64 floors, full parity/provenance/compliance checks passed plus `make bench-guard`; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB 60ms packetization/control flow changes materially alter this lane distribution.
owner: codex

date: 2026-03-01
topic: Ambisonics high-order libopus parity coverage
decision: Keep expanded libopus parity matrix coverage for ambisonics families 2 and 3, including TOA (`16ch`, `18ch`) and family-3 4th/5th-order lanes (`25ch`, `27ch`, `36ch`, `38ch`), as the default regression guard.
evidence: `go test ./multistream -run 'TestLibopus_AmbisonicsFamily(2|3)Matrix' -count=1 -v` passed for all newly added lanes with stable internal-vs-libopus energy ratios and decode drift within guard thresholds; full `go test ./multistream -count=1` also passed.
do_not_repeat_until: ambisonics projection/mapping control flow, projection matrix defaults, or libopus-reference decode helper semantics change.
owner: codex

date: 2026-03-01
topic: SILK-WB-20ms am amd64 ratchet floor hardening
decision: Keep `SILK-WB-20ms-mono-32k|am_multisine_v1` amd64 floor at `min_gap_db=-0.05` (tightened from `-0.10`) while keeping the default floor at `-0.03`.
evidence: Repeated subtest probes were stable on arm64 and amd64 at `gap=-0.00 dB` for `TestEncoderVariantProfileParityAgainstLibopusFixture/cases/SILK-WB-20ms-mono-32k-am_multisine_v1`. After tightening, full parity/provenance/compliance checks stayed green on both arches plus `make bench-guard`; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB 20ms packetization/control flow changes materially alter this lane distribution.
owner: codex

date: 2026-03-01
topic: SILK WB ratchet hardening (40ms am + 60ms impulse amd64)
decision: Keep tightened floors for `SILK-WB-40ms-mono-32k|am_multisine_v1` at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`, and for `SILK-WB-60ms-mono-32k|impulse_train_v1` amd64 at `min_gap_db=-0.05` while keeping default at `-0.04`.
evidence: Repeated subtest probes were stable: arm64 `SILK-WB-40ms am` at `gap=-0.00 dB`, amd64 `SILK-WB-40ms am` at `gap=0.00 dB`; arm64 `SILK-WB-60ms impulse` at `gap=-0.04 dB` (so default floor kept), amd64 `SILK-WB-60ms impulse` at `gap=-0.00 dB`. After tightening, full `TestEncoderVariantProfileParityAgainstLibopusFixture` (arm64 + amd64), `TestEncoderVariantProfileProvenanceAudit`, `TestEncoderComplianceSummary`, and `make bench-guard` passed; `make verify-production` showed only the known local `tmp_check` cgo-disabled blocker.
do_not_repeat_until: fixture corpus, quality scoring semantics, or SILK WB packetization/control flow changes materially alter these lane distributions.
owner: codex

date: 2026-03-01
topic: Planning doc compaction policy
decision: Keep `.planning/ACTIVE.md`, `.planning/DECISIONS.md`, and `.planning/WORK_CLAIMS.md concise; archive deep history snapshots under `.planning/archive/`.
evidence: On 2026-03-01, live planning files grew to ~345KB total and reduced usability; archived full snapshots and rewrote compact operational summaries.
do_not_repeat_until: planning volume remains low and navigation cost is no longer a concern.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms am_multisine ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|am_multisine_v1` floors at default `min_gap_db=-0.03` and amd64 `min_gap_db=-0.05`.
evidence: Focused arm64/amd64 parity probes were stable at `gap=0.00 dB`; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #261).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: SILK-WB-60ms impulse ratchet floor hardening
decision: Keep `SILK-WB-60ms-mono-32k|impulse_train_v1` floors at default `min_gap_db=-0.04` and amd64 `min_gap_db=-0.08`.
evidence: Repeated focused arm64/amd64 probes were stable; full parity/provenance/compliance checks plus CI matrix stayed green (merged PR #260).
do_not_repeat_until: fixture corpus, quality metric semantics, or SILK WB 60ms packetization/control flow changes materially.
owner: codex

date: 2026-03-01
topic: Final CELT compliance residual override floor
decision: Keep the remaining no-negative override for `CELT-FB-2.5ms-mono-64k` at `0.191 dB`.
evidence: Deterministic residual observed at approximately `-0.190 dB` with stable packet-shape parity; tightened from `0.20` without regression (merged PR #259).
do_not_repeat_until: CELT 2.5ms parity/control-flow changes or compliance quality-measure semantics shift this residual lane.
owner: codex

date: 2026-02-28
topic: Compliance packet-cadence parity
decision: Keep compliance encode cadence aligned to libopus fixture behavior by allowing bounded trailing flush packets.
evidence: Summary improved from failing rows to stable pass status after cadence alignment; follow-on precision/parity guards remained green.
do_not_repeat_until: fixture cadence model (`signal_frames`/`frames`) or compliance harness semantics change.
owner: codex

date: 2026-02-28
topic: Compliance reference-Q decode-path alignment
decision: Keep reference-Q calibration on libopus-only decode path (direct helper first, `opusdec` fallback) for fixture honesty.
evidence: Refreshed reference-Q fixtures and preserved parity/compliance guard behavior after decode-path alignment.
do_not_repeat_until: libopus helper decode protocol or compliance fixture generation semantics change.
owner: codex

date: 2026-02-28
topic: Hybrid held-frame transition redundancy parity
decision: Keep libopus-style to-CELT redundancy on held SILK/Hybrid transition frames (`celt_to_silk=0` path).
evidence: Source-port closed previously negative hybrid residual lane while parity/provenance/compliance suites stayed green.
do_not_repeat_until: transition-policy semantics or redundancy signaling model changes in encoder hybrid flow.
owner: codex

date: 2026-02-13
topic: Verified areas skip policy
decision: Do not re-debug SILK decoder correctness, resampler parity path, or NSQ constant-DC behavior without new contradictory fixture evidence.
evidence: Sustained passing parity checks plus explicit AGENTS verified-area guidance.
do_not_repeat_until: related code paths or fixtures change.
owner: codex
