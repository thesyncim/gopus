# gopus

Pure-Go implementation of the Opus audio codec (RFC 6716 / RFC 8251) — no cgo, no C toolchain, and **zero `unsafe` in the entire Opus codec core**.

- **Memory-safe**: `gopus`, `multistream`, `container/ogg`, and all internal codec packages (`celt`, `silk`, `hybrid`, `encoder`, `rangecoding`, `opusmath`, `dnnmath`, `dred`, `osce`, `lpcnetplc`) import **zero `unsafe`** and perform no raw pointer arithmetic.
- **Zero-allocation hot paths**: caller-buffer encode/decode APIs run at **0 B/op, 0 allocs/op** after warmup.
- **Faster than libopus 1.6.1**: beats pinned libopus 1.6.1 C SIMD on Apple M4 Max with Go 1.27's `GOEXPERIMENT=simd` (**0.88×** encode aggregate, **0.94×** float32 RFC decode) while keeping scalar `purego` builds fast and portable.
- **Parity-verified**: tested continuously against pinned libopus 1.6.1 and the RFC 8251 test vectors on Linux, macOS, and Windows.
- **Full-featured**: SILK, CELT, and Hybrid modes; mono, stereo, multistream, and ambisonics; 8–48 kHz (96 kHz via `gopus_qext`); 2.5–120 ms frames; Ogg Opus (`container/ogg`); RTP RED (`container/red`); standalone `VAD` and `SpeechDetector`.

[API reference](https://pkg.go.dev/github.com/thesyncim/gopus) ·
[Quick start](#quick-start) · [Usage](#usage) · [Memory safety](#memory-safety) ·
[Performance](#performance) · [Build options](#build-options) ·
[Examples](#examples) · [Parity & testing](#parity--testing)

## Install

Requires **Go 1.27.4** or newer.

```sh
go get github.com/thesyncim/gopus@v0.2.10
```

## Quick start

Encode and decode a 20 ms stereo frame at 48 kHz with caller-owned buffers:

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

	// Allocate once outside the audio loop.
	pcm := make([]float32, frameSize*channels) // interleaved [-1, 1]
	packet := make([]byte, cfg.MaxPacketBytes) // length sets encode byte budget
	out := make([]float32, cfg.MaxPacketSamples*channels)

	n, err := enc.Encode(pcm, packet)
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

## Usage

### Buffers & PCM formats

Use one `Encoder`/`Decoder` per stream (neither is safe for concurrent calls; call `Reset` to start a new stream). Allocate PCM and packet buffers outside the audio loop:

- **Zero-alloc methods**: `Encode`, `EncodeInt16`, `EncodeInt24`, `Decode`, `DecodeInt16`, `DecodeInt24`.
- **Convenience slice-returning methods**: `EncodeFloat32`, `EncodeInt16Slice`, `EncodeInt24Slice`, `DecodeInt24Slice`.
- **Layouts**: all formats are interleaved (`L0, R0, L1, R1, ...`). `float32` is normally in `[-1, 1]`; `Int24` uses right-justified signed 24-bit samples in `[]int32` (decode output is unclamped).
- **Sizing rules**: `Encode` uses `len(packet)` (not `cap`) as the byte budget — always pass the full `packet` buffer and transmit `packet[:n]`. `SetFrameSize` changes the required input length per channel (`FrameSize()`). Default decoder limits are 5,760 samples/channel and 1,500 bytes (configure larger limits in `DecoderConfig` before construction, e.g. 11,520 for 120 ms at 96 kHz).

```go
input := make([]int16, enc.FrameSize()*channels)
packetBuf := make([]byte, cfg.MaxPacketBytes)
output := make([]int16, cfg.MaxPacketSamples*channels)

n, err := enc.EncodeInt16(input, packetBuf)
if err != nil {
	log.Fatal(err)
}
samples, err := dec.DecodeInt16(packetBuf[:n], output)
if err != nil {
	log.Fatal(err)
}
decoded := output[:samples*channels]
```

### Packet loss (PLC & FEC)

Pick **one** recovery path per lost frame:

- **Immediate concealment (PLC)**: pass `nil` with an output slice sized to the missing duration (or pass the full `MaxPacketSamples*Channels` buffer to reuse the last frame's duration).
- **In-band FEC**: if your jitter buffer can wait one packet, recover the missing frame from `nextPacket` first, then decode `nextPacket` normally. (In-band FEC is present in SILK/Hybrid packets when enabled by the sender; if absent, `DecodeWithFEC` falls back to PLC. At 96 kHz it performs PLC.)

```go
// PLC: conceal 20 ms immediately.
missing := out[:960*channels]
samples, err := dec.Decode(nil, missing)

// Or FEC: recover missing frame from nextPacket, then decode nextPacket.
recoveredN, err := dec.DecodeWithFEC(nextPacket, missing, true)
currentN, err := dec.Decode(nextPacket, out)
```

See [examples/packet-loss](examples/packet-loss) for a runnable end-to-end example.

### Encoder controls

Default bitrate is 64 kbps (`Bitrate()` reports the configured target, including `BitrateAuto`/`BitrateMax`). With DTX enabled, `Encode` can return `(0, nil)` when there is no packet to transmit.

```go
_ = enc.SetBitrate(32000)   // bits/second across all channels
_ = enc.SetComplexity(5)    // 0–10
enc.SetVBR(true)
enc.SetDTX(true)
enc.SetFEC(true)            // allow SILK/Hybrid in-band FEC
_ = enc.SetPacketLoss(10)   // expected loss %, 0–100
```

### Standalone voice activity

`VAD` and `SpeechDetector` score 10 or 20 ms mono frames without allocating on analysis calls (single-goroutine per instance; `Reset` clears state):

- **`VAD`**: SILK voice activity detector at 8, 12, or 16 kHz; returns Q8 activity (`0–255`).
- **`SpeechDetector`**: neural tonality analyzer (libopus `activity_probability`) at 16, 24, or 48 kHz; returns `[0, 1]`. First ~200 ms (10 windows) are warm-up.

```go
det, err := gopus.NewSpeechDetector(16000)
if err != nil {
	log.Fatal(err)
}
prob, err := det.AnalyzeInt16(frame320) // 20 ms at 16 kHz
talking := prob >= 0.5
```

## Packages

| Package | Purpose |
|---|---|
| [gopus](https://pkg.go.dev/github.com/thesyncim/gopus) | Single-stream encoder/decoder, streaming reader/writer, multistream facade, packet parsing, repacketizer, VAD |
| [multistream](https://pkg.go.dev/github.com/thesyncim/gopus/multistream) | Multistream codec and projection / ambisonics |
| [container/ogg](https://pkg.go.dev/github.com/thesyncim/gopus/container/ogg) | Ogg Opus reader/writer with seeking (RFC 7845) |
| [container/red](https://pkg.go.dev/github.com/thesyncim/gopus/container/red) | RTP RED payload builder, parser, and recovery (RFC 2198) |
| [types](https://pkg.go.dev/github.com/thesyncim/gopus/types) | Shared mode, bandwidth, and signal enums |

## Memory safety

The entire Opus codec core — `gopus`, `multistream`, `container/ogg`, and every internal codec package (`internal/celt`, `internal/silk`, `internal/hybrid`, `internal/encoder`, `internal/rangecoding`, `internal/opusmath`, `internal/dnnmath`, `internal/dred`, `internal/osce`, `internal/lpcnetplc`) — contains **zero `"unsafe"` imports** and zero raw pointer arithmetic.

Across the entire repository, only **one production file** imports `"unsafe"`:

- [container/red/red.go](container/red/red.go): `byteSlicesOverlap` compares `uintptr(unsafe.Pointer(unsafe.SliceData(...)))` address ranges without dereferencing memory (the same pattern as Go's `crypto/internal/alias`) so `red.BuildAppend` stays zero-alloc and safe against overlapping input/output slices.

*(Windows-only test helper [internal/libopustest/build_lock_windows.go](internal/libopustest/build_lock_windows.go) uses `unsafe.Pointer` for a Win32 `LockFileEx` call during C-oracle builds and is never linked into production binaries.)*

## Performance

Go (`GOEXPERIMENT=simd`, `default.pgo`, **0 B/op, 0 allocs/op** on every row) vs pinned libopus 1.6.1 SIMD (`-O3`, NEON on `arm64`, SSE/AVX2 on `amd64`), measured with `Go 1.27.4` running C immediately before Go per case. Lower ratios (`< 1.00×`) mean gopus is faster.

- **`arm64`**: Apple M4 Max (`darwin/arm64`), Apple Clang 21.0.0, `GOMAXPROCS=1`, medians across 5 runs of ≥500 ms.
- **`amd64`**: Intel Xeon Platinum 8573C (`linux/amd64`), GCC 13.3.0, `GOAMD64=v1`, GitHub Actions `test-linux-simd-ab` paired run (3 × 250 ms).

| Workload | `arm64` C / Go (ns/sample) | `arm64` Go / C | `amd64` C / Go (ns/sample) | `amd64` Go / C |
|---|---:|---:|---:|---:|
| Encode aggregate | 150.30 / **132.80** | **0.88×** | 213.02 / **187.38** | **0.88×** |
| Encode CELT, 20 ms stereo, 128 kbps | 77.21 / **73.83** | **0.96×** | 148.69 / **133.69** | **0.90×** |
| Encode CELT, 5 ms mono, 64 kbps | 34.58 / 35.29 | 1.02× | 81.24 / **76.51** | **0.94×** |
| Encode SILK, WB 20 ms mono, 32 kbps | 349.03 / **290.23** | **0.83×** | 418.04 / **352.96** | **0.84×** |
| Encode Hybrid, 20 ms mono, 64 kbps | 182.54 / **163.14** | **0.89×** | 249.09 / **225.15** | **0.90×** |
| Encode Hybrid, 20 ms stereo, 96 kbps | 94.10 / **93.03** | **0.99×** | 168.02 / **148.59** | **0.88×** |
| Decode RFC 8251 vectors (float32, 20,075 pkts) | 19.17 / **17.93** | **0.94×** | 38.04 / **36.88** | **0.97×** |
| Decode RFC 8251 vectors (int16, 20,075 pkts) | 19.16 / 19.64 | 1.03× | 42.21 / 42.26 | 1.00× |

Detailed scalar, PGO-off, short-frame, and native AMD EPYC 7763 (`amd64`) numbers live in [reports/benchmarks/2026-10-23-m4/README.md](reports/benchmarks/2026-10-23-m4/README.md) and [reports/validation.md#performance](reports/validation.md#performance).

Reproduce locally:

```sh
# SIMD comparison (Go SIMD vs libopus SIMD)
GOMAXPROCS=1 GOEXPERIMENT=simd LIBOPUS_ENABLE_SIMD=1 make bench-libopus-guard \
  PGO_FILE=default.pgo BENCH_LIBOPUS_GUARD_TIME=500ms BENCH_LIBOPUS_GUARD_COUNT=5

# Scalar comparison (Go purego/nosimd vs libopus scalar)
GOMAXPROCS=1 GOEXPERIMENT=nosimd LIBOPUS_ENABLE_SCALAR=1 make bench-libopus-guard \
  PGO_FILE=default.pgo BENCH_LIBOPUS_GUARD_TIME=500ms BENCH_LIBOPUS_GUARD_COUNT=5
```

On x86-64 with AVX2/FMA, prefix with `GOAMD64=v3 GOPUS_LIBOPUS_AMD64_TARGET=v3`. Pass `PGO_FILE=off` to measure without PGO.

## Build options

### SIMD & PGO

Default builds use portable scalar Go (`purego`). Opt into Go 1.27's `simd/archsimd` kernels (with runtime CPU dispatch and scalar fallbacks) when building your application:

```sh
go build ./...                                  # portable scalar build
GOEXPERIMENT=simd go build ./...                # enable ARM64 NEON / AMD64 AVX2 kernels
GOEXPERIMENT=simd go build -tags purego ./...   # force scalar kernels
go build -pgo=/path/to/gopus/default.pgo ./...  # apply checked-in PGO profile
```

### Optional codec extensions

Optional features mirror libopus configure flags and link **zero code** in default builds (`SetDNNBlob` returns `ErrOptionalExtensionUnavailable`):

| Build tag | libopus flag | Feature | Support probe |
|---|---|---|---|
| `gopus_dred` | `--enable-dred` | DRED controls & standalone recovery | `OptionalExtensionDRED`, `OptionalExtensionDNNBlob` |
| `gopus_osce` | `--enable-osce` | OSCE BWE / LACE / NoLACE & deep PLC | `OptionalExtensionDNNBlob` (`OptionalExtensionOSCEBWE` reports false) |
| `gopus_qext` | `--enable-qext` | QEXT & native 96 kHz | `OptionalExtensionQEXT` |
| `gopus_custom_modes` | `--enable-custom-modes` | Opus Custom modes | — |
| `gopus_fixed_point` | `--enable-fixed-point` | Fixed-point CELT/SILK pipeline (mutually exclusive with DRED/OSCE) | — |

## Examples

Run from the repository root (`go run ./examples/<name>`, pass `-h` for flags):

| Example | Purpose |
|---|---|
| [roundtrip-min](examples/roundtrip-min) | Minimal zero-alloc encode/decode |
| [packet-loss](examples/packet-loss) | PLC and in-band FEC recovery ordering |
| [ogg-file](examples/ogg-file) | Ogg Opus reading, writing, and seeking |
| [sample-rates](examples/sample-rates) | `int16` PCM at 8/12/16/24/48 kHz |
| [low-delay](examples/low-delay) | Short CELT frames and algorithmic delay |
| [repacketizer](examples/repacketizer) | Frame merging, splitting, and padding |
| [surround](examples/surround) | Multistream 5.1 in Vorbis channel order |
| [roundtrip](examples/roundtrip) | Quality across codec configurations |
| [encode-play](examples/encode-play), [decode-play](examples/decode-play) | Ogg encoding, WAV decoding, optional playback |
| [ffmpeg-interop](examples/ffmpeg-interop) | Interoperability with `ffmpeg` / `ffprobe` |
| [mix-arrivals](examples/mix-arrivals) | Timed speech mixing with loss and jitter |
| [bench-encode](examples/bench-encode), [bench-decode](examples/bench-decode) | Quick file-based throughput estimates |

Standalone example modules (run inside each directory): [external-consumer-smoke](examples/external-consumer-smoke), [webrtc-control](examples/webrtc-control), and [webrtc-dred-loopback](examples/webrtc-dred-loopback/README.md). Verify all examples without audio hardware via `make test-consumer-smoke test-examples-smoke`.

## Parity & testing

Parity is verified against live libopus 1.6.1 C oracles (built automatically by the test targets; the library itself has no C dependency) across two tiers: bit-exact kernel/entropy-coder/SILK oracles plus differential fuzzing, and `opus_compare` audio quality on real speech/music corpora. See [reports/validation.md](reports/validation.md) for the parity contract and coverage matrix.

| Command | Checks |
|---|---|
| `make test-fast` | Fast iteration suite |
| `make test` | Full default suite against live libopus 1.6.1 oracles |
| `make test-simd` | Full `GOEXPERIMENT=simd` suite against matching SIMD C oracles |
| `make test-race` | Fast suite under `-race` |
| `make test-build-contract` | Build tags and optional-feature isolation |
| `make test-type-parity` | Libopus scalar-width parity guard |

## Releases & governance

API is pre-v1 (`v0.1.0` is retracted). Each release requires green CI on the tagged commit:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

- Latest release: [v0.2.10](https://github.com/thesyncim/gopus/releases/tag/v0.2.10)
- Security: [SECURITY.md](SECURITY.md)
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md) · [Code of Conduct](CODE_OF_CONDUCT.md)
- License: [LICENSE](LICENSE)
