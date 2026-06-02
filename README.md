# gopus

Pure-Go Opus codec — RFC 6716 / RFC 8251, bit-exact and quality parity with
pinned libopus 1.6.1, drop-in for the C library.

No cgo, no dependencies. Encoder, decoder, multistream, projection/ambisonics,
Ogg and RTP RED — with caller-owned, zero-allocation hot paths.

## Install

```sh
go get github.com/thesyncim/gopus
```

Requires Go 1.25+.

## Quick start

The hot-path API takes caller-owned buffers and returns the number of bytes /
samples written, so the encode and decode loops allocate nothing:

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

See [examples/](examples/) for Ogg files, ffmpeg interop, RED loss recovery,
WebRTC control, and benchmarks.

## Features

- SILK, CELT, and Hybrid coding, with automatic mode selection.
- Sample rates 8, 12, 16, 24, and 48 kHz (native sub-48 kHz encode, no upsampling).
- Mono, stereo, multistream, and projection/ambisonics
  (`NewProjectionEncoder` / `NewProjectionDecoder`, mapping families 0/1/3/255).
- Frame sizes 2.5–120 ms.
- CBR, VBR, CVBR, low-delay, and DTX.
- Packet loss concealment, in-band FEC / LBRR.
- `container/ogg` (Ogg read/write) and `container/red` (RFC 2198 RTP RED
  parse / build / recover).
- `float32`, `int16`, and `int24` PCM (`EncodeInt24` / `DecodeInt24`) on both the
  single-stream and multistream paths.
- The full libopus public surface: 50 CTLs, packet parsing, soft clipping, and
  matching error codes.

## Optional features behind build tags

The default build is core encode/decode/multistream/Ogg/RED — matching a default
libopus `./configure`. Optional features are exposed exactly the way libopus
exposes them: behind a compile flag in libopus, behind the matching Go build tag
here. The default build links ZERO of their code (enforced by
`TestDefaultBuildIsZeroCostForGatedFeatures`).

| gopus build tag | libopus flag |
| --- | --- |
| `gopus_dred` | `--enable-dred` |
| `gopus_extra_controls` | `--enable-osce` (+ `ENABLE_DEEP_PLC`) |
| `gopus_qext` | `--enable-qext` |
| `gopus_custom` | `--enable-custom-modes` |
| `gopus_fixedpoint` | `--enable-fixed-point` |

Under their tag these are parity-complete — none are experimental:

- **`gopus_dred`** — DRED (RDOVAE), control + standalone surfaces.
- **`gopus_extra_controls`** — OSCE BWE / LACE / NoLACE plus the deep-PLC family
  (PitchDNN / FARGAN), exactly as `--enable-osce`.
- **`gopus_qext`** — QEXT framing and native 96 kHz (Opus HD): decode is
  sample-exact, and the public `Encode` at `Fs=96000` is byte-exact (TOC, padding,
  main CELT payload, reserved QEXT extension) vs libopus `--enable-qext`. 96 kHz is
  CELT-only fullband (mirroring libopus) and accepted only under this tag;
  default-build API rates stay 8/12/16/24/48 kHz.
- **`gopus_custom`** — Opus Custom standard modes.
- **`gopus_fixedpoint`** — integer CELT/SILK pipeline (libopus `FIXED_POINT`);
  public decode and encode are bit-exact vs the `--enable-fixed-point` oracle.

Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op
returning `ErrOptionalExtensionUnavailable`. This matches a default libopus build,
where the DNN / PitchDNN / FARGAN / RDOVAE neural code is empty and none of it is
compiled; gopus keeps those packages out of the default import graph. DNN blob
loading (USE_WEIGHTS_FILE model loading) requires `-tags gopus_dred` or
`-tags gopus_extra_controls`; QEXT requires `-tags gopus_qext`; DRED
control/standalone surfaces require `-tags gopus_dred`; OSCE BWE/LACE/NoLACE
require `-tags gopus_extra_controls`. Under their build tag these are
parity-complete and supported, exactly as libopus exposes them behind the
corresponding compile flag.

| Extension | Status | Probe |
| --- | --- | --- |
| DNN blob loading | Supported under `gopus_dred` / `gopus_extra_controls` | `OptionalExtensionDNNBlob` |
| QEXT | Supported under `gopus_qext` | `OptionalExtensionQEXT` |
| DRED | Supported under `gopus_dred` (control + standalone) | `OptionalExtensionDRED` |
| OSCE BWE | Supported under `gopus_extra_controls` | `OptionalExtensionOSCEBWE` |

The `gopus_extra_controls` tag enables the OSCE and deep-PLC family exactly as
libopus's `--enable-osce` does. These features are supported under the tag and
link zero code into the default build.

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_extra_controls ./...
```

```sh
make test-dnn-blob-parity
make test-qext-parity
make test-dred-tag
make test-extra-controls-parity
make test-custom-parity
```

## Status / parity

gopus is codec-complete against libopus 1.6.1: the full public API and 50 CTLs,
plus the optional surface mirrored tag-for-flag (above). The pinned
`tmp_check/opus-1.6.1/` is the reference — when behavior is uncertain, gopus
matches libopus unless fixture evidence says otherwise.

Parity is proven on two tiers: isolated kernels (range coder, NLSF/LPC/gain,
PVQ/bands, MDCT/KISS-FFT, resamplers, DNN matmuls) are compared **bit-for-bit**
against a live libopus C oracle, and every public decode entry point is two-sided
differential-fuzzed against it. End-to-end audio is judged by libopus's own
`opus_compare` (RFC 8251's conformance metric), tier-matched so gopus tracks the
reference at least as closely as libopus tracks itself across builds. SILK decode
is bit-exact; CELT/Hybrid sit inside the near-exact envelope.

One residual is documented: a few CELT float kernels drift by ≤1 ULP on
darwin/arm64 (a per-arch float budget). amd64/CI is bit-exact; the default arm64
build is quality-gated for that tail, exactly as libopus's NEON path is relative
to its own scalar build.

Pre-v1: no release tagged yet (see below).

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

`make release-evidence` must produce a PASS summary before a tag is published.

## Trust And Verification

Released version: none yet.

`v0.1.0` is not a release until the tag and GitHub Release are both published.

Latest release evidence: none yet.

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

These aggregate gates make the libopus C-oracle parity suites mandatory across
platforms: the core float numeric oracle (`make test-core-oracles-parity`) runs
on Linux, macOS, and Windows; the tagged DRED and `--enable-fixed-point` oracle
gates (`make test-dred-tag`, `make test-fixedpoint-parity`) run on Linux and
macOS; the QEXT (`make test-qext-parity`), Opus Custom `--enable-custom-modes`
(`make test-custom-parity`), extended corpus signal-quality
(`make test-corpus-quality`), and extra-controls oracle gates run on Linux. Each
lane builds the pinned libopus C reference first under
`GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1`.

Release checklist:

- select a `vMAJOR.MINOR.PATCH` tag
- confirm README and package docs agree
- run the verification commands
- attach release evidence summary and archive
- publish the tag and GitHub Release together

Supply-chain controls:

- Dependabot is enabled for GitHub Actions and Go modules.
- OpenSSF Scorecard runs on `master`, weekly, and by manual dispatch.
- Workflow permissions are least-privilege.
- Release evidence records commit SHA, Go version, platform, pinned libopus
  version and SHA256, command logs, benchmark guardrails, fuzz/safety summary,
  parity summary, and module inventory.
- Future binary releases need signed checksums, provenance, and an SPDX or CycloneDX SBOM.

Security reports: [SECURITY.md](SECURITY.md).
Consumer smoke test: [examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go).

## Docs

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [examples/README.md](examples/README.md)

## License

See [LICENSE](LICENSE).
