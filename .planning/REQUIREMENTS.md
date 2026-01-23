# Requirements: gopus

**Defined:** 2026-01-21
**Core Value:** Correct, pure-Go Opus encoding and decoding that passes official test vectors — no cgo, no external dependencies.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Decoder - Core

- [x] **DEC-01**: Implement bit-exact range decoder per RFC 6716 Section 4.1
- [x] **DEC-02**: Decode SILK mode frames (NB/MB/WB bandwidths)
- [x] **DEC-03**: Decode CELT mode frames (all bandwidths up to 48kHz)
- [x] **DEC-04**: Decode Hybrid mode frames (SILK + CELT combined)
- [x] **DEC-05**: Support all SILK frame sizes (10/20/40/60ms)
- [x] **DEC-06**: Support all CELT frame sizes (2.5/5/10/20ms)
- [x] **DEC-07**: Parse TOC byte and handle Code 0-3 packet formats
- [x] **DEC-08**: Implement basic packet loss concealment (PLC)

### Decoder - Channels

- [x] **DEC-09**: Decode mono streams
- [x] **DEC-10**: Decode stereo streams (mid-side SILK, intensity CELT)
- [x] **DEC-11**: Decode multistream packets (coupled/uncoupled streams)

### Encoder - Core

- [x] **ENC-01**: Implement range encoder matching decoder
- [x] **ENC-02**: Encode SILK mode frames (speech)
- [x] **ENC-03**: Encode CELT mode frames (audio)
- [x] **ENC-04**: Encode Hybrid mode frames
- [x] **ENC-05**: Support all frame sizes matching decoder
- [x] **ENC-06**: Support all bandwidths (NB through FB)

### Encoder - Channels

- [x] **ENC-07**: Encode mono streams
- [x] **ENC-08**: Encode stereo streams
- [x] **ENC-09**: Encode multistream packets

### Encoder - Controls

- [x] **ENC-10**: VBR mode (default)
- [x] **ENC-11**: CBR mode option
- [x] **ENC-12**: Bitrate control (6-510 kbps range)
- [x] **ENC-13**: Complexity setting (0-10)
- [x] **ENC-14**: In-band FEC encoding
- [x] **ENC-15**: DTX (discontinuous transmission)

### API

- [x] **API-01**: Frame-based Encoder type with Encode() method
- [x] **API-02**: Frame-based Decoder type with Decode() method
- [x] **API-03**: io.Writer wrapper for streaming encode
- [x] **API-04**: io.Reader wrapper for streaming decode
- [x] **API-05**: int16 PCM sample format support
- [x] **API-06**: float32 PCM sample format support

### Container

- [x] **CTR-01**: Ogg Opus file reader
- [x] **CTR-02**: Ogg Opus file writer

### Compliance

- [ ] **CMP-01**: Pass official Opus decoder test vectors
- [x] **CMP-02**: Produce bitstreams decodable by libopus
- [x] **CMP-03**: Zero cgo dependencies
- [x] **CMP-04**: Go 1.21+ compatibility

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Performance Optimization

- **PERF-01**: Assembly optimizations for amd64 (SIMD)
- **PERF-02**: Assembly optimizations for arm64 (NEON)
- **PERF-03**: Assembly optimizations for arm (32-bit)
- **PERF-04**: WASM-optimized build
- **PERF-05**: Zero-allocation encode/decode paths

### Extended Features

- **EXT-01**: Surround sound (5.1, 7.1 channel layouts)
- **EXT-02**: Ambisonics support
- **EXT-03**: WebM/Matroska container support
- **EXT-04**: Repacketizer (merge/split packets)
- **EXT-05**: Bandwidth detection
- **EXT-06**: Voice activity detection
- **EXT-07**: Speech/music classification

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Deep PLC (ML-based) | Requires neural network runtime, 1.5MB model |
| DRED (Deep Redundancy) | Requires ML inference, not yet standardized |
| LACE/NoLACE | ML-based enhancement, floating-point only |
| Audio capture/playback | OS-specific, would require cgo |
| Resampling | Separate concern, use existing Go resamplers |
| Audio effects/DSP | Not codec's job |
| GUI/CLI tools | Library only, applications built separately |

## Traceability

Which phases cover which requirements.

| Requirement | Phase | Status |
|-------------|-------|--------|
| DEC-01 | Phase 1: Foundation | Complete |
| DEC-02 | Phase 2: SILK Decoder | Complete |
| DEC-03 | Phase 3: CELT Decoder | Complete |
| DEC-04 | Phase 4: Hybrid Decoder | Complete |
| DEC-05 | Phase 2: SILK Decoder | Complete |
| DEC-06 | Phase 3: CELT Decoder | Complete |
| DEC-07 | Phase 1: Foundation | Complete |
| DEC-08 | Phase 4: Hybrid Decoder | Complete |
| DEC-09 | Phase 2: SILK Decoder | Complete |
| DEC-10 | Phase 2: SILK Decoder | Complete |
| DEC-11 | Phase 5: Multistream Decoder | Complete |
| ENC-01 | Phase 1: Foundation | Complete |
| ENC-02 | Phase 6: SILK Encoder | Complete |
| ENC-03 | Phase 7: CELT Encoder | Complete |
| ENC-04 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-05 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-06 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-07 | Phase 6: SILK Encoder | Complete |
| ENC-08 | Phase 6: SILK Encoder | Complete |
| ENC-09 | Phase 9: Multistream Encoder | Complete |
| ENC-10 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-11 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-12 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-13 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-14 | Phase 8: Hybrid Encoder & Controls | Complete |
| ENC-15 | Phase 8: Hybrid Encoder & Controls | Complete |
| API-01 | Phase 10: API Layer | Complete |
| API-02 | Phase 10: API Layer | Complete |
| API-03 | Phase 10: API Layer | Complete |
| API-04 | Phase 10: API Layer | Complete |
| API-05 | Phase 10: API Layer | Complete |
| API-06 | Phase 10: API Layer | Complete |
| CTR-01 | Phase 11: Container | Complete |
| CTR-02 | Phase 11: Container | Complete |
| CMP-01 | Phase 12: Compliance & Polish | Pending |
| CMP-02 | Phase 12: Compliance & Polish | Pending |
| CMP-03 | Phase 1: Foundation | Complete |
| CMP-04 | Phase 1: Foundation | Complete |

**Coverage:**
- v1 requirements: 38 total
- Mapped to phases: 38
- Unmapped: 0

---
*Requirements defined: 2026-01-21*
*Last updated: 2026-01-22 after Phase 11 Container completion*
