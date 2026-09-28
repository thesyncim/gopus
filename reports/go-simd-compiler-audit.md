# Go 1.27 SIMD compiler audit

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

## Validation

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
