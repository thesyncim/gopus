# gopus

An Opus audio codec in pure Go, with no cgo. gopus implements RFC 6716 and
RFC 8251 and targets strong behavioral and audio-quality parity with libopus
1.6.1. It includes encoding, decoding, multistream, projection/ambisonics,
Ogg files and RTP RED recovery.

The caller-buffer APIs reuse storage and keep the covered encode, decode and
container hot paths allocation-free after warmup. All codec kernels are Go code;
Go 1.27's experimental SIMD support is optional.

- [API reference](https://pkg.go.dev/github.com/thesyncim/gopus)
- [Runnable examples](examples/README.md)
- [Correctness and performance evidence](reports/validation.md)

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
	"fmt"
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
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}

	pcm := make([]float32, frameSize*channels) // interleaved input, normally [-1, 1]
	packet := make([]byte, cfg.MaxPacketBytes)
	out := make([]float32, cfg.MaxPacketSamples*channels)

	n, err := enc.Encode(pcm, packet) // bytes written
	if err != nil {
		log.Fatal(err)
	}
	samples, err := dec.Decode(packet[:n], out) // samples per channel
	if err != nil {
		log.Fatal(err)
	}
	decoded := out[:samples*channels]
	fmt.Printf("decoded %d samples per channel (%d total)\n", samples, len(decoded))
}
```

### Buffers, frames, and state

PCM channels are interleaved. A frame size counts samples **per channel**, so
20 ms at 48 kHz is 960 samples per channel or 1,920 values for stereo. `Encode`
returns packet bytes; `Decode` returns samples per channel. Use `packet[:n]`
for the encoded packet and `out[:samples*channels]` for the decoded PCM.

`Encoder.FrameSize()` determines the required input length. `len(packet)` sets
the encode byte budget. For arbitrary incoming packets, allocate output for
`DecoderConfig.MaxPacketSamples * Channels`. The default limits are **5,760
samples per channel** and **1,500 packet bytes**; 5,760 samples is 120 ms at
48 kHz. Set those limits explicitly when your stream needs larger buffers,
including 120 ms packets at native 96 kHz.

Reuse each codec instance and its buffers. Construction and initial warmup can
allocate. Instances retain stream history and require external synchronization
if shared between goroutines. Use one instance per independent stream. `Reset`
clears stream history and retains the stream format and ordinary controls.
Encoder reset also disables DRED emission in tagged builds.

`EncodeInt16` and `DecodeInt16` use `[]int16`. `EncodeInt24` takes
right-justified signed 24-bit samples in `[]int32`, in the range
[-8,388,608, 8,388,607]. `DecodeInt24` writes into a caller-provided `[]int32`
buffer at the same PCM scale; it does not clamp to that range, so output gain
can produce larger values.
All formats use the same interleaved layout.

### Packet loss

Pass an empty or nil packet to `Decode` for packet-loss concealment. To request
one missing 20 ms stereo frame at 48 kHz, pass `out[:960*2]`. A buffer whose
length equals `MaxPacketSamples * Channels` instead requests the last decoded
packet's duration when available.

When the following packet arrives, `DecodeWithFEC(packet, missingPCM, true)`
recovers the missing audio from in-band FEC when available and otherwise
conceals the loss. Then call `Decode(packet, out)` on the **same packet** for its
primary audio. At native 96 kHz, a FEC request always uses concealment and
ignores the supplied packet. The [packet-loss example](examples/packet-loss) demonstrates this
ordering. Transport framing, packet timing, and jitter buffering belong to the
application; the codec processes the packets supplied to it.

### Encoder controls

Configure bitrate, VBR, complexity, FEC, and DTX with the encoder's control
methods. `NewEncoder` starts at 64 kbps; libopus starts with automatic bitrate
selection. Set matching controls when comparing output. `Bitrate()` returns the
configured target, including `BitrateAuto` and `BitrateMax`; libopus reports an
effective bitrate instead.

DTX can produce a short one- or two-byte packet, or no packet. A zero encode
byte count with a nil error means there is no packet to send.

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
controls are exposed for parity work. DRED controls and standalone recovery
APIs also compile with `gopus_osce`, while the supported DRED probe follows
`gopus_dred`. Consult the [validation reference](reports/validation.md#coverage)
for tested feature combinations. Run tagged tests with the matching reference:

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_osce ./...
```

## Performance

The table pairs **C scalar with Go scalar** and **C SIMD with Go SIMD**, using
identical inputs and controls. Values are median **ns/sample per channel**
(lower is faster) on AMD EPYC 7763, Go 1.27.1 and GCC 13.3.0, with
**GOAMD64=v3**, PGO and candidate `4b660d668`. Each case has three runs of
at least 250 ms. Go reports zero allocations; C allocations are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 176.89 | 197.94 | 129.50 | 126.28 |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 75.99 | 93.79 | 69.83 | 80.00 |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 705.61 | 685.98 | 462.01 | 329.21 |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 363.00 | 420.99 | 253.40 | 225.80 |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 200.37 | 227.97 | 145.10 | 139.57 |
| Decode RFC vectors, float32 | 38.22 | 42.29 | 35.90 | 33.14 |
| Decode RFC vectors, int16 | 42.23 | 46.80 | 38.83 | 37.64 |

Decoder rows aggregate 20,075 identical packets. Go SIMD takes 2.5–28.7% less time
than matched C SIMD in four encode workloads and 14.6% more for 5 ms CELT.
Float32 vector decode takes 7.7% less time; int16 takes 3.1% less time.
These results describe the measured revision and workloads. A same-host
[incremental benchmark](https://github.com/thesyncim/gopus/actions/runs/36870981432)
of the v3 parity fixes (`4b660d668` → `bce51d66f`) on AMD EPYC 9V74 changes
public encoder time by −0.35% to +0.42%, with zero allocations in all 80 timing
samples. These are paired measurements on a separate host; they do not rescale
the EPYC 7763 tables. The
[passing native benchmark](https://github.com/thesyncim/gopus/actions/runs/36852328909) records all targets and raw samples. The
[performance reference](reports/validation.md#performance) contains all **53
replacement routines**, assembly/Go/`nosimd` comparisons, allocations and
per-row provenance.

Use **GOAMD64=v3** on a supporting CPU and select the same C compiler target:

```sh
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-encode
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./examples/bench-decode
```

For scalar comparisons, retain both target settings and use `GOEXPERIMENT=nosimd`.
On ARM64, omit both AMD64 target settings. The [native v3 audit](https://github.com/thesyncim/gopus/actions/runs/36870981432)
at `bce51d66f` passes both matched lanes: 19 CBR cases and 2,175 packets/ranges,
60 encoder, 24 decoder, 15 allocation and 200 analyzer corpus/variant cases.
All 11 stereo-width cases and all 51 sidecar packets/ranges also agree with
matching live C. [Current evidence](reports/validation.md#analyzer-history-and-getter-on-amd64-v3).
The optional [v1/v2/v3 audit](reports/validation.md#amd64-compiler-targets)
runs on one native host; routine PR CI does not run that matrix.

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

The [coverage audit](reports/validation.md#coverage) records the build
configurations, inputs, and state sequences covered by exact comparisons,
and identifies evidence gaps. The documented
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
