# gopus

**Pure Go implementation of the Opus audio codec**

[![Go Reference](https://pkg.go.dev/badge/github.com/thesyncim/gopus.svg)](https://pkg.go.dev/github.com/thesyncim/gopus)
[![Go Report Card](https://goreportcard.com/badge/github.com/thesyncim/gopus)](https://goreportcard.com/report/github.com/thesyncim/gopus)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

---

## Highlights

| Feature | Description |
|---------|-------------|
| **No CGO** | Pure Go implementation with zero C dependencies |
| **No External Dependencies** | Uses only the Go standard library |
| **RFC 6716 Compliant** | Full Opus codec specification |
| **RFC 7845 Compliant** | Ogg Opus container format |
| **Full Encode/Decode** | Complete encoder and decoder |
| **All Opus Modes** | SILK, CELT, and Hybrid |
| **Surround Sound** | Mono, stereo, and up to 7.1 surround (8 channels) |

---

## Features

### Decoder

- **All modes:** SILK (speech), CELT (audio), Hybrid (wideband speech)
- **All bandwidths:** Narrowband (8 kHz) to Fullband (48 kHz)
- **All frame sizes:** 2.5, 5, 10, 20, 40, 60 ms
- **Packet Loss Concealment (PLC):** Graceful handling of lost packets
- **Multistream:** Decode surround sound with 1-8 channels

### Encoder

- **Bitrate modes:** VBR, CBR, CVBR (constrained VBR)
- **Bitrate range:** 6-510 kbps
- **Forward Error Correction (FEC):** In-band redundancy for lossy networks
- **Discontinuous Transmission (DTX):** Bandwidth savings during silence
- **Complexity control:** 0-10 quality vs. speed tradeoff
- **Application hints:** VoIP, Audio, LowDelay

### Container

- **Ogg Opus read/write:** Per RFC 7845
- **Streaming support:** Sequential packet access
- **Metadata:** OpusHead and OpusTags headers

### Streaming API

- **io.Reader/io.Writer:** Standard Go streaming interfaces
- **PacketReader/PacketSink:** Custom packet I/O abstraction
- **Sample formats:** float32 and int16 PCM

### Multistream (Surround Sound)

- **Channel configurations:** 1-8 channels
- **Vorbis-style mapping:** Standard channel ordering per RFC 7845
- **Coupled/uncoupled streams:** Stereo pairs and mono streams

---

## Installation

```bash
go get github.com/thesyncim/gopus
```

**Requirements:** Go 1.18 or later

---

## Quick Start

### Encoding

```go
package main

import (
    "log"

    "github.com/thesyncim/gopus"
)

func main() {
    // Create encoder: 48kHz, stereo, optimized for audio
    enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
    if err != nil {
        log.Fatal(err)
    }

    // Prepare 20ms of stereo audio (960 samples per channel at 48kHz)
    pcm := make([]float32, 960*2)
    // ... fill pcm with audio samples (interleaved: L0, R0, L1, R1, ...) ...

    // Encode to Opus packet
    packet, err := enc.EncodeFloat32(pcm)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Encoded %d samples to %d bytes", 960, len(packet))
}
```

### Decoding

```go
package main

import (
    "log"

    "github.com/thesyncim/gopus"
)

func main() {
    // Create decoder: 48kHz, stereo
    cfg := gopus.DefaultDecoderConfig(48000, 2)
    dec, err := gopus.NewDecoder(cfg)
    if err != nil {
        log.Fatal(err)
    }
    pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

    // Decode an Opus packet
    packet := []byte{ /* Opus packet data */ }
    n, err := dec.Decode(packet, pcmOut)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Decoded %d samples", n)
}
```

### Packet Loss Concealment

```go
// When a packet is lost, pass nil to trigger PLC
if packetLost {
    n, err = dec.Decode(nil, pcmOut)  // PLC generates replacement audio
} else {
    n, err = dec.Decode(packet, pcmOut)
}
```

---

## API Overview

| Type | Purpose | Package |
|------|---------|---------|
| `Encoder` | Frame-based Opus encoding | `gopus` |
| `Decoder` | Frame-based Opus decoding | `gopus` |
| `MultistreamEncoder` | Surround sound encoding (1-8 ch) | `gopus` |
| `MultistreamDecoder` | Surround sound decoding (1-8 ch) | `gopus` |
| `Reader` | Streaming decode via io.Reader | `gopus` |
| `Writer` | Streaming encode via io.Writer | `gopus` |
| `ogg.Reader` | Read Ogg Opus files | `gopus/container/ogg` |
| `ogg.Writer` | Write Ogg Opus files | `gopus/container/ogg` |

### Application Hints

| Constant | Use Case |
|----------|----------|
| `ApplicationVoIP` | Speech transmission, low latency |
| `ApplicationAudio` | Music and high-quality audio |
| `ApplicationLowDelay` | Minimum algorithmic delay |

---

## Advanced Usage

### Encoder Configuration

```go
enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

// Set bitrate (6000-510000 bps)
enc.SetBitrate(128000)  // 128 kbps

// Set complexity (0-10, higher = better quality, more CPU)
enc.SetComplexity(10)

// Enable Forward Error Correction for lossy networks
enc.SetFEC(true)

// Enable DTX for VoIP to save bandwidth during silence
enc.SetDTX(true)

// Set frame size (samples at 48kHz)
enc.SetFrameSize(960)   // 20ms (default)
enc.SetFrameSize(480)   // 10ms
enc.SetFrameSize(1920)  // 40ms
enc.SetFrameSize(2880)  // 60ms
```

### Multistream (5.1 Surround)

```go
package main

import (
    "log"

    "github.com/thesyncim/gopus"
)

func main() {
    // Create 5.1 surround encoder
    enc, err := gopus.NewMultistreamEncoderDefault(48000, 6, gopus.ApplicationAudio)
    if err != nil {
        log.Fatal(err)
    }

    // 20ms of 6-channel audio at 48kHz
    // Channel order: FL, C, FR, RL, RR, LFE (Vorbis/RFC 7845)
    pcm := make([]float32, 960*6)
    // ... fill with interleaved surround samples ...

    packet, err := enc.EncodeFloat32(pcm)
    if err != nil {
        log.Fatal(err)
    }

    // Create matching decoder
    dec, err := gopus.NewMultistreamDecoderDefault(48000, 6)
    if err != nil {
        log.Fatal(err)
    }

    pcmOut := make([]float32, 960*6)
    n, err := dec.Decode(packet, pcmOut)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("5.1 surround: encoded %d bytes, decoded %d samples", len(packet), n)
}
```

### Ogg Opus Container

```go
package main

import (
    "log"
    "os"

    "github.com/thesyncim/gopus"
    "github.com/thesyncim/gopus/container/ogg"
)

func main() {
    // Writing Ogg Opus
    file, _ := os.Create("output.opus")
    defer file.Close()

    writer, err := ogg.NewWriter(file, ogg.WriterConfig{
        SampleRate: 48000,
        Channels:   2,
    })
    if err != nil {
        log.Fatal(err)
    }

    enc, _ := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)

    // Encode and write frames
    pcm := make([]float32, 960*2)
    for i := 0; i < 100; i++ {
        // ... fill pcm with audio ...
        packet, _ := enc.EncodeFloat32(pcm)
        writer.WritePacket(packet)
    }
    writer.Close()
}
```

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
    // Reading Ogg Opus
    file, _ := os.Open("input.opus")
    defer file.Close()

    reader, err := ogg.NewReader(file)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Channels: %d, PreSkip: %d", reader.Header.Channels, reader.Header.PreSkip)

    cfg := gopus.DefaultDecoderConfig(48000, int(reader.Header.Channels))
    dec, _ := gopus.NewDecoder(cfg)
    pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

    for {
        packet, err := reader.ReadPacket()
        if err == io.EOF {
            break
        }
        if err != nil {
            log.Fatal(err)
        }

        n, _ := dec.Decode(packet, pcmOut)
        // ... process decoded samples ...
        _ = n
    }
}
```

### Streaming API

```go
package main

import (
    "io"
    "log"

    "github.com/thesyncim/gopus"
)

// PacketReader for decoding
type mySource struct {
    packets [][]byte
    index   int
}

func (s *mySource) ReadPacketInto(dst []byte) (int, uint64, error) {
    if s.index >= len(s.packets) {
        return 0, 0, io.EOF
    }
    p := s.packets[s.index]
    s.index++
    if len(p) > len(dst) {
        return 0, 0, io.ErrShortBuffer
    }
    n := copy(dst, p)
    return n, 0, nil
}

// PacketSink for encoding
type mySink struct {
    packets [][]byte
}

func (s *mySink) WritePacket(packet []byte) (int, error) {
    s.packets = append(s.packets, append([]byte(nil), packet...))
    return len(packet), nil
}

func main() {
    // Streaming encode
    sink := &mySink{}
    writer, _ := gopus.NewWriter(48000, 2, sink, gopus.FormatFloat32LE, gopus.ApplicationAudio)

    pcmBytes := make([]byte, 960*2*4) // 20ms stereo float32
    writer.Write(pcmBytes)
    writer.Flush()

    log.Printf("Encoded %d packets", len(sink.packets))

    // Streaming decode
    source := &mySource{packets: sink.packets}
    reader, _ := gopus.NewReader(gopus.DefaultDecoderConfig(48000, 2), source, gopus.FormatFloat32LE)

    buf := make([]byte, 4096)
    for {
        n, err := reader.Read(buf)
        if err == io.EOF {
            break
        }
        // ... process buf[:n] ...
        _ = n
    }
}
```

---

## Supported Configurations

### Sample Rates

| Rate | Use Case |
|------|----------|
| 8000 Hz | Narrowband (telephone quality) |
| 12000 Hz | Mediumband |
| 16000 Hz | Wideband (VoIP) |
| 24000 Hz | Super-wideband |
| 48000 Hz | Fullband (music quality) |

### Channels

| Count | Configuration |
|-------|---------------|
| 1 | Mono |
| 2 | Stereo |
| 3 | 3.0 (L, C, R) |
| 4 | Quadraphonic |
| 5 | 5.0 surround |
| 6 | 5.1 surround |
| 7 | 6.1 surround |
| 8 | 7.1 surround |

### Frame Sizes

| Samples (48kHz) | Duration | Modes |
|-----------------|----------|-------|
| 120 | 2.5 ms | CELT only |
| 240 | 5 ms | CELT only |
| 480 | 10 ms | All modes |
| 960 | 20 ms | All modes (default) |
| 1920 | 40 ms | SILK only |
| 2880 | 60 ms | SILK only |

### Bitrates

- **Minimum:** 6 kbps (speech, narrowband)
- **Maximum:** 510 kbps (music, fullband, stereo)
- **Typical VoIP:** 16-32 kbps
- **Typical music:** 96-128 kbps stereo

---

## Thread Safety

Encoder and Decoder instances are **NOT** safe for concurrent use. Each goroutine should create its own instance:

```go
// Correct: one decoder per goroutine
func decodeWorker(packets <-chan []byte) {
    cfg := gopus.DefaultDecoderConfig(48000, 2)
    dec, _ := gopus.NewDecoder(cfg)
    pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
    for packet := range packets {
        n, _ := dec.Decode(packet, pcmOut)
        // ... process pcm ...
        _ = n
    }
}
```

---

## Buffer Sizing

| Operation | Recommended Size |
|-----------|------------------|
| Encode output | 4000 bytes |
| Decode output (20ms stereo) | 960 * 2 = 1920 samples |
| Decode output (120ms stereo) | 5760 * 2 = 11520 samples |
| Multistream encode output | 4000 * streams bytes |

---

## Benchmarks

gopus includes decode and encode benchmark coverage and end-to-end benchmark examples:

```bash
# Microbenchmarks
go test -run='^$' -bench='^Benchmark(DecoderDecode|EncoderEncode)_' -benchmem ./...

# End-to-end sample benchmarks vs ffmpeg/libopus
go run ./examples/bench-decode
go run ./examples/bench-encode
```

### Profile-Guided Optimization (PGO)

PGO is supported and `default.pgo` is tracked at the module root.

```bash
# Refresh profile from decode hot-path benchmarks
make pgo-generate

# Build with PGO (uses default.pgo)
make build
```

Equivalent manual commands:

```bash
go test -run='^$' -bench='^BenchmarkDecoderDecode_(CELT|Hybrid|SILK|Stereo|MultiFrame)$' -benchtime=20s -cpuprofile default.pgo .
go build -pgo=auto ./...
```

---

## Comparison with libopus

| Aspect | gopus | libopus (CGO) |
|--------|-------|---------------|
| Dependencies | None | Requires C compiler, libopus |
| Cross-compilation | Trivial | Complex |
| Deployment | Single binary | Shared library required |
| Performance | Good | Optimized |
| Compatibility | Fully interoperable | Reference implementation |

**Interoperability:** Opus packets produced by gopus are fully compatible with libopus and vice versa. You can encode with gopus and decode with libopus (or any other RFC 6716 compliant implementation).

---

## Project Status

gopus has completed **14 development phases** with **54 implementation plans**, delivering:

- Complete RFC 6716 Opus codec implementation
- SILK speech encoder/decoder
- CELT audio encoder/decoder
- Hybrid mode encoder/decoder
- Multistream (surround sound) support
- Ogg Opus container format (RFC 7845)
- Comprehensive test coverage
- Libopus cross-validation tests

---

## Contributing

Contributions are welcome! Please:

1. Open an issue to discuss proposed changes
2. Fork the repository
3. Create a feature branch
4. Add tests for new functionality
5. Submit a pull request

### Development

```bash
# Run tests
go test ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...

# Refresh and use PGO profile
make pgo-build
```

---

## License

BSD 3-Clause License. See [LICENSE](LICENSE) for details.

---

## Acknowledgments

- **RFC 6716:** Definition of the Opus Audio Codec
- **RFC 7845:** Ogg Encapsulation for the Opus Audio Codec
- **RFC 8251:** Updates to the Opus Audio Codec
- **Xiph.Org Foundation:** For the Opus codec specification and reference implementation

---

## References

- [Opus Codec](https://opus-codec.org/) - Official Opus website
- [RFC 6716](https://tools.ietf.org/html/rfc6716) - Opus codec specification
- [RFC 7845](https://tools.ietf.org/html/rfc7845) - Ogg Opus encapsulation
- [pkg.go.dev/github.com/thesyncim/gopus](https://pkg.go.dev/github.com/thesyncim/gopus) - Go package documentation
