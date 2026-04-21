# gopus

Pure Go Opus codec with production-focused validation.

[![Go Reference](https://pkg.go.dev/badge/github.com/thesyncim/gopus.svg)](https://pkg.go.dev/github.com/thesyncim/gopus)
[![Go Report Card](https://goreportcard.com/badge/github.com/thesyncim/gopus)](https://goreportcard.com/report/github.com/thesyncim/gopus)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

`gopus` implements Opus ([RFC 6716](https://datatracker.ietf.org/doc/html/rfc6716)) and Ogg Opus ([RFC 7845](https://datatracker.ietf.org/doc/html/rfc7845)) in pure Go.

No cgo. No C toolchain. Caller-owned buffers in the encode/decode hot path.

## Why gopus

- Pure Go codec stack (encode + decode), interoperable with libopus.
- Full Opus modes: SILK, CELT, Hybrid.
- Architecture-tuned assembly kernels on `amd64`/`arm64` with pure Go fallbacks on other targets.
- Core hot path is zero-allocation with caller-provided buffers.
- Ogg Opus container reader/writer (`container/ogg`).
- Multistream support (default mappings for 1-8 channels, explicit mappings up to 255 channels with `streams + coupledStreams <= 255`).
- Compliance and parity coverage against libopus 1.6.1 fixtures.

## Status Snapshot (2026-04-21)

- Decoder: complete and stable across SILK/CELT/Hybrid, stereo, and sample rates.
- Encoder: complete core Opus feature surface (FEC/LBRR, DTX, multistream, ambisonics, and standard controls). QEXT is enabled and libopus-validated in the default build. Other optional libopus build-time extensions such as DRED, weights-file DNN blobs, and OSCE BWE remain intentionally unsupported; gate them with `SupportsOptionalExtension(...)` and expect `ErrUnsupportedExtension` when unavailable.
- QEXT parity: libopus-backed tests cover decoding libopus-generated QEXT packets, decoding gopus-generated QEXT packets in libopus, and the shared packet-extension framing path QEXT depends on.
- Allocations: zero allocs/op in encoder and decoder core hot paths.
- `TestSILKParamTraceAgainstLibopus`: `PASS` with exact SILK-WB trace parity on canonical 50-frame fixture.
- `TestEncoderComplianceSummary`: `PASS` (`23 passed, 0 failed` at current libopus-relative thresholds).
- `make verify-production`: `PASS` on the current baseline.
- `make test-quality`: `PASS` on the current baseline.
- Remaining focus: public API hardening, CI/release trust, and maintainability cleanup that makes green gates more boring and more believable.

## Project Priorities

Current work is ordered like this:

1. Parity with libopus in quality and features.
2. Performance.
3. Maintainability.
4. Documentation.
5. Dead-test cleanup.

Default validation flow:

1. `make test-quality`
2. `make bench-guard`
3. `make verify-production` before merge-ready changes

`make test-quality` uses the pinned libopus `opus_compare` metric for
encoder/decoder quality tracking against libopus reference packets and includes
the focused Hybrid/CELT transition compatibility checks.
`make test-exactness` stays opt-in when you specifically want libopus-internal
trace and state exactness checks. `make quality-report` writes a compact
Markdown summary plus structured logs to `reports/quality/`.

## Installation

```bash
go get github.com/thesyncim/gopus
```

Requirements:

- Go 1.25+

## Quick Start (Zero-Allocation Path)

Use `Encode` / `Decode` with caller-owned buffers for real-time paths.

```go
package main

import (
	"log"

	"github.com/thesyncim/gopus"
)

func main() {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	_ = enc.SetBitrate(128000)
	_ = enc.SetComplexity(10)
	_ = enc.SetFrameSize(960) // 20 ms at 48 kHz

	decCfg := gopus.DefaultDecoderConfig(48000, 2)
	dec, err := gopus.NewDecoder(decCfg)
	if err != nil {
		log.Fatal(err)
	}

	// Input/output buffers are caller-owned.
	pcmIn := make([]float32, enc.FrameSize()*enc.Channels())
	packetBuf := make([]byte, 4000)
	pcmOut := make([]float32, decCfg.MaxPacketSamples*decCfg.Channels)

	nPacket, err := enc.Encode(pcmIn, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if nPacket == 0 {
		// DTX can suppress silent frames.
		return
	}

	nSamples, err := dec.Decode(packetBuf[:nPacket], pcmOut)
	if err != nil {
		log.Fatal(err)
	}
	decoded := pcmOut[:nSamples*decCfg.Channels]
	_ = decoded
}
```

Packet loss concealment (PLC):

```go
nSamples, err := dec.Decode(nil, pcmOut) // nil packet => PLC
_ = nSamples
_ = err
```

## Convenience API (Allocating)

Convenience helpers allocate output buffers:

- `(*Encoder).EncodeFloat32`
- `(*Encoder).EncodeInt16Slice`
- `(*MultistreamEncoder).EncodeFloat32`
- `(*MultistreamEncoder).EncodeInt16Slice`

Use them for simplicity, not for the tightest real-time loop.

## API Surface

| Type | Purpose |
| --- | --- |
| `Encoder` | Mono/stereo Opus encoding |
| `Decoder` | Mono/stereo Opus decoding |
| `MultistreamEncoder` | Multichannel Opus encoding |
| `MultistreamDecoder` | Multichannel Opus decoding |
| `Reader` | Streaming decode (`io.Reader`) |
| `Writer` | Streaming encode (`io.WriteCloser`) |
| `container/ogg.Reader` | Ogg Opus packet reader |
| `container/ogg.Writer` | Ogg Opus packet writer |

Most applications should stay on the top-level `gopus` facade plus `container/ogg`.
Low-level subpackages such as `celt`, `silk`, `hybrid`, `rangecoding`, `plc`,
and `multistream` expose implementation details and may change freely before the
first release.

Application hints:

- `ApplicationVoIP`
- `ApplicationAudio`
- `ApplicationLowDelay`
- `ApplicationRestrictedSilk` for libopus-compatible SILK-only init-time behavior
- `ApplicationRestrictedCelt` for libopus-compatible CELT-only init-time behavior

## Ogg Opus Example

```go
package main

import (
	"io"
	"log"
	"os"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func main() {
	const sampleRate = 48000
	const channels = 2
	const frameSize = 960 // 20 ms at 48 kHz

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		log.Fatal(err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		log.Fatal(err)
	}

	outFile, err := os.Create("out.opus")
	if err != nil {
		log.Fatal(err)
	}
	defer outFile.Close()

	ow, err := ogg.NewWriter(outFile, uint32(sampleRate), uint8(channels))
	if err != nil {
		log.Fatal(err)
	}

	pcm := make([]float32, frameSize*channels)
	packetBuf := make([]byte, 4000)
	pcmOut := make([]float32, 5760*channels)

	nPacket, err := enc.Encode(pcm, packetBuf)
	if err != nil {
		log.Fatal(err)
	}
	if nPacket > 0 {
		if err := ow.WritePacket(packetBuf[:nPacket], frameSize); err != nil {
			log.Fatal(err)
		}
	}
	if err := ow.Close(); err != nil {
		log.Fatal(err)
	}

	inFile, err := os.Open("out.opus")
	if err != nil {
		log.Fatal(err)
	}
	defer inFile.Close()

	or, err := ogg.NewReader(inFile)
	if err != nil {
		log.Fatal(err)
	}

	for {
		packet, _, err := or.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		if _, err := dec.Decode(packet, pcmOut); err != nil {
			log.Fatal(err)
		}
	}
}
```

## Supported Configurations

Sample rates:

- 8000, 12000, 16000, 24000, 48000 Hz

Frame sizes (samples/channel at 48 kHz):

- 120 (2.5 ms, CELT-only)
- 240 (5 ms, CELT-only)
- 480 (10 ms)
- 960 (20 ms, default)
- 1920 (40 ms, SILK/hybrid)
- 2880 (60 ms, SILK/hybrid)
- 3840 (80 ms, expert frame duration)
- 4800 (100 ms, expert frame duration)
- 5760 (120 ms, expert frame duration)

Channels:

- Core `Encoder` / `Decoder`: 1 or 2 channels
- Default multistream constructors: 1-8 channels
- Explicit multistream constructors: up to 255 channels, with `streams + coupledStreams <= 255`

## Performance and Allocations

Core guidance:

- Reuse `Encoder` / `Decoder` instances.
- Reuse input/output buffers.
- Use `Encode` and `Decode` in hot paths.

Benchmarks:

```bash
go test -run='^$' -bench='^Benchmark(DecoderDecode|EncoderEncode)_' -benchmem ./...
go test -bench=. -benchmem ./...
go test -run '^TestHotPathAllocs' -count=1 .
make bench-guard
```

PGO:

```bash
make pgo-generate
make build
```

## Assembly Optimizations

`gopus` ships default-on assembly where it is exact and worth keeping.
No user-passed build tag is required to enable assembly.

- `arm64`: baseline arm64 kernels are selected automatically in CELT and SILK hot paths.
- `amd64`: baseline-safe kernels are selected automatically, and additional AVX/FMA CELT kernels are selected automatically when the package is built with `GOAMD64=v3` or higher.
- Other architectures use pure Go fallbacks with matching behavior.

Current production assembly surfaces include CELT FFT/IMDCT/correlation/PVQ helpers and SILK arm64 inner-product/pitch-xcorr helpers.
For the exact file map and maintenance rules, see [`ASSEMBLY.md`](ASSEMBLY.md).

## Parity and Compliance Workflow

```bash
# Full suite
go test ./... -count=1

# SILK trace parity vs libopus
go test ./testvectors -run TestSILKParamTraceAgainstLibopus -count=1 -v

# Encoder compliance summary
go test ./testvectors -run TestEncoderComplianceSummary -count=1 -v

# Project shortcuts
make test-fast
make test-race
make test-fuzz-smoke
make test-quality
make bench-guard
make verify-production
make verify-production-exhaustive
make release-evidence
make ensure-libopus
make test-exhaustive
make test-provenance
make fixtures-gen
make fixtures-gen-amd64
make docker-build
make docker-test
make docker-test-exhaustive
```

Docker targets use persistent caches:
- build layers in `.docker-cache/` (buildx local cache)
- Go modules in `gopus-gomod` volume
- Go build cache in `gopus-gobuild-*` volumes
- pinned libopus artifacts in `gopus-libopus-*` volumes

## Examples

```bash
go build ./examples/...

go run ./examples/roundtrip
go run ./examples/ogg-file
go run ./examples/ffmpeg-interop
go run ./examples/decode-play
go run ./examples/encode-play
go run ./examples/mix-arrivals
go run ./examples/bench-decode
go run ./examples/bench-encode
```

See [`examples/README.md`](examples/README.md) for details.

## Project Layout

- `encoder/`: encoder core
- `silk/`: SILK implementation
- `celt/`: CELT implementation
- `hybrid/`: SILK/CELT bridge
- `testvectors/`: parity/compliance fixtures and tests
- `container/ogg/`: Ogg Opus reader/writer
- `ASSEMBLY.md`: architecture-specific assembly coverage and fallbacks
- `tmp_check/opus-1.6.1/`: libopus 1.6.1 reference tooling

## Thread Safety

`Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder` are not safe for concurrent use.

Use one instance per goroutine.

## Contributing

1. Open an issue describing the change.
2. Add or update focused tests.
3. Verify parity/compliance commands for affected areas.
4. Submit a PR with a clear problem statement and tradeoffs.

See [`CI_GUARDRAILS.md`](CI_GUARDRAILS.md) for merge-blocking CI checks and branch-protection requirements.

## License

BSD 3-Clause. See [LICENSE](LICENSE).

## References

- [Opus codec website](https://opus-codec.org/)
- [RFC 6716](https://datatracker.ietf.org/doc/html/rfc6716)
- [RFC 7845](https://datatracker.ietf.org/doc/html/rfc7845)
- [RFC 8251](https://datatracker.ietf.org/doc/html/rfc8251)
- [Go package docs](https://pkg.go.dev/github.com/thesyncim/gopus)
