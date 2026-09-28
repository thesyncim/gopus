# gopus

Pure-Go Opus codec — RFC 6716 / RFC 8251, targeting byte and quality parity
with pinned libopus 1.6.1, with no cgo.

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

See [examples/](examples/) for Ogg files, ffmpeg interop, RED loss recovery,
WebRTC control, and benchmarks.

## Features

| Area | gopus |
| --- | --- |
| Coding modes | SILK, CELT, Hybrid, with automatic mode selection |
| Sample rates | 8, 12, 16, 24, 48 kHz by default; native 96 kHz CELT with `gopus_qext` |
| Channels | Mono, stereo, multistream, projection / ambisonics |
| Frame sizes | 2.5–120 ms |
| Bitrate control | CBR, VBR, CVBR, low-delay, DTX |
| Resilience | Packet loss concealment, in-band FEC / LBRR |
| PCM formats | `float32`, `int16`, `int24` (single-stream and multistream) |
| Containers | `container/ogg` (Ogg read/write), `container/red` (RFC 2198 RTP RED parse/build/recover) |
| libopus surface | Full public API: the libopus CTL surface, packet parsing, soft clipping, and matching error codes |

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

The default build is core encode/decode/multistream/Ogg/RED — matching a default
libopus `./configure`. Optional features are exposed exactly the way libopus
exposes them: behind a compile flag in libopus, behind the matching Go build tag
here. The default build links ZERO of their code (enforced by
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
- **`gopus_qext`** — QEXT framing and native 96 kHz (Opus HD). Native 96 kHz is
  CELT-only fullband and is available only under this tag; default-build API
  rates stay 8/12/16/24/48 kHz. Paired C tests cover selected encoder, decoder,
  and loss cases; the complete QEXT feature/build matrix is still under review.
- **`gopus_custom_modes`** — Opus Custom standard modes.
- **`gopus_fixed_point`** — integer CELT/SILK pipeline (libopus `FIXED_POINT`).
  Paired scalar/SIMD oracles cover integer kernels, packet encoding, public
  decode formats, multistream, and mode transitions. The full feature and
  architecture matrix is still being validated.

One more tag is orthogonal to the feature flags above and has no libopus
equivalent:

- **`nosimd`** — forces the scalar Go reference path, including when
  `GOEXPERIMENT=simd` is set. Ordinary builds use this scalar path. Set
  `GOEXPERIMENT=simd` to select Go `archsimd` kernels where they are implemented;
  other kernels keep the scalar fallback. Compare this path with scalar libopus
  and the SIMD path with libopus using matching CPU instructions.

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
- **Go SIMD.** `GOEXPERIMENT=simd` enables `simd/archsimd` kernels with CPU
  feature dispatch. All codec kernels are Go code. The
  [kernel evidence report](reports/go-simd-kernel-evidence.md) tracks all 53
  replacements, same-host assembly comparisons, allocations, and unresolved
  parity differences.

The latest recorded AMD64 end-to-end run compares Go with the previous assembly
baseline on the same workload, using an AMD EPYC 7763, Go 1.27.1, GCC 13.3,
GOAMD64=v1, and PGO. Four interleaved 500 ms samples report zero allocations
for all rows. Values are median ns/op.

| Workload | Old assembly | Go SIMD | `nosimd` |
| --- | ---: | ---: | ---: |
| CELT decode | 20,281.5 | 13,545 | 16,668 |
| Hybrid decode | 28,442.5 | 24,367 | 30,701 |
| SILK decode | 22,479 | 16,460 | 21,222.5 |
| Caller-buffer encode | 92,144.5 | 64,915.5 | 100,802 |
| VoIP encode | 98,179.5 | 70,047 | 106,786 |
| Low-delay encode | 91,436.5 | 64,768.5 | 100,178 |

The separate paired C comparison uses identical inputs and controls, pairing
scalar Go with scalar C and SIMD Go with SIMD C. It comes from [run
36444790749](https://github.com/thesyncim/gopus/actions/runs/36444790749) at
revision `d9d2c07a`, on the same runner and toolchain. Each case has three 250 ms
minimum runs. Times are µs per packet. Go rows report zero allocations; C
allocation counts are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
| --- | ---: | ---: | ---: | ---: |
| CELT-FB-20ms-stereo-128k encode | 179.80 | 191.93 | 134.29 | 130.40 |
| CELT-FB-5ms-mono-64k encode | 19.92 | 23.62 | 18.71 | 19.98 |
| Hybrid-FB-20ms-mono-64k encode | 358.50 | 358.10 | 254.68 | 281.42 |
| Hybrid-FB-20ms-stereo-96k encode | 203.08 | 219.22 | 151.69 | 142.33 |
| SILK-WB-20ms-mono-32k encode | 687.66 | 615.31 | 449.19 | 387.32 |
| RFC vectors Float32 decode | 30.78 | 32.98 | 28.97 | 27.88 |
| RFC vectors Int16 decode | 33.97 | 36.42 | 31.47 | 31.29 |

These measurements are workload-specific. The early artifact reports BWE mono
and stereo passing, with five LACE/NoLACE sample cases still mismatching across
OSCE feature builds. Those exactness issues are under investigation; these
base-codec performance measurements do not establish OSCE parity.

Run the benchmarks for numbers on your machine:

```sh
go run ./examples/bench-encode
go run ./examples/bench-decode
```

`make bench-guard` runs the benchmark guardrails used in CI.

## Parity & testing

gopus implements the core public API and the optional surfaces mirrored by the
build tags above. Full byte- and sample-parity across every feature,
architecture, input format, control sequence, and packet mode is not yet
proven. The pinned `tmp_check/opus-1.6.1/` is the reference; when behavior is
uncertain, gopus matches libopus unless fixture evidence says otherwise.

Open validation cases include long-running SILK CBR packet equality, mono QEXT
multi-frame reconstruction, native amd64 LACE/NoLACE output, Hybrid packet-loss
recovery, and fixed-point malformed multistream output. The [evidence report](reports/go-simd-kernel-evidence.md)
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
final-range difference. Five LACE/NoLACE sample mismatches remain in the early
native AMD64 OSCE artifact, and the complete feature/build/input matrix remains
under validation; the
[kernel evidence report](reports/go-simd-kernel-evidence.md) records tested
revisions, measurements, and remaining work. Comparing Go SIMD with scalar C,
or scalar Go with SIMD C, does not establish parity for either lane.

Pre-v1: latest release is `v0.1.1` (see [Trust And Verification](#trust-and-verification)).

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

Released version: `v0.1.1`.

`v0.1.0` was retracted: it was tagged but its GitHub Release never published.

Latest release evidence: attached to the [`v0.1.1` release](https://github.com/thesyncim/gopus/releases/tag/v0.1.1).

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
