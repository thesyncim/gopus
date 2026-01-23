# Roadmap: gopus

## Overview

This roadmap transforms the gopus project from empty repository to full Opus codec implementation in pure Go. The journey starts with the foundational range coder (shared by all modes), builds decoders first (normative per RFC 6716, testable with official vectors), then encoders (non-normative, 2-3x more complex), and finishes with streaming API and container support. Each phase delivers a coherent, independently testable capability.

## Milestones

- ✅ **v1.0 MVP** - Phases 1-14 (shipped 2026-01-23)
- 🚧 **v1.1 Quality & Tech Debt Closure** - Phases 15-18 (in progress)

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3...): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

<details>
<summary>✅ v1.0 MVP (Phases 1-14) - SHIPPED 2026-01-23</summary>

### Phase 1: Foundation
**Goal**: Establish the entropy coding foundation that all Opus modes depend on
**Depends on**: Nothing (first phase)
**Requirements**: DEC-01, DEC-07, ENC-01, CMP-03, CMP-04
**Plans**: 3/3 complete

### Phase 2: SILK Decoder
**Goal**: Decode SILK-mode Opus packets (narrowband to wideband speech)
**Depends on**: Phase 1
**Requirements**: DEC-02, DEC-05, DEC-09, DEC-10
**Plans**: 5/5 complete

### Phase 3: CELT Decoder
**Goal**: Decode CELT-mode Opus packets (music and general audio)
**Depends on**: Phase 1
**Requirements**: DEC-03, DEC-06
**Plans**: 5/5 complete

### Phase 4: Hybrid Decoder
**Goal**: Decode Hybrid-mode packets and implement packet loss concealment
**Depends on**: Phase 2, Phase 3
**Requirements**: DEC-04, DEC-08
**Plans**: 3/3 complete

### Phase 5: Multistream Decoder
**Goal**: Decode multistream packets for surround sound configurations
**Depends on**: Phase 4
**Requirements**: DEC-11
**Plans**: 2/2 complete

### Phase 6: SILK Encoder
**Goal**: Encode PCM audio to SILK-mode Opus packets
**Depends on**: Phase 2
**Requirements**: ENC-02, ENC-07, ENC-08
**Plans**: 7/7 complete

### Phase 7: CELT Encoder
**Goal**: Encode PCM audio to CELT-mode Opus packets
**Depends on**: Phase 3
**Requirements**: ENC-03, CMP-02
**Plans**: 6/6 complete

### Phase 8: Hybrid Encoder & Controls
**Goal**: Complete encoder with hybrid mode and all encoder controls
**Depends on**: Phase 6, Phase 7
**Requirements**: ENC-04, ENC-05, ENC-06, ENC-10, ENC-11, ENC-12, ENC-13, ENC-14, ENC-15
**Plans**: 6/6 complete

### Phase 9: Multistream Encoder
**Goal**: Encode surround sound to multistream packets
**Depends on**: Phase 8
**Requirements**: ENC-09
**Plans**: 4/4 complete

### Phase 10: API Layer
**Goal**: Production-ready Go API with frame-based and streaming interfaces
**Depends on**: Phase 5, Phase 9
**Requirements**: API-01, API-02, API-03, API-04, API-05, API-06
**Plans**: 2/2 complete

### Phase 11: Container
**Goal**: Read and write Ogg Opus container format
**Depends on**: Phase 10
**Requirements**: CTR-01, CTR-02
**Plans**: 2/2 complete

### Phase 12: Compliance & Polish
**Goal**: Validate against official test vectors and finalize for release
**Depends on**: Phase 11
**Requirements**: CMP-01, CMP-02
**Plans**: 3/3 complete

### Phase 13: Multistream Public API
**Goal**: Expose multistream encoder/decoder for surround sound (5.1, 7.1) support
**Depends on**: Phase 10
**Plans**: 1/1 complete

### Phase 14: Extended Frame Size Support
**Goal**: Support all Opus frame sizes (2.5/5/10/20/40/60ms) for RFC 8251 test vector compliance
**Depends on**: Phase 3, Phase 4
**Requirements**: CMP-01
**Plans**: 5/5 complete

</details>

### v1.1 Quality & Tech Debt Closure (In Progress)

**Milestone Goal:** Achieve RFC 8251 compliance (Q >= 0) by fixing decoder algorithm quality and encoder signal energy issues.

#### Phase 15: CELT Decoder Quality
**Goal**: Fix CELT decoder algorithm issues to achieve reference-matching output
**Depends on**: Phase 14
**Requirements**: DEQ-02, FRM-01, FRM-02, FRM-03
**Success Criteria** (what must be TRUE):
  1. CELT decoder output correlates with reference audio (energy ratio > 50%)
  2. CELT 2.5ms frames synthesize without audible artifacts
  3. CELT 5ms frames synthesize without audible artifacts
  4. CELT 10ms frames synthesize without audible artifacts
  5. CELT-only test vectors achieve Q >= 0 threshold
**Plans**: 5 plans

Plans:
- [ ] 15-01-PLAN.md — Fix coarse energy prediction coefficients (BetaCoef)
- [ ] 15-02-PLAN.md — Fix range decoder integration for Laplace decoding
- [ ] 15-03-PLAN.md — Verify and fix denormalization formula
- [ ] 15-04-PLAN.md — Verify IMDCT synthesis for all CELT frame sizes
- [ ] 15-05-PLAN.md — Frame-size-specific testing and quality validation

#### Phase 16: SILK Decoder Quality
**Goal**: Fix SILK decoder algorithm issues to achieve reference-matching output
**Depends on**: Phase 15
**Requirements**: DEQ-01
**Success Criteria** (what must be TRUE):
  1. SILK decoder output correlates with reference audio (energy ratio > 50%)
  2. SILK LPC synthesis produces stable, non-exploding output
  3. SILK excitation reconstruction matches reference signal characteristics
  4. SILK-only test vectors achieve Q >= 0 threshold
**Plans**: TBD

Plans:
- [ ] 16-01: TBD
- [ ] 16-02: TBD

#### Phase 17: Hybrid Decoder & Compliance
**Goal**: Fix Hybrid decoder coordination and achieve full RFC 8251 compliance
**Depends on**: Phase 15, Phase 16
**Requirements**: DEQ-03, DEQ-04
**Success Criteria** (what must be TRUE):
  1. Hybrid decoder correctly combines SILK and CELT outputs at crossover frequency
  2. Hybrid SILK-CELT timing alignment produces no phase artifacts
  3. All 12 RFC 8251 test vectors pass with Q >= 0 threshold
  4. No regression in SILK-only or CELT-only vector results
**Plans**: TBD

Plans:
- [ ] 17-01: TBD
- [ ] 17-02: TBD

#### Phase 18: Encoder Quality
**Goal**: Fix encoder signal energy preservation for round-trip testing
**Depends on**: Phase 17
**Requirements**: ENQ-01, ENQ-02, ENQ-03, ENQ-04
**Success Criteria** (what must be TRUE):
  1. SILK encoder round-trip preserves >10% signal energy
  2. CELT encoder round-trip preserves >10% signal energy
  3. Hybrid encoder round-trip produces non-zero output
  4. Multistream encoder internal round-trip produces non-zero output
  5. Encoded audio decoded by libopus produces >10% energy ratio
**Plans**: TBD

Plans:
- [ ] 18-01: TBD
- [ ] 18-02: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> ... -> 14 -> 15 -> 16 -> 17 -> 18

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1. Foundation | v1.0 | 3/3 | Complete | 2026-01-21 |
| 2. SILK Decoder | v1.0 | 5/5 | Complete | 2026-01-21 |
| 3. CELT Decoder | v1.0 | 5/5 | Complete | 2026-01-21 |
| 4. Hybrid Decoder | v1.0 | 3/3 | Complete | 2026-01-22 |
| 5. Multistream Decoder | v1.0 | 2/2 | Complete | 2026-01-22 |
| 6. SILK Encoder | v1.0 | 7/7 | Complete | 2026-01-22 |
| 7. CELT Encoder | v1.0 | 6/6 | Complete | 2026-01-22 |
| 8. Hybrid Encoder & Controls | v1.0 | 6/6 | Complete | 2026-01-22 |
| 9. Multistream Encoder | v1.0 | 4/4 | Complete | 2026-01-22 |
| 10. API Layer | v1.0 | 2/2 | Complete | 2026-01-22 |
| 11. Container | v1.0 | 2/2 | Complete | 2026-01-22 |
| 12. Compliance & Polish | v1.0 | 3/3 | Complete | 2026-01-22 |
| 13. Multistream Public API | v1.0 | 1/1 | Complete | 2026-01-23 |
| 14. Extended Frame Size Support | v1.0 | 5/5 | Complete | 2026-01-23 |
| 15. CELT Decoder Quality | v1.1 | 0/5 | Ready | - |
| 16. SILK Decoder Quality | v1.1 | 0/? | Not started | - |
| 17. Hybrid Decoder & Compliance | v1.1 | 0/? | Not started | - |
| 18. Encoder Quality | v1.1 | 0/? | Not started | - |

---
*Roadmap created: 2026-01-21*
*v1.0 shipped: 2026-01-23 (14 phases, 54 plans)*
*v1.1 roadmap added: 2026-01-23 (4 phases, TBD plans)*
