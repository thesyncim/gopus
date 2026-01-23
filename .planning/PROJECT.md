# gopus

## What This Is

A pure Go implementation of the Opus audio codec, providing both encoder and decoder with a Go-idiomatic API. Built for WebRTC and real-time audio applications where cgo is undesirable. Frame-based core with io.Reader/Writer streaming wrappers.

## Core Value

Correct, pure-Go Opus encoding and decoding that passes official test vectors — no cgo, no external dependencies.

## Current Milestone: v1.1 Quality & Tech Debt Closure

**Goal:** Achieve RFC 8251 compliance (Q >= 0) by fixing decoder algorithm quality and encoder signal energy issues.

**Target features:**
- Decoder algorithm quality improvements to achieve Q >= 0 on test vectors
- SILK encoder signal energy fixes
- CELT encoder signal energy fixes
- Hybrid encoder signal energy fixes
- CELT 2.5/5/10ms frame synthesis improvements

## Requirements

### Validated (v1.0)

- [x] Decode Opus frames to PCM audio (int16 and float32)
- [x] Encode PCM audio to Opus frames (int16 and float32)
- [x] Support all three Opus modes: SILK (voice), CELT (audio), Hybrid
- [x] Support mono, stereo, and multistream channel configurations
- [x] Frame-based API for encode/decode operations
- [x] io.Reader/Writer streaming wrappers over frame-based core
- [x] Go-idiomatic API following standard library patterns
- [x] Zero cgo dependencies
- [x] Mode routing for SILK/CELT/Hybrid packets
- [x] Extended frame size support (2.5/5/10/20/40/60ms)

### Active

- [ ] Pass official Opus test vectors (RFC 8251 compliance, Q >= 0)
- [ ] Decoder algorithm quality improvements (SILK, CELT, Hybrid)
- [ ] Encoder signal energy improvements (SILK, CELT, Hybrid)
- [ ] CELT short frame synthesis (2.5ms, 5ms, 10ms)

### Out of Scope

- Assembly optimizations — deferred to v2 (amd64, arm64, arm, wasm)
- Performance parity with libopus — v1 targets usable performance, not maximum speed
- Ogg container format — focus on raw Opus frames
- Resampling — expect input at supported sample rates

## Context

**Reference implementation:** https://github.com/xiph/opus (C)

**Opus codec structure:**
- SILK: speech-optimized codec (8-24kHz), based on Skype's SILK
- CELT: MDCT-based audio codec (full bandwidth up to 48kHz)
- Hybrid: SILK for low frequencies + CELT for high frequencies
- Range coder for entropy coding throughout

**Test validation:** Official Opus test vectors from opus-codec.org define compliance. Passing these means the implementation correctly decodes/encodes per spec.

**Go ecosystem:** Existing opus packages (pion/opus, hraban/opus) use cgo. Pure Go implementation fills a gap for environments where cgo is problematic (cross-compilation, WASM, security constraints).

## Constraints

- **Language**: Pure Go only — no cgo, no assembly in v1
- **Compatibility**: Must produce/consume bitstreams compatible with libopus
- **Sample rates**: Support 8, 12, 16, 24, 48 kHz (Opus native rates)
- **Go version**: Target Go 1.21+ (generics available if useful)

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Pure Go first, assembly later | Correctness before optimization; assembly is architecture-specific maintenance burden | — Pending |
| Frame-based core with streaming wrappers | Matches Opus packet structure; streaming is convenience layer | — Pending |
| Both int16 and float32 | int16 is common, float32 is useful for DSP pipelines | — Pending |
| Test vectors as success criteria | Objective, spec-defined correctness measure | — Pending |

---
*Last updated: 2026-01-23 — Milestone v1.1 started*
