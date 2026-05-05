# Encoder Parity And Performance

Last updated: 2026-05-05

## Goal

Keep encoder quality and hot-path behavior on the same release standard as decoder parity: libopus-backed evidence, no silent coverage loss, zero allocations on caller-owned encode paths, and clearly tracked exactness gaps.

## Required Evidence

Run these before treating encoder parity or performance changes as merge-ready:

```sh
GOCACHE="$PWD/.gocache" make quality-report
GOCACHE="$PWD/.gocache" make bench-guard
GOCACHE="$PWD/.gocache" make bench-libopus-guard
GOCACHE="$PWD/.gocache" make verify-production
```

For focused CELT prefilter or postfilter changes, also run:

```sh
GOCACHE="$PWD/.gocache" go test ./celt -run 'TestRunPrefilterParityAgainstLibopusFixture|TestPrefilterPitchXcorr|TestCELTAssemblyWrappersMatchReferenceEdges' -count=1
GOCACHE="$PWD/.gocache" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run TestEncoderVariantCELTHeaderParityAgainstFixture -count=1 -v
```

## Current Snapshot

The 2026-05-05 local arm64 run produced `reports/quality/quality-report-20260505-143023Z.md` from pinned libopus 1.6.1:

- `test-quality`: `PASS`
- `test-compat`: `PASS`
- Encoder summary: `23 passed, 0 failed`
- Encoder mean gap: `0.02 Q`
- Variant parity cases captured: `92`
- Worst variant case: `CELT-FB-20ms-stereo-128k-chirp_sweep_v1` at `-0.32 Q`
- Worst decoder parity case: `hybrid-fb-20ms-mono-24k` with `Q=99.67`
- Worst decoder loss case: `celt-fb-20ms-mono-64k-plc/periodic9` with `Q=99.18`

This is a green quality-parity report, not a claim of byte-identical packets or universally identical waveforms for every possible stream.

`reports/quality/` is intentionally ignored because the report is generated evidence, not source. Keep durable parity rules in tests and docs, and regenerate reports when evaluating a branch.

## Performance Baseline

`make bench-guard` is the required absolute hot-path performance gate, and `make bench-libopus-guard` is the required pinned-libopus relative codec-throughput gate. The absolute gate catches local `ns/op` and allocation regressions; the libopus-relative gate compares official RFC 8251 decode throughput plus generated CELT, SILK, and Hybrid encoder workloads against libopus 1.6.1 on the same runner.

The 2026-05-05 production verification run on `darwin/arm64` with Apple M4 Max reported:

- `BenchmarkEncoderEncode_CallerBuffer`: median `49451 ns/op`, `0 B/op`, `0 allocs/op`
- `BenchmarkEncoderEncodeInt16`: median `49848 ns/op`, `0 B/op`, `0 allocs/op`

Do not raise guardrail thresholds without measured evidence and reviewer signoff. Zero allocations on public encode hot paths remain a release requirement.

## Exactness Ratchets

The CELT postfilter header ratchet is intentionally stricter than the quality report. It verifies byte-level postfilter header choices for all covered CELT variant fixtures.

Current known CELT header gaps:

- None. The strict libopus-backed ratchet covers all 28 CELT variant fixtures on the generic/arm64 and amd64 fixture families.

On amd64, CELT prefilter pitch and inner-product helpers intentionally use the
deterministic scalar float32 path for encoded-header decisions. AVX/FMA
reassociation can flip tied postfilter pitch choices by one sample against the
pinned amd64 fixture family, which is a bitstream divergence even when the
quality report remains green.

Do not add an exclusion unless the divergence is backed by a fixture and documented with the exact affected architecture and cases.

## Quality Report Coverage

`tools/qualityreport` must fail closed when a passing test run produces no parsed cases for an expected section. Required parsed sections are:

- Encoder summary cases
- Encoder variant cases
- Decoder parity cases
- Decoder loss cases
- Transition cases

This prevents format drift in test logs from producing a green but empty report.
