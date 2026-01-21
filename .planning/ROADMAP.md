# Roadmap: gopus

## Overview

This roadmap transforms the gopus project from empty repository to full Opus codec implementation in pure Go. The journey starts with the foundational range coder (shared by all modes), builds decoders first (normative per RFC 6716, testable with official vectors), then encoders (non-normative, 2-3x more complex), and finishes with streaming API and container support. Each phase delivers a coherent, independently testable capability.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3...): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Foundation** - Range coder, packet parsing, test infrastructure
- [ ] **Phase 2: SILK Decoder** - Decode SILK mode frames (speech)
- [ ] **Phase 3: CELT Decoder** - Decode CELT mode frames (music/audio)
- [ ] **Phase 4: Hybrid Decoder** - Combined SILK+CELT decoding, PLC
- [ ] **Phase 5: Multistream Decoder** - Surround sound decoding
- [ ] **Phase 6: SILK Encoder** - Encode speech to SILK frames
- [ ] **Phase 7: CELT Encoder** - Encode audio to CELT frames
- [ ] **Phase 8: Hybrid Encoder & Controls** - Full encoder with bitrate/VBR/FEC/DTX
- [ ] **Phase 9: Multistream Encoder** - Surround sound encoding
- [ ] **Phase 10: API Layer** - Frame-based API and io.Reader/Writer wrappers
- [ ] **Phase 11: Container** - Ogg Opus file read/write
- [ ] **Phase 12: Compliance & Polish** - Test vectors, cross-validation, documentation

## Phase Details

### Phase 1: Foundation
**Goal**: Establish the entropy coding foundation that all Opus modes depend on
**Depends on**: Nothing (first phase)
**Requirements**: DEC-01, DEC-07, ENC-01, CMP-03, CMP-04
**Success Criteria** (what must be TRUE):
  1. Range decoder correctly decodes symbols using probability tables
  2. Range encoder produces output decodable by range decoder (round-trip)
  3. TOC byte parsed correctly to extract mode, bandwidth, frame size, stereo flag
  4. Packet frame count codes 0-3 correctly parsed with frame lengths
  5. Project builds with zero cgo dependencies on Go 1.21+
**Plans**: 3 plans

Plans:
- [x] 01-01-PLAN.md - Range decoder implementation (RFC 6716 Section 4.1)
- [x] 01-02-PLAN.md - Range encoder implementation with round-trip validation
- [x] 01-03-PLAN.md - TOC byte and packet frame parsing

### Phase 2: SILK Decoder
**Goal**: Decode SILK-mode Opus packets (narrowband to wideband speech)
**Depends on**: Phase 1
**Requirements**: DEC-02, DEC-05, DEC-09, DEC-10
**Success Criteria** (what must be TRUE):
  1. SILK mono frames decode to audible speech at NB/MB/WB bandwidths
  2. All SILK frame sizes (10/20/40/60ms) decode correctly
  3. SILK stereo frames decode with correct mid-side unmixing
  4. SILK decoder state persists correctly across frames (no artifacts at boundaries)
**Plans**: 5 plans

Plans:
- [ ] 02-01-PLAN.md - SILK tables, codebook, and decoder struct foundation
- [ ] 02-02-PLAN.md - Parameter decoding (frame type, gains, LSF/LPC, pitch/LTP)
- [ ] 02-03-PLAN.md - Excitation reconstruction and LPC/LTP synthesis
- [ ] 02-04-PLAN.md - Stereo decoding and frame orchestration
- [ ] 02-05-PLAN.md - Public API, resampling to 48kHz, and integration tests

### Phase 3: CELT Decoder
**Goal**: Decode CELT-mode Opus packets (music and general audio)
**Depends on**: Phase 1
**Requirements**: DEC-03, DEC-06
**Success Criteria** (what must be TRUE):
  1. CELT mono frames decode to audible audio at all bandwidths (NB to FB)
  2. All CELT frame sizes (2.5/5/10/20ms) decode correctly
  3. CELT stereo frames decode with correct intensity stereo handling
  4. Transient frames (short MDCT blocks) decode without artifacts
**Plans**: TBD

Plans:
- [ ] 03-01: TBD (CELT tables, energy decoding)
- [ ] 03-02: TBD (PVQ decoding, CWRS)
- [ ] 03-03: TBD (IMDCT, band folding, stereo)

### Phase 4: Hybrid Decoder
**Goal**: Decode Hybrid-mode packets and implement packet loss concealment
**Depends on**: Phase 2, Phase 3
**Requirements**: DEC-04, DEC-08
**Success Criteria** (what must be TRUE):
  1. Hybrid mode frames decode with combined SILK (0-8kHz) and CELT (8-20kHz) output
  2. Hybrid 10ms and 20ms frames decode correctly (only supported sizes)
  3. SILK output correctly upsampled and summed with CELT output
  4. Packet loss concealment produces reasonable audio when packet is NULL
**Plans**: TBD

Plans:
- [ ] 04-01: TBD (resampling, delay compensation)
- [ ] 04-02: TBD (hybrid coordination, PLC)

### Phase 5: Multistream Decoder
**Goal**: Decode multistream packets for surround sound configurations
**Depends on**: Phase 4
**Requirements**: DEC-11
**Success Criteria** (what must be TRUE):
  1. Multistream packets with coupled stereo streams decode correctly
  2. Multistream packets with uncoupled mono streams decode correctly
  3. Channel mapping table correctly routes streams to output channels
  4. All streams in packet decoded with consistent timing
**Plans**: TBD

Plans:
- [ ] 05-01: TBD (multistream parsing, channel mapping)

### Phase 6: SILK Encoder
**Goal**: Encode PCM audio to SILK-mode Opus packets
**Depends on**: Phase 2
**Requirements**: ENC-02, ENC-07, ENC-08
**Success Criteria** (what must be TRUE):
  1. SILK encoder produces packets decodable by Phase 2 SILK decoder
  2. SILK encoder produces packets decodable by libopus (cross-validation)
  3. Encoded speech is intelligible at target bitrates
  4. Mono and stereo encoding both produce valid output
**Plans**: TBD

Plans:
- [ ] 06-01: TBD (LPC analysis, LSF quantization)
- [ ] 06-02: TBD (pitch detection, LTP)
- [ ] 06-03: TBD (excitation coding, stereo encoding)

### Phase 7: CELT Encoder
**Goal**: Encode PCM audio to CELT-mode Opus packets
**Depends on**: Phase 3
**Requirements**: ENC-03
**Success Criteria** (what must be TRUE):
  1. CELT encoder produces packets decodable by Phase 3 CELT decoder
  2. CELT encoder produces packets decodable by libopus (cross-validation)
  3. Encoded audio is perceptually acceptable at target bitrates
  4. Transient detection triggers short MDCT blocks when appropriate
**Plans**: TBD

Plans:
- [ ] 07-01: TBD (MDCT analysis, energy quantization)
- [ ] 07-02: TBD (PVQ encoding, bit allocation)
- [ ] 07-03: TBD (transient detection, stereo)

### Phase 8: Hybrid Encoder & Controls
**Goal**: Complete encoder with hybrid mode and all encoder controls
**Depends on**: Phase 6, Phase 7
**Requirements**: ENC-04, ENC-05, ENC-06, ENC-10, ENC-11, ENC-12, ENC-13, ENC-14, ENC-15
**Success Criteria** (what must be TRUE):
  1. Hybrid mode encoder produces valid SWB/FB speech packets
  2. VBR mode produces variable-size packets based on content complexity
  3. CBR mode produces consistent packet sizes within tolerance
  4. Bitrate control respects target (6-510 kbps range)
  5. In-band FEC encodes redundant data for loss recovery
**Plans**: TBD

Plans:
- [ ] 08-01: TBD (hybrid encoder coordination)
- [ ] 08-02: TBD (VBR/CBR, bitrate control)
- [ ] 08-03: TBD (FEC, DTX, complexity)

### Phase 9: Multistream Encoder
**Goal**: Encode surround sound to multistream packets
**Depends on**: Phase 8
**Requirements**: ENC-09
**Success Criteria** (what must be TRUE):
  1. Multistream encoder produces packets decodable by Phase 5 decoder
  2. Coupled stereo streams share appropriate cross-channel information
  3. Channel mapping correctly routes input channels to streams
**Plans**: TBD

Plans:
- [ ] 09-01: TBD (multistream encoding, channel coupling)

### Phase 10: API Layer
**Goal**: Production-ready Go API with frame-based and streaming interfaces
**Depends on**: Phase 5, Phase 9
**Requirements**: API-01, API-02, API-03, API-04, API-05, API-06
**Success Criteria** (what must be TRUE):
  1. Decoder.Decode() accepts packet bytes and returns PCM samples
  2. Encoder.Encode() accepts PCM samples and returns packet bytes
  3. io.Reader wraps decoder for streaming decode of packet sequences
  4. io.Writer wraps encoder for streaming encode to packet sequences
  5. Both int16 and float32 sample formats work correctly
**Plans**: TBD

Plans:
- [ ] 10-01: TBD (frame-based encoder/decoder API)
- [ ] 10-02: TBD (io.Reader/Writer wrappers)

### Phase 11: Container
**Goal**: Read and write Ogg Opus container format
**Depends on**: Phase 10
**Requirements**: CTR-01, CTR-02
**Success Criteria** (what must be TRUE):
  1. Ogg Opus files created by FFmpeg/libopus can be read and decoded
  2. Encoded audio can be written to Ogg Opus files playable by standard players
  3. OpusHead and OpusTags headers correctly parsed/written per RFC 7845
**Plans**: TBD

Plans:
- [ ] 11-01: TBD (Ogg page parsing)
- [ ] 11-02: TBD (Opus headers, Ogg writing)

### Phase 12: Compliance & Polish
**Goal**: Validate against official test vectors and finalize for release
**Depends on**: Phase 11
**Requirements**: CMP-01, CMP-02
**Success Criteria** (what must be TRUE):
  1. Decoder passes all official RFC 8251 test vectors
  2. Encoder output is decodable by libopus without errors
  3. Zero cgo dependencies verified in final build
  4. API documentation complete with examples
**Plans**: TBD

Plans:
- [ ] 12-01: TBD (test vector validation)
- [ ] 12-02: TBD (cross-implementation testing, documentation)

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8 -> 9 -> 10 -> 11 -> 12

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation | 3/3 | Complete | 2026-01-21 |
| 2. SILK Decoder | 0/5 | Planned | - |
| 3. CELT Decoder | 0/3 | Not started | - |
| 4. Hybrid Decoder | 0/2 | Not started | - |
| 5. Multistream Decoder | 0/1 | Not started | - |
| 6. SILK Encoder | 0/3 | Not started | - |
| 7. CELT Encoder | 0/3 | Not started | - |
| 8. Hybrid Encoder & Controls | 0/3 | Not started | - |
| 9. Multistream Encoder | 0/1 | Not started | - |
| 10. API Layer | 0/2 | Not started | - |
| 11. Container | 0/2 | Not started | - |
| 12. Compliance & Polish | 0/2 | Not started | - |

---
*Roadmap created: 2026-01-21*
*Phase 1 planned: 2026-01-21*
*Phase 2 planned: 2026-01-21*
*Total phases: 12 | Total plans: ~30*
