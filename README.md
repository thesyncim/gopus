# gopus

An Opus audio codec in pure Go, with no cgo. gopus implements RFC 6716 and
RFC 8251 and targets strong behavioral and audio-quality parity with libopus
1.6.1. It includes encoding, decoding, multistream, projection/ambisonics,
Ogg files and RTP RED recovery.

Caller-buffer APIs reuse storage and have zero steady-state allocations in the
covered encode, decode and container paths. All codec kernels are Go code;
Go 1.27's experimental SIMD support is optional.

## Install

Requires Go 1.27 or newer:

```sh
go get github.com/thesyncim/gopus
```

## Quick start

Encode and decode a 20 ms stereo frame at 48 kHz:

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
		frameSize  = 960 // samples per channel
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

	pcm := make([]float32, frameSize*channels) // interleaved input, normally [-1, 1]
	packet := make([]byte, 1500)               // reusable packet storage
	out := make([]float32, frameSize*channels) // storage for this 20 ms frame

	n, err := enc.Encode(pcm, packet) // bytes written
	if err != nil {
		log.Fatal(err)
	}
	samples, err := dec.Decode(packet[:n], out) // samples per channel
	if err != nil {
		log.Fatal(err)
	}
	_ = out[:samples*channels]
}
```

For arbitrary incoming packets, size the output for
`DecoderConfig.MaxPacketSamples * Channels`. The default reserves 120 ms
(5,760 samples per channel at 48 kHz) and accepts packets up to 1,500 bytes.
Reuse codec instances and buffers; construction and initial warmup can allocate.
Each encoder or decoder maintains stream history and needs external
synchronization if shared between goroutines.

`EncodeInt16` / `DecodeInt16` use `[]int16`; `EncodeInt24` / `DecodeInt24`
use signed 24-bit samples in `[]int32`. All formats use interleaved channels.
Pass a nil packet to `Decode` for packet-loss concealment. Use `DecodeWithFEC`
with the following packet for in-band recovery; see the
[examples](examples/README.md) for loss handling, streaming, Ogg and RED.

Configure bitrate, VBR, complexity, FEC and DTX with the encoder's control methods.
`NewEncoder` starts at 64 kbps; libopus starts with automatic bitrate selection.
Set matching controls when comparing output. `Bitrate()` returns the configured
target, including `BitrateAuto` and `BitrateMax`; libopus reports an effective
bitrate instead.

## Packages and features

| Package | API |
|---|---|
| [gopus](https://pkg.go.dev/github.com/thesyncim/gopus) | Single-stream codec, streaming reader/writer, multistream facade, packet parsing, repacketizer and controls |
| [multistream](https://pkg.go.dev/github.com/thesyncim/gopus/multistream) | Multistream codec and projection/ambisonics |
| [container/ogg](https://pkg.go.dev/github.com/thesyncim/gopus/container/ogg) | Ogg Opus reading and writing (RFC 7845) |
| [container/red](https://pkg.go.dev/github.com/thesyncim/gopus/container/red) | RTP RED payload construction, parsing and recovery (RFC 2198) |
| [types](https://pkg.go.dev/github.com/thesyncim/gopus/types) | Shared mode, bandwidth and signal enums |

| Feature | Support |
|---|---|
| Modes | SILK, CELT and Hybrid, with automatic selection |
| Sample rates | 8, 12, 16, 24 and 48 kHz; native 96 kHz with `gopus_qext` |
| Channels | Mono, stereo, multistream and projection/ambisonics |
| Frame durations | 2.5, 5, 10, 20, 40, 60, 80, 100 and 120 ms, subject to mode constraints |
| Rate control | CBR, VBR, constrained VBR, low-delay and DTX |
| Recovery | Packet-loss concealment and in-band FEC/LBRR |
| PCM | Float32, int16 and int24, including multistream |

### Build configuration

Ordinary builds use scalar Go kernels. `GOEXPERIMENT=simd` compiles
`simd/archsimd` kernels where implemented; runtime CPU checks select supported
kernels and the rest use scalar code. `-tags nosimd` or `-tags purego` forces
the scalar path, including the matching scalar C reference in oracle tests.

Optional features mirror libopus build flags and are excluded from the default
build's import graph:

| Go build tag | libopus flag | Feature |
|---|---|---|
| `gopus_dred` | `--enable-dred` | DRED controls and standalone recovery |
| `gopus_osce` | `--enable-osce` (+ `ENABLE_DEEP_PLC`) | OSCE BWE/LACE/NoLACE and deep PLC |
| `gopus_qext` | `--enable-qext` | QEXT and native 96 kHz |
| `gopus_custom_modes` | `--enable-custom-modes` | Opus Custom modes |
| `gopus_fixed_point` | `--enable-fixed-point` | Integer CELT/SILK pipeline |

Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op
returning `ErrOptionalExtensionUnavailable`. DNN model loading follows libopus's
`USE_WEIGHTS_FILE` configuration. A build tag enables an implementation; it does
not establish parity for every feature, architecture or packet sequence.
Libopus rejects fixed-point combined with DRED/OSCE; those combinations have no
matching supported C reference lane.

| Extension | Availability | Probe |
|---|---|---|
| DNN blob loading | Available under `gopus_dred` / `gopus_osce` | `OptionalExtensionDNNBlob` |
| QEXT | Available under `gopus_qext` | `OptionalExtensionQEXT` |
| DRED | Available under `gopus_dred` (control + standalone) | `OptionalExtensionDRED` |
| OSCE BWE | Extra controls under `gopus_osce`; support probe returns false | `OptionalExtensionOSCEBWE` |

`SupportsOptionalExtension(OptionalExtensionOSCEBWE)` reports false: these
controls are exposed for parity work. Consult the [validation reference](reports/validation.md#coverage)
for tested feature combinations. Run tagged tests with the matching reference:

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_osce ./...
```

## Performance

The table pairs **C scalar with Go scalar** and **C SIMD with Go SIMD**, using
identical inputs and controls. Values are median microseconds per packet on an
AMD EPYC 9V74, Go 1.27.1, GCC 13.3.0, **GOAMD64=v1**, with PGO, at `c6dfb561`.
Each case has three runs of at least 250 ms. Go reports zero allocations;
C allocation counts are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 197.34 | 191.63 | 145.71 | 123.58 |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 21.59 | 23.66 | 20.36 | 19.71 |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 395.42 | 376.56 | 267.38 | 221.67 |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 226.37 | 223.36 | 170.36 | 139.82 |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 762.47 | 669.81 | 421.35 | 321.99 |
| Decode RFC vectors, float32 | 32.85 | 34.01 | 31.01 | 28.27 |
| Decode RFC vectors, int16 | 36.82 | 37.34 | 33.82 | 32.38 |

Decoder rows aggregate 20,075 identical packets. These results are specific to
the workloads and measured revision. The [performance reference](reports/validation.md#performance)
contains all **53 replacement routines**, assembly/Go/`nosimd` comparisons,
allocation results and artifact provenance from the
[passing native run](https://github.com/thesyncim/gopus/actions/runs/36506668630).

For new AMD64 SIMD measurements, use **GOAMD64=v3** on a supporting CPU and
select the same C compiler target:

```sh
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-encode
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-decode
```

For scalar comparisons, retain both target settings and use `GOEXPERIMENT=nosimd`.
On ARM64, omit both AMD64 target settings. The published numbers retain their
measured v1 provenance: v3 encoder packet parity remains unresolved, so fresh
v3 tables require correctness validation before publication. The optional
[v1/v2/v3 audit](reports/validation.md#amd64-compiler-targets) runs on one native host;
routine PR CI does not run that matrix.

## Parity & testing

The [parity contract](reports/validation.md#parity-contract) requires exact
API/protocol behavior and integer arithmetic, matching entropy ranges for
identical packets and history, and preservation of established exact regressions.
C and Go use the same features, controls, scalar widths and effective CPU
instructions. SIMD Go versus scalar C is not a parity comparison.

Floating-point allowances require a reproducible rounding cause, a narrow
kernel-specific bound, independent quality/recovery evidence and a reviewed
executable regression. Unexplained mismatches remain failures. Exact oracle
checks and real-audio `opus_compare` quality checks complement each other;
neither proves every possible input and state sequence.

The [coverage audit](reports/validation.md#coverage) records passing configurations
and unresolved cases. Universal byte parity is not claimed. The documented
[C reference boundary](reports/validation.md#reference-boundary) is an unsafe
custom-QEXT history read at 96 kHz / 2,048 samples; Go uses bounded concealment
and compares defined C behavior.

Use `make test-fast` for iteration, `make test` for the live C-oracle suite,
and `make test-doc-contract` for documentation contracts. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the verification and release checklist.

## Trust And Verification

Released version: `v0.1.2`. The API is pre-v1.

`v0.1.0` was retracted: its GitHub Release never published.
Latest release evidence: attached to the
[v0.1.2 release](https://github.com/thesyncim/gopus/releases/tag/v0.1.2).

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

These checks build the pinned C reference with matching features and instructions
and run mandatory parity suites on Linux, macOS and Windows. The release workflow
requires green checks on the tagged commit; `make release-evidence` must also
produce a PASS summary with safety, performance and build provenance.

Security reports: [SECURITY.md](SECURITY.md). External API verification:
[examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go).
Contributions follow [CONTRIBUTING.md](CONTRIBUTING.md) and the
[code of conduct](CODE_OF_CONDUCT.md). See [LICENSE](LICENSE) for licensing.
