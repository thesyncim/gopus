# Assembly Validation

This project treats assembly as a measured replacement for a pure Go codec
primitive, never as the only specification of that primitive. Every assembly
path must keep a scalar Go implementation that is easy to inspect, runs under
`-tags=purego`, and is used as the differential oracle for tests and fuzzing.

## Contract

- Every `.s` file must have a `//go:build ... && !purego` constraint.
- Every Go declaration file that uses `//go:noescape` must also be excluded by
  `!purego`.
- The scalar fallback must build under `-tags=purego`.
- Tests compare the public wrapper, not only the raw assembly symbol, so the
  dispatch and slice-bound preconditions are covered too.
- Integer and layout helpers must be bit-exact against Go references.
- Float helpers should be bit-exact when the algorithm promises a specific
  accumulation order; otherwise use a documented relative/absolute tolerance.
- Hot decode/encode paths must keep `0 allocs/op`.

The repository enforces the first two rules with
`TestAssemblyValidationContract`.

## Differential Tests

Assembly-backed functions should have deterministic edge tests over lengths
that exercise scalar tails and vector bodies:

- `1, 2, 3, 4, 5, 7, 8`
- lane boundaries such as `15, 16, 17, 31, 32, 33`
- codec sizes such as `80, 120, 480, 960`
- offset slices to shake out alignment assumptions

Current coverage lives in:

- `celt/asm_parity_fuzz_test.go`
- `silk/asm_kernels_test.go`
- existing FFT, IMDCT, PVQ, CWRS, Haar, LPC, and PCM conversion tests

## Fuzzing

Fuzz wrappers with bounded, valid codec-domain inputs. Keep generated samples
finite and mostly exactly representable so failures point to implementation
differences, not floating-point noise. Fuzz harnesses should vary:

- lengths, including tails and vector widths
- slice offsets from `0..3`
- stateful filter memories
- pulse counts, correlation windows, and FIR output counts

Run smoke fuzzing locally with:

```sh
GOWORK=off go test ./celt -run '^$' -fuzz FuzzCELTAssemblyWrappersMatchReference -fuzztime=10s -count=1
GOWORK=off go test ./silk -run '^$' -fuzz FuzzSilkAssemblyKernelsMatchReference -fuzztime=10s -count=1
GOWORK=off go test ./internal/dnnmath -run '^$' -fuzz FuzzReciprocalEstimate32FiniteAndBounded -fuzztime=10s -count=1
```

## Benchmarks

Assembly exists only when it wins on a relevant hot path. Benchmark current and
reference implementations side by side with `-benchmem`, track `ns/op` and
`allocs/op`, and keep benchmark input sizes tied to Opus/WebRTC workloads.

Use:

```sh
make bench-kernels
make bench-testvectors-report BENCH_TESTVECTORS_COMPARE_CASES=aggregate BENCH_TESTVECTORS_COMPARE_PATHS=float32,int16 BENCH_TESTVECTORS_COMPARE_TIMES=200ms,1s,5s
```

An assembly implementation should be removed or disabled when it is not faster,
is not allocation-neutral, cannot pass purego differential coverage, or needs
undefined alignment/aliasing assumptions to be correct.
