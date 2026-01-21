# gopus

## What This Is

A pure Go implementation of the Opus audio codec, providing both encoder and decoder with a Go-idiomatic API. Built for WebRTC and real-time audio applications where cgo is undesirable. Frame-based core with io.Reader/Writer streaming wrappers.

## Core Value

Correct, pure-Go Opus encoding and decoding that passes official test vectors — no cgo, no external dependencies.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Decode Opus frames to PCM audio (int16 and float32)
- [ ] Encode PCM audio to Opus frames (int16 and float32)
- [ ] Support all three Opus modes: SILK (voice), CELT (audio), Hybrid
- [ ] Support mono, stereo, and multistream channel configurations
- [ ] Frame-based API for encode/decode operations
- [ ] io.Reader/Writer streaming wrappers over frame-based core
- [ ] Pass official Opus test vectors
- [ ] Go-idiomatic API following standard library patterns
- [ ] Zero cgo dependencies

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
*Last updated: 2026-01-21 after initialization*
