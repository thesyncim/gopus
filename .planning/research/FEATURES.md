# Features Research: gopus

**Domain:** Pure Go Opus Audio Codec Implementation
**Researched:** 2026-01-21
**Overall Confidence:** HIGH (based on RFC 6716, RFC 8251, RFC 7845, official Opus documentation)

## Executive Summary

This document maps the feature landscape for a pure Go Opus codec implementation. Features are categorized by compliance necessity (RFC requirements), Go-ecosystem value proposition, and implementation complexity. The Opus specification (RFC 6716) is unique in that **only the decoder is normatively defined** - encoders have significant implementation freedom as long as they produce decodable bitstreams.

---

## Table Stakes (Compliance Required)

Features required by the Opus specification (RFC 6716, RFC 8251). Missing any of these means the implementation is not spec-compliant.

### Decoder Requirements (Normative)

| Feature | RFC Reference | Complexity | Notes |
|---------|---------------|------------|-------|
| **Range Decoder** | RFC 6716 Section 4.1 | HIGH | Core entropy decoding; must be bit-exact |
| **SILK Mode Decoder** | RFC 6716 Section 4.2 | HIGH | Linear prediction for speech; NB/MB/WB bandwidths |
| **CELT Mode Decoder** | RFC 6716 Section 4.3 | HIGH | MDCT-based for music; all bandwidths |
| **Hybrid Mode Decoder** | RFC 6716 Section 3.2 | HIGH | SILK+CELT combined; SWB/FB bandwidths |
| **All Frame Sizes** | RFC 6716 Section 3.2.1 | MEDIUM | SILK: 10/20/40/60ms; CELT: 2.5/5/10/20ms |
| **All Audio Bandwidths** | RFC 6716 Table 1 | MEDIUM | NB(8k), MB(12k), WB(16k), SWB(24k), FB(48k) |
| **Mono and Stereo** | RFC 6716 Section 2.1.4 | MEDIUM | Mid-side stereo in SILK, intensity stereo in CELT |
| **Packet Parsing (TOC)** | RFC 6716 Section 3 | LOW | Table-of-contents byte parsing |
| **Code 0-3 Packets** | RFC 6716 Section 3.2 | MEDIUM | Single frame, two equal, two different, arbitrary |
| **Packet Loss Concealment** | RFC 6716 Section 4.2.6.4 | MEDIUM | Basic PLC when packets are lost |
| **Test Vector Compliance** | RFC 8251 | REQUIRED | Must pass official test vectors |

### Encoder Requirements (Functional)

While encoders aren't normatively specified, these are required for a functional implementation:

| Feature | Why Required | Complexity | Notes |
|---------|--------------|------------|-------|
| **Range Encoder** | Entropy coding for all data | HIGH | Inverse of range decoder |
| **SILK Mode Encoder** | Speech encoding | VERY HIGH | LPC analysis, pitch detection, quantization |
| **CELT Mode Encoder** | Music/general audio | VERY HIGH | MDCT, band energy, fine quantization |
| **Hybrid Mode Encoder** | Mixed content | VERY HIGH | Coordinate SILK and CELT layers |
| **Bitrate Control** | 6-510 kbps range | HIGH | VBR default, CBR option |
| **Mode Selection** | Auto, SILK, CELT, Hybrid | MEDIUM | Can be manual initially |

### Packet Format Requirements

| Requirement | RFC Reference | Notes |
|-------------|---------------|-------|
| Packets >= 1 byte | RFC 6716 R1 | Minimum packet size |
| Frame length <= 1275 bytes | RFC 6716 R2 | Maximum individual frame |
| Code 1 packets odd total length | RFC 6716 R3 | For equal frame split |
| Code 3 packets >= 2 bytes | RFC 6716 R6 | Minimum for frame count byte |
| Frame count M != 0 | RFC 6716 R4 | At least one frame |
| Total duration <= 120ms | RFC 6716 R5 | Maximum packet duration |

---

## Differentiators (Go-Specific Value)

Features that make a pure Go implementation valuable beyond just "another Opus library."

### Zero-CGO Architecture

| Value Proposition | Impact | Implementation Notes |
|-------------------|--------|---------------------|
| **No C dependencies** | Simplified deployment | No libopus, no pkg-config |
| **Single binary deployment** | DevOps simplicity | No shared library management |
| **Cross-compilation** | Easy multi-platform builds | GOOS/GOARCH only |
| **WASM support** | Browser deployment possible | Pure Go enables TinyGo/WASM |
| **Memory safety** | Eliminates C memory bugs | Go's garbage collector handles memory |

### Idiomatic Go API

| Feature | Why Valuable | Complexity |
|---------|--------------|------------|
| **io.Reader/io.Writer streaming** | Standard Go patterns | LOW |
| **context.Context support** | Cancellation and timeouts | LOW |
| **Error wrapping** | Go 1.13+ error handling | LOW |
| **Exported internals** | Bitstream analysis without full decode | MEDIUM |
| **Concurrent-safe design** | Multiple goroutines sharing encoder/decoder | MEDIUM |

### WebRTC/Real-Time Focus

| Feature | Why Valuable | Pion Integration |
|---------|--------------|------------------|
| **Frame-based API** | Natural fit for RTP packets | Direct compatibility |
| **Low latency paths** | Skip unnecessary copies | Zero-allocation encoding |
| **RTP timestamp handling** | 48kHz clock alignment | Built-in support |
| **FEC data extraction** | PLC recovery | Expose LBRR frames |

### Performance Transparency

| Feature | Why Valuable | Notes |
|---------|--------------|-------|
| **Benchmarks included** | Know what you're getting | Per-mode, per-bandwidth |
| **CPU/memory profiling** | Optimization guidance | pprof compatible |
| **Complexity knob effects documented** | Informed tradeoffs | Encoder complexity 0-10 |

---

## Optional/Extended Features

Nice to have but not required for initial compliance or core value.

### Extended Encoder Features

| Feature | Value | Complexity | When to Add |
|---------|-------|------------|-------------|
| **VBR/CBR toggle** | Bandwidth control | LOW | Phase 2 |
| **Constrained VBR** | Quality/size tradeoff | LOW | Phase 2 |
| **DTX (Discontinuous Transmission)** | Silence compression | MEDIUM | Phase 2-3 |
| **In-band FEC** | Loss recovery | MEDIUM | Phase 2-3 |
| **Packet loss percentage hint** | Encoder optimization | LOW | Phase 2 |
| **Bandwidth selection** | Manual bandwidth control | LOW | Phase 2 |
| **Signal type hint** | Voice/music optimization | LOW | Phase 2 |
| **Complexity setting** | CPU/quality tradeoff | LOW | Phase 2 |

### Multistream Support

| Feature | Value | Complexity | When to Add |
|---------|-------|------------|-------------|
| **RFC 7845 channel mapping** | Ogg Opus compatibility | MEDIUM | Phase 3 |
| **Up to 255 streams** | Surround sound | MEDIUM | Phase 3 |
| **Coupled/uncoupled streams** | Stereo pairs | MEDIUM | Phase 3 |
| **Ambisonics support** | Spatial audio | HIGH | Post-MVP |

### Container Support

| Feature | Value | Complexity | When to Add |
|---------|-------|------------|-------------|
| **Ogg reader/writer** | File format support | MEDIUM | Phase 3 |
| **WebM/Matroska** | Video container support | MEDIUM | Post-MVP |
| **Raw packet I/O** | RTP integration | LOW | Phase 1 |

### Repacketizer

| Feature | Value | Complexity | When to Add |
|---------|-------|------------|-------------|
| **Packet merging** | Combine frames | LOW | Phase 3 |
| **Packet splitting** | Extract frames | LOW | Phase 3 |
| **Padding operations** | Bitrate alignment | LOW | Phase 3 |

### Advanced Analysis

| Feature | Value | Complexity | When to Add |
|---------|-------|------------|-------------|
| **Bandwidth detection** | Encoder optimization | HIGH | Phase 3+ |
| **Voice activity detection** | DTX support | HIGH | Phase 3+ |
| **Speech/music classification** | Mode selection | HIGH | Phase 3+ |
| **Tonality estimation** | Quality improvement | HIGH | Post-MVP |

---

## Anti-Features (Defer/Exclude)

Features to explicitly NOT build in v1. Common scope-creep items.

### Exclude from v1 (High complexity, niche value)

| Anti-Feature | Why Exclude | Alternative |
|--------------|-------------|-------------|
| **Deep PLC (ML-based)** | Requires neural network runtime, 1.5MB model | Use basic PLC; add later if demanded |
| **DRED (Deep Redundancy)** | Requires ML inference, not yet standardized, model versioning complexity | Standard FEC is sufficient for v1 |
| **LACE/NoLACE** | ML-based enhancement, floating-point only | Out of scope for pure codec |
| **Floating-point analysis** | Complex, only needed for advanced encoder optimization | Fixed-point encoder is acceptable |
| **Custom neural network training** | Research-level work | Use reference if ever needed |

### Exclude Permanently (Out of scope)

| Anti-Feature | Why Exclude | What to Do Instead |
|--------------|-------------|-------------------|
| **Audio capture/playback** | OS-specific, CGO territory | Users bring their own I/O |
| **Resampling** | Separate concern | Use existing Go resamplers |
| **Audio effects/DSP** | Not codec's job | Separate library |
| **GUI/CLI tools** | Application layer | Provide library only |
| **Container format parsing (beyond Ogg)** | Scope creep | Use existing demuxers |

### Defer (Maybe later, not v1)

| Feature | Why Defer | Reconsider When |
|---------|-----------|-----------------|
| **SIMD optimizations** | Go's SIMD story is weak | Go adds better SIMD support |
| **Assembly routines** | Defeats "pure Go" value | Performance proves critical |
| **Surround sound (5.1, 7.1)** | Niche use case | Clear demand emerges |
| **Custom allocation** | Premature optimization | Profiling shows GC pressure |

---

## Feature Dependencies

Understanding which features unlock or require others.

```
Core Dependencies (must implement in order):
  Range Decoder/Encoder
         |
    +---------+
    |         |
  SILK      CELT
    |         |
    +---------+
         |
      Hybrid
         |
    All Modes Working
         |
    +----+----+
    |         |
   FEC      DTX
    |
  Multistream
```

### Detailed Dependencies

| Feature | Requires | Enables |
|---------|----------|---------|
| Range coder | Nothing | Everything else |
| SILK decoder | Range decoder | Hybrid decoder, basic FEC decode |
| CELT decoder | Range decoder | Hybrid decoder |
| Hybrid decoder | SILK + CELT decoders | Full bandwidth support |
| SILK encoder | Range encoder, LPC analysis | Speech encoding, FEC encoding |
| CELT encoder | Range encoder, MDCT | Music encoding |
| Hybrid encoder | SILK + CELT encoders | Optimal speech quality |
| In-band FEC | SILK encoder/decoder | Loss recovery |
| DTX | Full encoder | Bandwidth savings |
| Multistream | Full mono/stereo codec | Surround sound |
| Ogg support | Full codec | File format support |

---

## Complexity Assessment

Relative difficulty of each major feature area, based on RFC 6716 analysis and existing implementations.

### Decoder Complexity (in implementation order)

| Component | Complexity | LOC Estimate | Risk |
|-----------|------------|--------------|------|
| Range decoder | HIGH | 500-800 | Bit-exact requirements |
| Packet parsing | LOW | 200-300 | Well-specified |
| SILK decoder | VERY HIGH | 3000-5000 | LPC synthesis, pitch |
| CELT decoder | VERY HIGH | 3000-5000 | MDCT, band unpacking |
| Hybrid decoder | MEDIUM | 500-1000 | Coordination logic |
| PLC (basic) | MEDIUM | 500-800 | Extrapolation |
| Test vector validation | MEDIUM | 500-1000 | Tooling |

### Encoder Complexity

| Component | Complexity | LOC Estimate | Risk |
|-----------|------------|--------------|------|
| Range encoder | HIGH | 500-800 | Must match decoder |
| SILK encoder | EXTREME | 5000-8000 | LPC analysis, pitch detection |
| CELT encoder | EXTREME | 5000-8000 | MDCT, bit allocation |
| Hybrid encoder | HIGH | 1000-2000 | Mode coordination |
| VBR rate control | HIGH | 1000-2000 | Quality/bitrate balance |
| FEC encoder | MEDIUM | 500-1000 | LBRR frames |

### Estimated Total

| Milestone | Complexity | LOC Estimate |
|-----------|------------|--------------|
| MVP Decoder | VERY HIGH | 8,000-12,000 |
| MVP Encoder | EXTREME | 15,000-25,000 |
| Full v1 | EXTREME | 25,000-40,000 |

### Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Bit-exactness failures | HIGH | Blocks compliance | Extensive test vectors |
| Performance < realtime | MEDIUM | Limits use cases | Profile early, optimize hot paths |
| SILK complexity underestimated | HIGH | Schedule slip | Start with decoder, defer encoder |
| Edge cases in spec | MEDIUM | Bug reports | Fuzz testing |

---

## MVP Recommendation

For MVP, prioritize features that enable basic real-time audio:

### Phase 1: Decoder Foundation
1. Range decoder (bit-exact)
2. SILK decoder (NB/WB speech)
3. CELT decoder (all bandwidths)
4. Hybrid decoder
5. Basic packet parsing
6. Test vector compliance

### Phase 2: Encoder Foundation
1. Range encoder
2. SILK encoder (basic quality)
3. CELT encoder (basic quality)
4. Hybrid encoder
5. Frame-based API

### Phase 3: Production Readiness
1. io.Reader/io.Writer wrappers
2. VBR/CBR control
3. In-band FEC
4. Performance optimization
5. Multistream (mono/stereo)

### Defer to Post-MVP
- DTX
- Ogg container support
- Surround sound
- Advanced encoder analysis
- Any ML-based features

---

## Sources

### Primary (HIGH confidence)
- [RFC 6716: Definition of the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc6716)
- [RFC 8251: Updates to the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc8251)
- [RFC 7845: Ogg Encapsulation for Opus](https://datatracker.ietf.org/doc/html/rfc7845)
- [Opus Official Test Vectors](https://opus-codec.org/testvectors/)
- [Opus API Documentation](https://opus-codec.org/docs/opus_api-1.5/group__opus__encoderctls.html)

### Secondary (MEDIUM confidence)
- [Pion Opus Go Implementation](https://github.com/pion/opus) - Existing pure Go decoder
- [Concentus Multi-language Ports](https://github.com/lostromb/concentus) - C#/Java/Go ports
- [Opus Wikipedia](https://en.wikipedia.org/wiki/Opus_(audio_format))
- [Xiph Opus FAQ](https://wiki.xiph.org/OpusFAQ)

### Technical Papers
- [The Opus Codec (arXiv)](https://arxiv.org/pdf/1602.04845)
- [AES 135 Opus CELT Paper](https://jmvalin.ca/papers/aes135_opus_celt.pdf)
- [AES 135 Opus SILK Paper](https://jmvalin.ca/papers/aes135_opus_silk.pdf)
