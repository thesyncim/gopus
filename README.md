# gopus

Pure-Go Opus codec — RFC 6716 / RFC 8251, targeting strong behavioral and
quality parity with pinned libopus 1.6.1, with no cgo.

Encoder, decoder, multistream, projection/ambisonics, Ogg, and RTP RED are
implemented in Go. Caller-buffer encode and decode paths are designed for
allocation-free steady-state use. Codec math and bitstream decisions follow the
pinned reference and are checked with a live C oracle (see
[Parity & testing](#parity--testing)).

## Install

```sh
go get github.com/thesyncim/gopus
```

Requires Go 1.27 or newer.

## Quick start

The caller-buffer API returns the number of bytes or samples written. Covered
steady-state encode and decode paths reuse the supplied buffers:

```go
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)
func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)
```

Encode one 20 ms stereo frame at 48 kHz, then decode it back:

```go
package main

import (
	"log"

	"github.com/thesyncim/gopus"
)

func main() {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960 // 20 ms at 48 kHz
	)

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		log.Fatal(err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		log.Fatal(err)
	}

	pcm := make([]float32, frameSize*channels) // your interleaved input
	packet := make([]byte, 4000)               // reusable encode buffer
	out := make([]float32, frameSize*channels) // reusable decode buffer

	n, err := enc.Encode(pcm, packet) // n = bytes written to packet
	if err != nil {
		log.Fatal(err)
	}

	samples, err := dec.Decode(packet[:n], out) // samples = per-channel samples
	if err != nil {
		log.Fatal(err)
	}
	_ = out[:samples*channels]
}
```

`int16` and `int24` PCM use the same caller-buffer shape — only the slice
element type changes:

```go
pcm16 := make([]int16, frameSize*channels) // interleaved 16-bit input
packet := make([]byte, 4000)
out16 := make([]int16, frameSize*channels)

n, err := enc.EncodeInt16(pcm16, packet)   // also EncodeInt24([]int32, …)
// …
samples, err := dec.DecodeInt16(packet[:n], out16) // also DecodeInt24(…, []int32)
```

Tune the encoder through libopus-style CTL methods (`SetBitrate`, `SetVBR`,
`SetComplexity`, `SetInBandFEC`, `SetDTX`, …). Pass a nil packet to `Decode` to
run packet-loss concealment for a dropped frame.

`NewEncoder` starts at 64 kbps; libopus starts with automatic bitrate selection.
Set matching controls explicitly when comparing their output. `Bitrate()`
returns the configured target, retaining `BitrateAuto` and `BitrateMax`;
libopus's bitrate getter reports an effective target instead.

See [examples/](examples/) for Ogg files, ffmpeg interop, RED loss recovery,
WebRTC control, and benchmarks.

## Features

| Area | gopus |
| --- | --- |
| Coding modes | SILK, CELT, Hybrid, with automatic mode selection |
| Sample rates | 8, 12, 16, 24, 48 kHz by default; native 96 kHz with `gopus_qext` |
| Channels | Mono, stereo, multistream, projection / ambisonics |
| Frame sizes | 2.5–120 ms |
| Bitrate control | CBR, VBR, CVBR, low-delay, DTX |
| Resilience | Packet loss concealment, in-band FEC / LBRR |
| PCM formats | `float32`, `int16`, `int24` (single-stream and multistream) |
| Containers | `container/ogg` (Ogg read/write), `container/red` (RFC 2198 RTP RED parse/build/recover) |
| libopus compatibility | CTL methods, packet parsing, soft clipping, and error-code helpers |

## Public API

The importable surface is five packages. Everything else lives under `internal/`
and is not importable.

| Package | Use it for |
| --- | --- |
| `github.com/thesyncim/gopus` | `Encoder` / `Decoder` (float32 / int16 / int24), streaming `Reader` / `Writer`, multistream and DRED constructors, packet parsing, repacketizer, soft clip, CTLs, error codes |
| `github.com/thesyncim/gopus/multistream` | Lower-level multistream `Encoder` / `Decoder` and projection / ambisonics (`NewProjectionEncoder` / `NewProjectionDecoder`) |
| `github.com/thesyncim/gopus/container/ogg` | Read and write Ogg Opus files (RFC 7845) |
| `github.com/thesyncim/gopus/container/red` | `Encoder` / `Decoder` structs (plus `Build` / `Parse` / `FindRecovery`) to build, parse, and recover RFC 2198 RTP RED payloads |
| `github.com/thesyncim/gopus/types` | Shared `Mode` / `Bandwidth` / `Signal` enums |

Multistream is reachable two ways: `gopus.NewMultistreamEncoder` /
`gopus.NewMultistreamDecoder` (and the `…Default` constructors for 1–8 channels
in Vorbis order) wrap the lower-level `multistream` package, which also carries
the projection / ambisonics constructors (mapping families 0/1/3/255).

Write an Ogg Opus file with the `container/ogg` writer:

```go
w, err := ogg.NewWriter(file, sampleRate, channels)
if err != nil {
	log.Fatal(err)
}
defer w.Close()

n, _ := enc.Encode(pcm, packet)
if err := w.WritePacket(packet[:n], frameSize); err != nil {
	log.Fatal(err)
}
```

## Optional features behind build tags

The default build includes core encode/decode/multistream/Ogg/RED and matches a
default libopus `./configure` feature set. Optional features follow libopus's
compile flags, with matching Go build tags. The default build links ZERO of their
code (enforced by
`TestDefaultBuildIsZeroCostForGatedFeatures`).

| gopus build tag | libopus flag |
| --- | --- |
| `gopus_dred` | `--enable-dred` |
| `gopus_osce` | `--enable-osce` (+ `ENABLE_DEEP_PLC`) |
| `gopus_qext` | `--enable-qext` |
| `gopus_custom_modes` | `--enable-custom-modes` |
| `gopus_fixed_point` | `--enable-fixed-point` |

Feature oracles use the pinned libopus build with the matching flags:

- **`gopus_dred`** — DRED (RDOVAE), control and standalone surfaces.
- **`gopus_osce`** — OSCE BWE / LACE / NoLACE plus the deep-PLC family
  (PitchDNN / FARGAN), matching the `--enable-osce` feature set.
- **`gopus_qext`** — QEXT framing and native 96 kHz (Opus HD). Native 96 kHz
  uses the shared SILK/Hybrid/CELT mode and history driver and is available only
  under this tag; default-build API rates stay 8/12/16/24/48 kHz. Paired C tests cover selected encoder, decoder,
  and loss cases; the complete QEXT feature/build matrix is still under review.
- **`gopus_custom_modes`** — Opus Custom standard modes.
- **`gopus_fixed_point`** — integer CELT/SILK pipeline (libopus `FIXED_POINT`).
  Paired scalar/SIMD oracles cover integer kernels, packet encoding, public
  decode formats, multistream, and mode transitions. The full feature and
  architecture matrix is still being validated.

One more tag is orthogonal to the feature flags above and has no libopus
equivalent:

- **`nosimd`** — forces the scalar Go reference path, including when
  `GOEXPERIMENT=simd` is set. Ordinary builds use scalar Go kernels. Set
  `GOEXPERIMENT=simd` to compile Go `archsimd` kernels where they are implemented;
  runtime CPU checks select supported kernels and the rest use scalar code. The
  `purego` tag has no effect in this repository. Compare scalar Go with scalar
  libopus and SIMD Go with libopus using matching CPU instructions.

Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op
returning `ErrOptionalExtensionUnavailable`. This matches a default libopus build,
where the DNN / PitchDNN / FARGAN / RDOVAE neural code is empty and none of it is
compiled; gopus keeps those packages out of the default import graph. DNN blob
loading (USE_WEIGHTS_FILE model loading) requires `-tags gopus_dred` or
`-tags gopus_osce`; QEXT requires `-tags gopus_qext`; DRED
control/standalone surfaces require `-tags gopus_dred`; OSCE BWE/LACE/NoLACE
require `-tags gopus_osce`. A build tag enables its API and implementation; it
does not imply that every feature, architecture, control sequence, and packet
combination has completed parity validation.

| Extension | Availability | Probe |
| --- | --- | --- |
| DNN blob loading | Available under `gopus_dred` / `gopus_osce` | `OptionalExtensionDNNBlob` |
| QEXT | Available under `gopus_qext` | `OptionalExtensionQEXT` |
| DRED | Available under `gopus_dred` (control + standalone) | `OptionalExtensionDRED` |
| OSCE BWE | Extra controls under `gopus_osce`; support probe returns false | `OptionalExtensionOSCEBWE` |

The `gopus_osce` tag enables the OSCE and deep-PLC family exposed by
libopus's `--enable-osce`. These controls are available for parity work;
`SupportsOptionalExtension(OptionalExtensionOSCEBWE)` reports false. The tagged
implementation is excluded from the default build.

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_osce ./...
```

```sh
make test-dnn-blob-parity
make test-qext-parity
make test-dred-tag
make test-extra-controls-parity
make test-custom-parity
```

## Performance

gopus is built for real-time use, where steady allocation is the enemy:

- **Zero-allocation hot paths.** Caller-buffer `Encode` / `Decode` (and their
  `int16` / `int24` variants) reuse caller-owned buffers. Focused warm-path tests
  assert zero allocations for the covered codec modes and feature builds.
- **Allocation-free containers too.** `container/ogg` (`Reader.ReadPacketInto` /
  `Writer.WritePacket`) and `container/red` (`Decoder.Parse` / `Encoder.Encode`)
  own their buffers and the redundant-frame history, so steady-state demux/mux and
  RED packetization allocate nothing once warm — each locked by an
  `AllocsPerRun == 0` test.
- **Go SIMD.** `GOEXPERIMENT=simd` opts into `simd/archsimd` kernels where they
  are implemented; runtime CPU checks select supported kernels and other kernels
  use scalar code. Ordinary builds stay scalar, and `-tags nosimd` forces the
  scalar path. All codec kernels are Go code. The
  [kernel evidence report](reports/go-simd-kernel-evidence.md) tracks all 53
  replacements, same-host assembly comparisons, allocations, and validated
  parity coverage.

Native AMD64 end-to-end measurements in early artifact `11009635111` compare
assembly `8ac93c85` with Go `c6dfb561` on an AMD EPYC 9V74, Go 1.27.1, GCC
13.3.0, GOAMD64=v1, and PGO. Four interleaved 500 ms samples use `-cpu=1`;
all 72 benchmark samples report zero allocations. Values are median ns/op.
All 96 early phase exit records have status 0, with no JSON test failures.
The [complete CI run](https://github.com/thesyncim/gopus/actions/runs/36506668630)
passes, including the full native AMD64 A/B gate. See the
[evidence report](reports/go-simd-kernel-evidence.md) for coverage and archived
diagnostic evidence.

| Workload | Old assembly | Go SIMD | `nosimd` |
|---|---:|---:|---:|
| CELT decode | 20,222.5 | 13,446 | 17,080 |
| Hybrid decode | 29,924 | 26,080 | 32,961 |
| SILK decode | 23,579.5 | 16,890 | 21,827.5 |
| Caller-buffer encode | 93,138 | 61,203 | 104,182.5 |
| VoIP encode | 99,290.5 | 66,263.5 | 109,582 |
| Low-delay encode | 92,213 | 61,247.5 | 103,673.5 |

Compare old assembly and Go variants within this artifact. Absolute timings from
separate early artifacts are not source comparisons.

The paired C comparison uses identical inputs and controls, pairing scalar Go
with scalar C and SIMD Go with SIMD C. The paired results use early artifact
`11009635111`, candidate `c6dfb561`, and the same EPYC 9V74 runner and
toolchain. Each C/Go case has three 250 ms minimum runs. Times are µs per
packet. Each paired Go benchmark row reports zero allocations; C allocation
counts are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| CELT-FB-20ms-stereo-128k | 197.34 | 191.63 | 145.71 | 123.58 |
| CELT-FB-5ms-mono-64k | 21.59 | 23.66 | 20.36 | 19.71 |
| Hybrid-FB-20ms-mono-64k | 395.42 | 376.56 | 267.38 | 221.67 |
| Hybrid-FB-20ms-stereo-96k | 226.37 | 223.36 | 170.36 | 139.82 |
| SILK-WB-20ms-mono-32k | 762.47 | 669.81 | 421.35 | 321.99 |
| RFC vectors Float32 | 32.85 | 34.01 | 31.01 | 28.27 |
| RFC vectors Int16 | 36.82 | 37.34 | 33.82 | 32.38 |

These measurements are workload-specific. Decoder rows aggregate 20,075
identical packets; encoder timings do not establish long-stream packet parity.
The [evidence report](reports/go-simd-kernel-evidence.md) records the early
validation scope and the full A/B status.

The opt-in compiler-target benchmark compares `GOAMD64=v1`, `v2`, and `v3` on one
native host; routine PR CI does not run this matrix. Each target pairs Go with
separately built scalar/SIMD libopus using the matching C compiler baseline.
These are compiler targets; SIMD dispatch
still uses the runner's supported AVX2/FMA kernels. Exact packet, range, PCM,
dispatch, and allocation checks precede timings. The matrix covers the default
float codec; optional-feature evidence and the 53-routine inventory retain their
recorded targets. See [compiler-target methodology](reports/go-simd-kernel-evidence.md#amd64-compiler-targets).
The recorded v1/v2 selection passes; v3 has open packet and PCM mismatches,
so v3 exactness and performance are not claimed.

AMD64 SIMD benchmarks use `GOAMD64=v3` on a CPU that supports that target.
The published tables above retain their measured v1 provenance; replacement
v3 tables require validation against the [parity target](reports/parity-target.md)
and fresh matched-C measurements. Unclassified mismatches still block publication.

```sh
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-encode
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-decode
```

`GOPUS_LIBOPUS_AMD64_TARGET=v3` selects the matching C compiler target.
For a matching scalar comparison, keep both target settings and use
`GOEXPERIMENT=nosimd`. On ARM64, omit both target settings and select the
same SIMD or scalar experiment for both benchmark commands.

`make bench-guard` runs the benchmark guardrails used in CI.

## Parity & testing

The [parity target](reports/parity-target.md) requires exact API/protocol behavior
and integer primitives on identical inputs, matching decoder entropy ranges for
identical packets, and zero steady-state allocations. Passing byte-exact coverage remains protected.
Floating-point differences may be accepted only with a proven rounding cause,
a narrow tested bound and unchanged quality/recovery gates. Universal packet
identity across compiler targets is not a release requirement; unexplained
mismatches remain failures. Current v3 differences are not yet accepted.

gopus implements the core public API and the optional surfaces mirrored by the
build tags above. Exact gates compare packets, final ranges, sample counts and
PCM bits against pinned libopus 1.6.1 with matching feature flags, CPU dispatch,
inputs and controls. Parity claims refer to the [recorded test coverage](reports/parity-evidence-audit.md).
The documented oracle exception is libopus's undefined stateful custom-QEXT
history access at 96 kHz / 2048 samples. gopus keeps history access bounded and
uses safe concealment; defined first-frame behavior remains compared with C, and
the subsequent unsafe C output is excluded. See the [boundary report](reports/libopus-custom-qext-boundaries.md).

The [public API boundary audit](reports/parity-evidence-audit.md#public-api-boundary-audit)
covers control failure state, native 96 kHz limits, malformed packet errors and
fixed-point analysis thresholds. Its eight local ARM64 feature/ISA lanes pass;
native AMD64 validation of this additional patch is pending.

The latest recorded [required-CI pass](https://github.com/thesyncim/gopus/actions/runs/36514746209)
completed on snapshot `d9fba62b`. The measured runtime snapshot at `c6dfb561` has 96
successful exit records in early artifact `11009635111`; full artifact
`11009623177` includes its passing SIMD package sweep. The neural
allocation guard passes all 12 feature/ISA combinations; FARGAN, PLC-feature,
`SinF32`, and LACE/NoLACE exactness checks pass their paired matrices.

DRED history and root/multistream OSCE automatic loss/recovery matrices pass
all eight applicable local feature/ISA lanes at `b29fcff7`. Mixed LBRR, outer
PLC prefixes, tiny FEC payloads, model reload and complexity-change history
pass exact combined-feature scalar/SIMD checks, including warm allocation gates. QEXT non-fullband refinement, SILK comfort-noise history
and pitch state across rate changes pass their exact local regressions. The strict 8,000-case malformed-FEC sweep passes six
local feature/ISA lanes; the full custom package passes all eight. Native 96 kHz encoder mode/budget sequences, including
40 ms packets and QEXT off/on, pass exact packet/range and warm zero-allocation
checks in all four local float/fixed scalar/SIMD lanes. Native 96 kHz multistream encode/decode, long-burst PLC,
Hybrid QEXT routing, and discarded extension bands pass their matching local
feature/ISA matrices. Malformed Hybrid main-length and projection full-output
gates pass all eight local lanes. QEXT mono/stereo multiframe reconstruction
passes independent exact C checks. The strict long-stream matrix
passes 13 configurations × 2,500 frames in both local ARM64 instruction lanes.
The [evidence report](reports/go-simd-kernel-evidence.md)
records the passing matrices, active investigations, and the documented unsafe
libopus custom-QEXT boundary. Passing short matrices does not close these cases.

Validation uses two tiers against a live libopus C oracle:

- **Bit-exact kernel and public-path oracles.** Covered kernels (range coder,
  NLSF/LPC/gain, PVQ/bands, MDCT/KISS-FFT, resamplers, and DNN math) are checked
  against C. Public encoder and decoder tests compare packet, range, or PCM
  results for explicit input and state matrices. This coverage is broad but does
  not prove every possible input and state sequence.
- **`opus_compare` quality on real audio.** End-to-end audio is also measured
  with libopus's `opus_compare` (RFC 8251's conformance metric). This quality
  check complements exact packet, range, and PCM tests; it does not replace them.

Parity tests pair Go and C by feature configuration and instruction lane, using
identical inputs and controls. Strict packet gates fail on every byte or
final-range difference. The complete feature/build/input matrix remains
under validation; the
[kernel evidence report](reports/go-simd-kernel-evidence.md) records tested
revisions, measurements, and remaining work. Comparing Go SIMD with scalar C,
or scalar Go with SIMD C, does not establish parity for either lane.

Pre-v1: latest release is [v0.1.2](https://github.com/thesyncim/gopus/releases/tag/v0.1.2)
(see [Trust And Verification](#trust-and-verification)).

## Verification

Run focused tests while iterating. Before merge-ready codec changes, run:

```sh
go test ./...
make test-doc-contract
make lint
make test-consumer-smoke
make test-examples-smoke
make verify-production
```

```sh
make verify-production-exhaustive
make release-evidence
```

Before a tag is published the tagged commit must be green on the required branch
checks (below), and `make release-evidence` must produce a PASS summary.

## Trust And Verification

Released version: `v0.1.2`.

`v0.1.0` was retracted: it was tagged but its GitHub Release never published.

Latest release evidence: attached to the [v0.1.2 release](https://github.com/thesyncim/gopus/releases/tag/v0.1.2).

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

These aggregate gates make the libopus C-oracle parity suites mandatory across
Linux, macOS, and Windows; each lane builds the pinned libopus C reference under
`GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1` with matching
feature flags and instruction support, then compares identical inputs and
controls. They are the authoritative codec gate:
`release.yml` publishes a tag only after verifying these checks are green on the
tagged commit. `make release-evidence` then captures the supplementary safety and
performance gates that are not in the required CI set, plus build provenance.

Security policy: [SECURITY.md](SECURITY.md). Consumer smoke test:
[examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go).

## Docs

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [examples/README.md](examples/README.md)

## License

See [LICENSE](LICENSE).
