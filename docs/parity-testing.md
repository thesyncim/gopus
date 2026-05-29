# Parity testing

How gopus proves it matches pinned libopus 1.6.1 without brittle or misapplied
gates. Two tiers, each with one job; nothing in between.

## Tier 1 — bit-exact kernel oracles (algorithmic correctness)

Isolated kernels (range coder, NLSF/LPC/gain, PVQ/bands, MDCT/KISS-FFT,
deemphasis, resamplers, DNN matmuls, …) are compared **bit-for-bit** against a
live libopus C oracle (`internal/libopustest`, `tools/csrc`), e.g.
`Float32bits(got) == Float32bits(want)`. This is the only tier that can catch an
algorithmic bug, so it stays a hard gate.

Bit-exactness for float kernels depends on float fusion: the Go arm64 backend
contracts `a*b+c` into a single FMADD, while libopus (clang, per-statement
`-ffp-contract`) does not fuse across C statements. gopus matches libopus with a
rounding barrier (`mul32`/`add32`/`sub32` and the `noFMA32*`/`fma32` helpers)
that is **gated by GOARCH, not by build tag** — the barrier is a property of the
arm64 hardware/compiler, so it must apply under `-tags purego` on arm64 exactly
as in the default build. A constraint like `arm64 && !purego` is a bug: it drops
the barrier under purego-arm64 and silently diverges from libopus.

`make test-build-config-matrix` reruns the oracle suite under `-tags purego` so
that class of regression fails immediately. The default arm64 build is covered by
the normal parity run; amd64 by CI. Every (GOARCH × tag) combination must produce
exactly one implementation with the correct fusion behavior.

## Tier 2 — end-to-end quality (perceptual / waveform parity)

Decoded/encoded audio is judged by the metric **RFC 8251** defines conformance
with: libopus's own `opus_compare` Q, delay-searched, plus waveform correlation
and RMS ratio. The single authority is `internal/qualitycompare`. This tier is
**build-invariant**: Q, correlation, and RMS are statistical/perceptual, so a
1-ULP FMA difference between build configs moves them far below their bars — they
do not break across arch/tags the way bit-exact comparison does. That is why
end-to-end parity is judged here, not by bytes.

`opus_compare`'s Q is valid **only** for coded audio at 48 kHz with at least
10 ms of content. It is invalid on resampled (sub-48 kHz), too-short, or
extrapolated (PLC/concealment) output, where it returns nonsense (often Q < 0)
even when the waveform matches libopus to a few 1e-3. The comparator removes that
judgement from the test:

```go
qualitycompare.AssertParity(t, candidate, reference, profile, intent, label)
```

`AssertParity` takes an objective `SignalProfile` (sample rate, channels, total
samples, and how many leading samples are coded vs concealment), then:

- **selects the metric automatically** per region — `opus_compare` Q for ≥ 10 ms
  of coded 48 kHz audio, waveform corr/RMS otherwise;
- **splits coded vs concealed** so Q is never scored on extrapolated samples;
- **gates on externally anchored bars** chosen by `ParityIntent`, never a
  per-test number.

Callers pass facts about the signal, not thresholds.

## Anchored bars (no magic numbers)

Every threshold is anchored to an external reference, never hand-picked:

1. **RFC 8251 conformance** — Q ≥ 0 (`IntentRFCConformance`).
2. **libopus self-variation** — gopus must track the libopus reference at least as
   closely as libopus tracks itself across builds/arches. Requiring bit-exactness
   end-to-end would hold gopus to a *higher* bar than libopus holds itself (e.g.
   libopus-amd64 differs from libopus-generic on a 2.5 ms chirp). The near-exact
   envelope (`IntentNearExact`: Q ≥ 20, corr ≥ 0.997, RMS ∈ [0.98, 1.02]) is that
   measured cross-build agreement, met by SILK/CELT/Hybrid on every covered case.

## Rules

- Parity references come from a **live libopus oracle** or a pinned
  libopus-generated fixture with recorded provenance — never from golden data
  captured from gopus's own kernels (that is circular and would validate a bug
  against itself).
- A documented per-arch residual (a proven 1-ULP transcendental/FMA tail that is
  not a gopus defect, e.g. amd64-only SSE rounding) is recorded with its root
  cause, never hidden by widening a bar.
- New parity tests use `AssertParity` (tier 2) or a live-oracle bit-exact check
  (tier 1). They do not invent local tolerance constants.
