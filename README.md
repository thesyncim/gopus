# gopus

Pure Go Opus codec for Go applications.

[![Go Reference](https://pkg.go.dev/badge/github.com/thesyncim/gopus.svg)](https://pkg.go.dev/github.com/thesyncim/gopus)
[![Go Report Card](https://goreportcard.com/badge/github.com/thesyncim/gopus)](https://goreportcard.com/report/github.com/thesyncim/gopus)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

`gopus` implements Opus ([RFC 6716](https://datatracker.ietf.org/doc/html/rfc6716)) and Ogg Opus ([RFC 7845](https://datatracker.ietf.org/doc/html/rfc7845)) in pure Go. It is built for real-time use: no cgo, no C toolchain, and caller-owned buffers on the main encode/decode hot path.

## Status

`gopus` is usable today, but it is still pre-v1.

- Recommended starting surface: `Encoder`, `Decoder`, `MultistreamEncoder`, `MultistreamDecoder`, `Reader`, `Writer`, and `container/ogg`.
- The main API target is the zero-allocation caller-owned path:
  - `func (d *Decoder) Decode(data []byte, pcm []float32) (int, error)`
  - `func (e *Encoder) Encode(pcm []float32, data []byte) (int, error)`
- The default build intentionally does not support every optional libopus build-time extension. Supported default controls are `SetDNNBlob(...)` plus `SetQEXT(...)` / `QEXT()`. DRED control and standalone surfaces are compiled explicitly with `-tags gopus_dred`; default builds keep DRED controls absent and DRED runtime hooks dormant. OSCE BWE remains quarantine-only under `-tags gopus_unsupported_controls`, and that quarantine tag does not itself make `SupportsOptionalExtension(...)` report support. See [Optional Extensions](docs/optional-extensions.md) for the release-contract matrix.
- Low-level packages such as `celt`, `silk`, `hybrid`, `rangecoding`, and `plc` are implementation detail, not a stable public contract yet.
- Validation and parity work is pinned against libopus 1.6.1.
- No tagged release has been published yet. If you adopt `gopus` before `v0.1.0`, pin the exact version you validate.

## Installation

```bash
go get github.com/thesyncim/gopus
```

Requirements:

- Go 1.25+
- No cgo or external C toolchain for normal builds

## Performance Snapshot

Official RFC 8251 test-vector decode benchmarks use pinned libopus 1.6.1 as the baseline, with the same preloaded packets, reset cadence, and 48 kHz stereo output. Current checked-in results were measured on Apple M4 Max with Go 1.26.0; the full report uses median-of-3 runs at 200ms, 1s, and 5s minimum run times. The table below highlights the 5s row. Ratios above `1.00x` mean `gopus` is slower than libopus.

| Path | gopus ns/sample | libopus ns/sample | gopus/libopus | gopus allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Float32 decode | 24.69 | 19.08 | 1.29x | 0 |
| Int16 decode | 25.69 | 19.26 | 1.33x | 0 |

See the full Markdown report in [Official Test Vector Decode Performance](docs/testvector-benchmarks.md). Reproduce it with `BENCH_TESTVECTORS_COMPARE_CASES=aggregate BENCH_TESTVECTORS_COMPARE_TIMES=200,1000,5000 make bench-testvectors-compare`.

## Quick Start

Use caller-owned buffers in real-time paths.

```go
package main

import (
	"log"

	"github.com/thesyncim/gopus"
)

func main() {
	const sampleRate = 48000
	const channels = 2

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		log.Fatal(err)
	}

	decCfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		log.Fatal(err)
	}

	pcmIn := make([]float32, 960*channels)
	packetBuf := make([]byte, 4000)
	pcmOut := make([]float32, decCfg.MaxPacketSamples*channels)

	nPacket, err := enc.Encode(pcmIn, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if nPacket == 0 {
		return
	}

	nSamples, err := dec.Decode(packetBuf[:nPacket], pcmOut)
	if err != nil {
		log.Fatal(err)
	}

	decoded := pcmOut[:nSamples*channels]
	_ = decoded
}
```

Packet loss concealment uses `dec.Decode(nil, pcmOut)`. If you prefer convenience over zero-allocation behavior, allocating helpers such as `EncodeFloat32` and `EncodeInt16Slice` are also available.

## Support Matrix

| Area | Status | Notes |
| --- | --- | --- |
| Mono/stereo encode/decode | Supported | `Encoder` / `Decoder` with caller-owned buffers |
| Multistream encode/decode | Supported | Default mappings for 1-8 channels; explicit mappings up to 255 channels |
| Ogg Opus container | Supported | `container/ogg` reader/writer |
| Streaming facade | Supported | `Reader` / `Writer` |
| Allocating convenience helpers | Supported | Simpler to use, but not zero-allocation |
| Low-level codec packages | Experimental | May change before `v1` |
| Optional libopus build-time extensions | Mixed | `SetDNNBlob(...)` plus `SetQEXT(...)` / `QEXT()` are supported in the default build. DRED control and standalone surfaces are supported with `-tags gopus_dred`, which exposes `SetDREDDuration(...)` / `DREDDuration()` and standalone `DREDDecoder` / `DRED` helpers. Broader DRED audio-path parity remains seam-specific. OSCE BWE remains quarantine-only under `-tags gopus_unsupported_controls`. See [Optional Extensions](docs/optional-extensions.md) |

Environment and codec expectations:

| Topic | Current expectation |
| --- | --- |
| Go versions | Go 1.25+ is required; scheduled safety automation also exercises Go 1.26 |
| CI platforms | Linux, macOS, and Windows |
| Optimized architectures | `amd64` and `arm64` have tuned assembly kernels; other architectures use pure Go fallbacks |
| Sample rates | 8000, 12000, 16000, 24000, 48000 Hz |
| Frame durations | 2.5 ms to 120 ms, depending on mode |
| Channels | `Encoder` / `Decoder`: 1-2; default multistream: 1-8; explicit multistream: up to 255 channels |

`Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder` are not safe for concurrent use. Use one instance per goroutine.

## Verification

If you want to evaluate or contribute to the codec, these are the main entry points:

- `go test ./...`
- `make test-quality`
- `make bench-guard`
- `make bench-testvectors`
- `make bench-testvectors-compare`
- `make verify-production`

`make ensure-libopus` bootstraps the pinned libopus 1.6.1 reference used by parity and quality checks. Some verification paths also expect `ffmpeg` and `opusdec` to be available.

## Docs and Project Hygiene

- [Go package docs](https://pkg.go.dev/github.com/thesyncim/gopus)
- [Official test-vector performance](docs/testvector-benchmarks.md)
- [Optional extension policy](docs/optional-extensions.md)
- [Examples guide](examples/README.md)
- [Release notes](docs/releases/README.md)
- [Contributing guide](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Maintainer docs](docs/maintainers/README.md)
- [Assembly notes](ASSEMBLY.md)

## License

BSD 3-Clause. See [LICENSE](LICENSE).
