# Stack Research: gopus

**Project:** Pure Go Opus Codec Implementation
**Researched:** 2026-01-21
**Go Version:** 1.25+

## Executive Summary

For a pure Go Opus codec implementation targeting WebRTC/real-time applications, the stack should prioritize:
1. **Zero external dependencies** - Only Go standard library
2. **Performance-conscious patterns** - Buffer reuse, careful allocation management
3. **Reference implementation compatibility** - Match libopus bit-for-bit where specified

The Go ecosystem has proven this is achievable - pion/opus demonstrates a working pure Go SILK decoder, and concentus shows full Opus ports to managed languages are feasible (at ~40-50% native performance).

---

## Recommended Stack

### Core Language & Runtime

| Technology | Version | Purpose | Rationale |
|------------|---------|---------|-----------|
| Go | 1.25+ | Language runtime | Latest generics improvements, SIMD experiment availability, performance optimizations |
| Standard library only | - | All implementation | Pure Go requirement eliminates cgo overhead and cross-compilation issues |

### Go Standard Library Packages (Critical)

| Package | Purpose | Usage Pattern |
|---------|---------|---------------|
| `math/bits` | Bit manipulation | Leading/trailing zeros, rotation, population count - compiler intrinsics for performance |
| `encoding/binary` | Byte order conversion | Little-endian PCM output, bitstream parsing |
| `math` | Floating-point operations | CELT MDCT, psychoacoustic model, when float path needed |
| `unsafe` | Zero-copy conversions | `[]byte` to `[]int16` for PCM output (use sparingly, encapsulate) |
| `sync` | Concurrency primitives | `sync.Pool` for buffer reuse in encoder/decoder |
| `io` | Streaming interfaces | `io.Reader`/`io.Writer` for frame streaming API |

### Go Standard Library Packages (Testing & Development)

| Package | Purpose | Usage Pattern |
|---------|---------|---------------|
| `testing` | Unit tests & benchmarks | Standard `*_test.go` files, benchmark with `go test -bench` |
| `testing/fstest` | Test file systems | Load test vectors without real filesystem |
| `runtime/pprof` | CPU/memory profiling | Profile hot paths: range coder, LPC, MDCT |

---

## Internal Package Structure (Recommended)

Based on pion/opus architecture and Opus RFC structure:

```
gopus/
├── decoder.go              # Public Decoder API
├── encoder.go              # Public Encoder API
├── internal/
│   ├── rangecoding/        # RFC 6716 Section 4.1 - Entropy coding
│   │   ├── decoder.go
│   │   └── encoder.go
│   ├── silk/               # SILK codec (voice)
│   │   ├── decoder.go
│   │   ├── encoder.go
│   │   ├── lpc.go          # Linear Predictive Coding
│   │   └── nlsf.go         # Normalized Line Spectral Frequencies
│   ├── celt/               # CELT codec (music/full-band)
│   │   ├── decoder.go
│   │   ├── encoder.go
│   │   ├── mdct.go         # Modified Discrete Cosine Transform
│   │   └── bands.go        # Bark-scale bands
│   ├── bitstream/          # Packet parsing, TOC byte handling
│   └── resampler/          # Sample rate conversion
├── pkg/
│   └── oggreader/          # OGG container support (optional)
└── testdata/               # Test vectors
```

**Rationale:** This mirrors RFC 6716 structure, making code reviewable against spec. Internal packages prevent API surface bloat while allowing component testing.

---

## Numeric Implementation Strategy

### Fixed-Point vs Floating-Point Decision

| Approach | Pros | Cons | Recommendation |
|----------|------|------|----------------|
| **Fixed-point (Q15/Q16)** | Bit-exact with reference, deterministic, no float precision issues | More complex code, requires careful overflow handling | **Use for SILK decoder** (reference uses fixed-point) |
| **Floating-point** | Simpler code, Go stdlib works with float64 | May have precision drift from reference | **Use for CELT** (reference allows both) |

**Implementation:**

```go
// Fixed-point Q15 operations (no external library needed)
const Q15_ONE = 1 << 15

func q15Mul(a, b int32) int32 {
    return int32((int64(a) * int64(b)) >> 15)
}

func q15Add(a, b int32) int32 {
    // Saturating add
    sum := int64(a) + int64(b)
    if sum > math.MaxInt32 { return math.MaxInt32 }
    if sum < math.MinInt32 { return math.MinInt32 }
    return int32(sum)
}
```

**Do NOT use external fixed-point libraries** (robaho/fixed, andreas-jonsson/fix16) - they add dependencies and aren't optimized for audio Q formats.

### Sample Format Support

| Format | Go Type | Notes |
|--------|---------|-------|
| S16LE (int16) | `[]int16` | Primary output, WebRTC native format |
| F32LE (float32) | `[]float32` | Secondary output, Web Audio API compatible |

Use `unsafe.Slice` for zero-copy conversion between `[]byte` and `[]int16`/`[]float32` in hot paths:

```go
// Zero-copy []byte to []int16 (11.5x faster than copy loop)
func bytesToInt16(b []byte) []int16 {
    return unsafe.Slice((*int16)(unsafe.Pointer(&b[0])), len(b)/2)
}
```

---

## Testing Stack

### Unit Testing

| Tool | Purpose | When to Use |
|------|---------|-------------|
| `go test` | Standard test runner | Always |
| `go test -race` | Race detection | Concurrent encoder/decoder tests |
| `go test -fuzz` | Fuzz testing | Bitstream parser, range coder |
| `go test -cover` | Coverage analysis | Ensure codec paths exercised |

### Codec Compliance Testing

| Resource | URL | Purpose |
|----------|-----|---------|
| RFC 8251 Test Vectors | https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz | Official compliance |
| RFC 6716 Test Vectors | https://opus-codec.org/testvectors/ | Legacy compliance |
| opusinfo tool | Part of opus-tools | Bitstream validation |

**Test Vector Strategy:**
```go
// Download and cache test vectors
func TestDecoder_OfficialVectors(t *testing.T) {
    vectors := loadTestVectors(t, "testdata/opus_testvectors")
    for _, v := range vectors {
        t.Run(v.Name, func(t *testing.T) {
            got := decode(v.Input)
            if !bytes.Equal(got, v.Expected) {
                t.Errorf("mismatch at frame %d", v.Frame)
            }
        })
    }
}
```

### Fuzz Testing

Critical for codec security - codecs process untrusted input:

```go
func FuzzDecode(f *testing.F) {
    // Seed with valid Opus packets
    f.Add(validOpusPacket1)
    f.Add(validOpusPacket2)

    f.Fuzz(func(t *testing.T, data []byte) {
        dec := NewDecoder(48000, 2)
        // Must not panic, must not allocate excessively
        dec.Decode(data)
    })
}
```

### Performance Testing

| Tool | Command | Purpose |
|------|---------|---------|
| Benchmark | `go test -bench=. -benchmem` | Measure allocations per decode |
| CPU Profile | `go test -bench=. -cpuprofile=cpu.pprof` | Find hot spots |
| Memory Profile | `go test -bench=. -memprofile=mem.pprof` | Find allocation sites |
| Trace | `go test -bench=. -trace=trace.out` | GC pauses, goroutine scheduling |

**Target Metrics:**
- Zero allocations per Decode() call (buffer reuse via sync.Pool)
- < 2ms for 20ms frame at 48kHz stereo (10x real-time minimum)

---

## Build & Cross-Compilation

### Standard Build

```bash
# Native build
go build ./...

# With race detector (development)
go build -race ./...

# With optimizations disabled (debugging)
go build -gcflags="-N -l" ./...
```

### Cross-Compilation Targets

| Target | Command | Use Case |
|--------|---------|----------|
| Linux AMD64 | `GOOS=linux GOARCH=amd64 go build` | Server deployment |
| Linux ARM64 | `GOOS=linux GOARCH=arm64 go build` | Raspberry Pi, ARM servers |
| macOS ARM64 | `GOOS=darwin GOARCH=arm64 go build` | Apple Silicon |
| Windows AMD64 | `GOOS=windows GOARCH=amd64 go build` | Windows desktop |
| WebAssembly | `GOOS=js GOARCH=wasm go build` | Browser deployment |

### WebAssembly Considerations

| Compiler | Binary Size | Performance | Use Case |
|----------|-------------|-------------|----------|
| Standard Go | ~2-5 MB | Good | Feature-complete |
| TinyGo | ~100-500 KB | Good (10% faster GC in 2025) | Size-constrained |

**WASM Build Notes:**
- Standard Go WASM: Full language support, larger binary
- TinyGo: Smaller binary, may have reflect/interface limitations
- Both support WASM 3.0 features (SIMD, threads) as of 2025

```bash
# Standard Go WASM
GOOS=js GOARCH=wasm go build -o gopus.wasm

# TinyGo (if size matters)
tinygo build -o gopus.wasm -target=wasm ./...
```

---

## Third-Party Libraries Assessment

### DO NOT USE (Anti-Recommendations)

| Library | Why Not |
|---------|---------|
| `hraban/opus` | CGo wrapper around libopus - violates pure Go requirement |
| `gopkg.in/hraban/opus.v2` | Same as above |
| Any CGo-based opus package | Cross-compilation issues, memory safety concerns |
| `robaho/fixed`, `fix16` | Adds dependency for simple Q-format math you can inline |
| `mjibson/go-dsp` | Overkill - only need specific FFT/MDCT, not general DSP |
| `gonum/dsp` | Heavy dependency for what you need |

### CONSIDER EXAMINING (Reference Only)

| Library | Why Examine | Confidence |
|---------|-------------|------------|
| [pion/opus](https://github.com/pion/opus) | Working pure Go SILK decoder, range coder implementation | HIGH |
| [lostromb/concentus](https://github.com/lostromb/concentus) | Full Opus port to Go (from C#/Java), fixed-point | MEDIUM |
| [zikichombo/dsp/lpc](https://pkg.go.dev/github.com/zikichombo/dsp/lpc) | LPC reference implementation patterns | LOW |

**Note:** Examine for patterns and algorithms, don't import as dependencies.

### USEFUL FOR TESTING ONLY

| Library | Purpose | Notes |
|---------|---------|-------|
| [go-audio/wav](https://github.com/go-audio/wav) | Test vector I/O | Development dependency only |
| [go-audio/audio](https://github.com/go-audio/audio) | Buffer format reference | Pattern reference, not runtime dependency |

---

## Performance Optimization Path

### Phase 1: Correct Implementation (No optimization)
- Clear, readable code matching RFC
- Full test coverage
- May allocate freely

### Phase 2: Allocation Reduction
- `sync.Pool` for decode buffers
- Pre-allocated scratch space in Decoder struct
- Target: 0 allocations per Decode()

### Phase 3: Hot Path Optimization
- Profile with pprof
- `math/bits` intrinsics for bit operations
- `unsafe` for zero-copy where measured benefit

### Phase 4: SIMD (Future/Optional)
- Go 1.25+ experimental `simd` package (GOEXPERIMENT=simd)
- Or hand-written assembly for critical paths (MDCT, LPC synthesis)
- **Only if profiling shows need**

```go
// Example: Future SIMD path for sample scaling
//go:build goexperiment.simd

import "simd"

func scaleSamples(dst, src []float32, gain float32) {
    // Use SIMD when available
    simd.MulScalarF32(dst, src, gain)
}
```

---

## Confidence Levels

| Area | Confidence | Reason |
|------|------------|--------|
| Go standard library selection | HIGH | math/bits, encoding/binary are well-documented, proven in production |
| No external dependencies approach | HIGH | pion/opus proves feasibility, matches project requirements |
| Fixed-point for SILK | HIGH | RFC specifies fixed-point, concentus confirms approach |
| Floating-point option for CELT | MEDIUM | RFC allows both, but may need fixed-point for bit-exactness |
| Testing with official vectors | HIGH | Official Opus project provides them, pion/opus uses them |
| WASM compatibility | MEDIUM | Standard Go WASM works, TinyGo may have edge cases |
| SIMD optimization | MEDIUM | Go SIMD experiment is new (2025), may stabilize |
| Performance targets (10x realtime) | MEDIUM | pion/opus achieves this for SILK, CELT unknown |

---

## Sources

### Official Documentation
- [Go math/bits package](https://pkg.go.dev/math/bits) - Compiler intrinsics for bit manipulation
- [Go Fuzzing](https://go.dev/doc/security/fuzz/) - Native fuzz testing
- [RFC 6716](https://tools.ietf.org/html/rfc6716) - Opus codec specification
- [Opus Test Vectors](https://opus-codec.org/testvectors/) - Official compliance tests

### Reference Implementations
- [pion/opus](https://github.com/pion/opus) - Pure Go SILK decoder (MIT license)
- [lostromb/concentus](https://github.com/lostromb/concentus) - Multi-language Opus port including Go

### Performance & Optimization
- [Go SIMD Proposal (Issue #73787)](https://github.com/golang/go/issues/73787) - Native SIMD intrinsics
- [Go unsafe package](https://leapcell.io/blog/go-unsafe-double-edged-sword-type-safety) - Performance patterns
- [Go pprof](https://go.dev/blog/pprof) - Profiling guide

### Audio Libraries (Reference)
- [go-audio/audio](https://pkg.go.dev/github.com/go-audio/audio) - Buffer format patterns
- [go-audio/wav](https://github.com/go-audio/wav) - WAV file handling for tests

### Build & Deployment
- [TinyGo](https://tinygo.org/) - Small WASM builds
- [WASM 3.0](https://asacrew.medium.com/wasm-3-0-standard-released-268c8f67ffe4) - Current WASM capabilities
