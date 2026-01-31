# Decoder Performance Findings (2026-01-31)

This file summarizes allocation hotspots and candidate improvements in the Opus
decoder path. It is meant to help divide work across multiple people.

## Bench snapshot (local, Apple M2 Pro)

Command:

```
go test ./internal/celt -run=^$ -bench DecodeFrame -benchmem
```

Results:

- BenchmarkDecodeFrame: ~1.07 ms/op, 68,312 B/op, 21 allocs/op
- BenchmarkDecodeFrame_Stereo: ~2.08 ms/op, 149,936 B/op, 29 allocs/op

After initial CELT scratch reuse (branch `decoder-perf-allocs`):

- BenchmarkDecodeFrame: ~0.77 ms/op, 66,553 B/op, 11 allocs/op
- BenchmarkDecodeFrame_Stereo: ~1.55 ms/op, 147,475 B/op, 19 allocs/op

After PVQ scratch reuse (quantAllBandsDecodeWithScratch):

- BenchmarkDecodeFrame: ~0.78 ms/op, 51,563 B/op, 7 allocs/op
- BenchmarkDecodeFrame_Stereo: ~1.72 ms/op, 119,433 B/op, 14 allocs/op

Note: BenchmarkHybridDecode is skipped in `internal/hybrid/hybrid_test.go`, so
there is no reliable hybrid decode benchmark yet.

## Recent updates (decoder-perf-allocs)

- Added `decodeOpusFrameInto` and a buffer-based `Decoder.Decode` loop that avoids
  per-frame allocations at the top level.
- Removed `DecodeFloat32` / `DecodeInt16Slice`; callers must pass explicit buffers.
- Added `DecoderConfig` caps (`MaxPacketSamples`, `MaxPacketBytes`) and streaming
  `PacketReader` plumbing for buffer-based packet reads.
- CELT `DecodeFrame` now reuses a range decoder scratch to reach 0 allocs/op in
  `BenchmarkDecodeFrame` and `BenchmarkDecodeFrame_Stereo`.

## Allocation hotspots (by layer)

### Top-level decoder (gopus)

- Public `Decoder` path is now buffer-based; PLC/transition/redundancy use decoder scratch.
  Per-call allocations have been removed at this layer.

### CELT

- `internal/celt/decoder.go` (lines ~548-550)
  - `prev1Energy`, `prev1LogE`, `prev2LogE` copied via `append`, allocating every frame.
- `internal/celt/decoder.go` (lines ~621, ~624, ~636, ~675-677)
  - `energies`, `tfRes`, `offsets`, `pulses`, `fineQuant`, `finePriority` allocate each frame.
- `internal/celt/bands_quant.go` (lines ~1331-1349)
  - `quantAllBandsDecode` allocates `left`, `right`, `collapse`, `norm`, `lowbandScratch`.
- `internal/celt/decoder.go` (lines ~560-565)
  - `silenceE` allocates on silence frames.

### Hybrid

- `internal/hybrid/decoder.go` (lines ~210-245)
  - `silkUpsampled` allocated per frame, plus float32->float64 conversions.
- `internal/hybrid/decoder.go` (lines ~305-306)
  - `output` allocated per frame.
- `internal/hybrid/hybrid.go`
  - `float64ToFloat32` / `float64ToInt16` allocate each call.

### SILK

- `internal/silk/decode.go` (lines ~58, ~64, ~74, ~93)
  - `pulses`, `outInt16`, and float32 conversions allocate each frame.
- `internal/silk/decode.go` (lines ~116-119)
  - `mid` conversion allocates.
- `internal/silk/silk.go`
  - Wrapper conversions and resampler input buffers allocate per call.

## Suggested workstreams (to divide work)

1. **API / outer-loop: zero-alloc decode for float32**
   - Add `decodeOpusFrameInto(dst []float32, ...)` (or similar) so `Decoder.Decode`
     can pass caller-provided buffers through without allocating per frame.
   - Provide `DecodeInt16Into(data []byte, pcm []int16, scratch []float32)` or a
     Decoder-owned scratch buffer to avoid `make([]float32, needed)` in `DecodeInt16`.
   - Replace `extractFrameData` with an in-place decode loop or add `ParsePacketInto`
     to reuse caller-provided frame-size buffers.

2. **CELT scratch reuse (biggest allocation win)**
   - Add scratch fields in `internal/celt.Decoder` for:
     - `prevEnergyCopy`, `prevLogECopy`, `prevLogE2Copy` (size `MaxBands*channels`)
     - `tfRes`, `offsets`, `pulses`, `fineQuant`, `finePriority` (size `MaxBands`)
   - Replace `append([]float64(nil), ...)` with `copy` into scratch.
   - Consider `quantAllBandsDecodeInto` that writes into caller-provided buffers
     for `left`, `right`, `collapse`, `norm`, and `lowbandScratch`.
   - Target: reduce 21/29 allocs per frame to low single digits.

3. **Hybrid allocations + float32 pipeline**
   - Store `silkUpsampled` and `output` scratch buffers in `hybrid.Decoder`.
   - Consider a float32-only hybrid path (requires CELT decode output in float32).
   - Add `DecodeToFloat32Into` helpers to avoid per-call conversions.

4. **SILK per-frame scratch**
   - Decoder-scoped scratch for `pulses`, `outInt16`, and mid/side buffers.
   - Add `decodeFrameIntoInt16` / `decodeFrameIntoFloat32` helpers to fill caller buffers.
   - Reuse resampler input buffers inside the decoder struct.

5. **Packet parsing & frame loop**
   - Avoid `FrameSizes` slice allocation by using a fixed `[48]int` scratch.
   - Decode frames directly while parsing sizes to avoid `[][]byte` allocation.
   - For code 0/1/2 packets, parse and decode without any heap allocs.

6. **Conversions / logging**
   - Float64->float32 conversions currently allocate new slices; use caller-provided
     output buffers instead.
   - There is little string building in the hot decode path today. `strconv.Append*`
     only matters if we add logging/metrics; prefer it over `fmt.Sprintf` in those cases.

## Quick wins vs deeper changes

Quick wins:

- Reuse a `Decoder`-owned float32 scratch for `DecodeInt16`.
- Replace `extractFrameData` with an in-place decode loop using frame sizes.
- Add CELT scratch slices for `tfRes`, `offsets`, `pulses`, `fineQuant`, `finePriority`.

Deeper changes:

- `quantAllBandsDecodeInto` + output buffer plumbing for CELT.
- Float32-only decode pipeline for CELT/Hybrid to avoid float64 conversions.
- Public `DecodeInto` API for zero-alloc decode.

## Assembly candidates (amd64/arm64, Linux/macOS)

Likely wins in tight inner loops (vector-heavy, predictable strides):

- CELT IMDCT/MDCT kernels (`internal/celt/mdct.go`, `imdct_*` helpers).
- Band scaling / denormalization loops (`denormalizeCoeffs`, `scaleSamples`).
- PVQ normalization and pulse spreading loops (`internal/celt/bands_quant.go`).
- Overlap-add / windowing (`Synthesize`, `SynthesizeStereo`).
- De-emphasis filter (mono/stereo loops in `applyDeemphasis`).
- Resampler inner loops (SILK resampler in `internal/silk/resample_*`).
- Pitch comb filter / postfilter (`applyPostfilter`).

These map well to NEON (arm64) and AVX2/SSE2 (amd64). A staged approach:
start with NEON+SSE2 for MDCT/IMDCT + overlap-add, then expand to PVQ and
resampling once correctness is locked.

## Benchmark TODO

- Add a top-level `Decoder.Decode` benchmark using real Opus packets (testvectors)
  to measure allocations across the full pipeline.
- Add SILK/Hybrid decode benchmarks once valid packet fixtures are available.
