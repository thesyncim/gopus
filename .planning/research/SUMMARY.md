# Project Research Summary

**Project:** gopus
**Domain:** Audio codec implementation (pure Go)
**Researched:** 2026-01-21
**Confidence:** HIGH

## Executive Summary

This project aims to build a pure Go implementation of the Opus audio codec (RFC 6716) targeting WebRTC and real-time audio applications. Opus is a hybrid codec combining SILK (speech) and CELT (music/general audio) modes, unified by a shared range coder. The research reveals this is a complex but achievable undertaking, with existing proof points: pion/opus demonstrates a working pure Go SILK decoder, and concentus shows full Opus ports to managed languages achieve 40-50% of native performance.

The recommended approach is decoder-first implementation starting with the range coder foundation, followed by SILK and CELT decoders independently, then hybrid mode integration. The critical constraint is that **the decoder is normative** - RFC 6716 prose is secondary to the reference C implementation's actual behavior, and compliance requires bit-exact range decoder state matching, not just correct-sounding audio. Budget 25,000-40,000 lines of code with significant fixed-point arithmetic complexity.

Key risks include underestimating SILK complexity (years-long effort for pion/opus despite appearing simpler than CELT), numeric precision mismatches between C and Go (integer overflow, Q-format conversions), and performance limitations (pure Go caps at 40-50% of libopus without SIMD/assembly). Mitigate by treating libopus source as ground truth, implementing comprehensive test vector validation from day one, and accepting performance trade-offs inherent in the pure Go constraint.

## Key Findings

### Recommended Stack

The stack should be minimalist, relying exclusively on Go standard library to maintain zero-CGO compatibility and cross-compilation capabilities. This enables single-binary deployment, WebAssembly compilation (both standard Go and TinyGo), and eliminates C memory safety concerns. The trade-off is accepting 40-50% of native libopus performance as the achievable ceiling without assembly optimization.

**Core technologies:**
- **Go 1.25+ standard library only**: Eliminates external dependencies while leveraging latest generics, SIMD experiment availability, and performance optimizations
- **math/bits package**: Provides compiler intrinsics for bit manipulation (leading/trailing zeros, rotation) critical to range coding performance
- **unsafe package (sparingly)**: Enables zero-copy conversions between []byte and []int16/[]float32 for PCM data (11.5x faster than copy loops)
- **sync.Pool for buffer reuse**: Target zero allocations per Decode() call to avoid GC pauses in real-time audio paths
- **testing package with fuzz support**: Native fuzzing critical for codec security (processes untrusted bitstreams)
- **Official RFC 8251 test vectors**: Bit-exact compliance validation required by specification

**Build approach:**
- Standard `go build` with cross-compilation via GOOS/GOARCH
- WebAssembly targets: standard Go (~2-5 MB) or TinyGo (~100-500 KB)
- No external libraries (examining pion/opus and concentus for patterns only, not importing)

### Expected Features

The Opus specification uniquely defines only the decoder normatively - encoders have implementation freedom as long as they produce decodable bitstreams. This creates asymmetric complexity: decoders must be bit-exact, encoders just need to be "good enough."

**Must have (compliance required):**
- Range decoder/encoder (RFC 6716 Section 4.1) - bit-exact entropy coding
- SILK decoder - speech coding at NB/MB/WB bandwidths, all frame sizes (10/20/40/60ms)
- CELT decoder - MDCT-based music coding, all bandwidths including NB/WB/SWB/FB
- Hybrid mode decoder - SILK+CELT for super-wideband/fullband speech
- Mono and stereo support with mid-side/intensity stereo
- All packet structures (codes 0-3: single frame, two equal, two different, arbitrary count)
- Packet loss concealment (basic extrapolation per RFC 6716 Section 4.2.6.4)
- Test vector compliance (RFC 8251) - quality metric >90 for 48kHz, >48 dB SNR minimum

**Should have (production readiness):**
- SILK encoder (basic quality) - enables speech encoding, significantly harder than decoder
- CELT encoder (basic quality) - enables music encoding, extremely complex
- VBR/CBR bitrate control (6-510 kbps range)
- In-band FEC (Forward Error Correction) for loss recovery
- Frame-based API for RTP packet integration
- io.Reader/io.Writer streaming wrappers for idiomatic Go patterns

**Defer (v2+):**
- DTX (Discontinuous Transmission) for silence compression
- Ogg container support (RFC 7845) - explicitly out of scope per PROJECT.md
- Multistream (surround sound 5.1/7.1) - niche use case
- Advanced encoder analysis (bandwidth detection, VAD, tonality estimation)
- Deep PLC/DRED (ML-based loss concealment) - requires neural network runtime
- SIMD optimizations - Go's SIMD story weak, defeats "pure Go" value, defer until clear demand

### Architecture Approach

Opus is a hybrid codec with three operating modes: SILK-only (low-bitrate speech), CELT-only (music/low-delay), and Hybrid (SILK for 0-8kHz + CELT for 8-20kHz at SWB/FB). All modes share a common range coder for entropy coding. The architecture must cleanly separate these components while allowing state sharing where needed (stereo prediction, mode transitions).

**Major components:**
1. **Range Coder (foundation)** - Arithmetic coding using 32-bit integer math exclusively, bit-exact requirements, raw bits bypass for error resilience. Must implement state tracking to validate against reference decoder.
2. **SILK Codec** - Linear Predictive Coding (LPC) with noise feedback, pitch analysis, LSF quantization, 5-tap LTP filters. Operates at internal sample rates 8/12/16 kHz with fixed-point Q15/Q16 arithmetic. More complex than it appears: 4 frame types, stereo state persistence, careful overflow handling.
3. **CELT Codec** - Modified Discrete Cosine Transform (MDCT) with ~21 Bark-scale bands, Pyramid Vector Quantization (PVQ), coarse+fine energy quantization. Floating-point acceptable (unlike SILK's fixed-point requirement). Band folding reconstructs uncoded high frequencies.
4. **Hybrid Mode Coordinator** - Downsamples to 16kHz for SILK (WB), runs full 48kHz CELT with bands above 8kHz zeroed, synchronizes with 2.7ms delay compensation, sums outputs. Only supports 10/20ms frames.
5. **Packet Parser** - TOC byte decoding (config/stereo/frame count), frame length extraction, routing to correct decoder mode.
6. **Resampler** - Polyphase filters for 8/12/16/24/48 kHz conversion, needed for hybrid mode and API flexibility.

**Package structure:**
```
gopus/
├── opus.go, encoder.go, decoder.go    # Public API
├── packet.go                           # TOC parsing
├── internal/
│   ├── rangecoding/                    # Shared entropy coder
│   ├── silk/                           # LPC, LSF, LTP, excitation
│   ├── celt/                           # MDCT, bands, PVQ
│   ├── hybrid/                         # Mode coordination
│   └── resample/                       # Sample rate conversion
└── testdata/vectors/                   # RFC 8251 test vectors
```

### Critical Pitfalls

1. **Treating RFC prose as authoritative over reference code** - RFC 6716 explicitly states when description contradicts libopus source, source wins. Implement by keeping libopus C code open during all development, not just the RFC PDF. Test against reference decoder state continuously.

2. **Ignoring range decoder final state validation** - Producing correct-sounding audio but wrong internal state is non-compliant. RFC requires "MUST have the same final range decoder state as that of the reference decoder." Implement ec_tell() comparison from day one, not as afterthought.

3. **Underestimating SILK complexity** - pion/opus has been working on SILK for years with open issues for relative lag, stereo frames, LPC gain limiting. It's not simpler than CELT despite appearances. Budget 2-3x expected time, implement all 4 frame types from start, test voiced/unvoiced transitions rigorously.

4. **Deferring CELT until "later"** - Most real-world Opus uses Hybrid or pure CELT mode. SILK-only decoder handles minority of content. pion/opus is currently limited because CELT is unimplemented. Start CELT architecture planning early even if implementation comes after SILK.

5. **Integer overflow behavior (C vs Go)** - C's undefined signed overflow produces different optimized code than Go's defined wraparound. Affects LPC coefficients, range coder normalization, MDCT butterflies. Use explicit masking where C depends on overflow, test extreme values.

6. **Heap allocations in decode path** - Audio at 20ms frames = 50 decodes/second. GC pauses cause glitches. Pre-allocate all buffers at decoder creation, use sync.Pool for temporaries, target zero allocations per frame, profile with -benchmem.

7. **Test vector coverage gaps** - Vectors test compliance, not real-world edge cases. Add captures from FFmpeg, WebRTC, Discord encoders. Test DTX frames, packet loss scenarios, mode transitions. Validate encoder output with libopus decoder, not just own decoder (circular validation trap).

8. **Performance expectations** - Concentus documents 40-50% of native as ceiling for managed languages without SIMD. Don't expect pure Go to match libopus. Design for assembly hot-path replacements in v2 if needed, but accept trade-off for v1.

## Implications for Roadmap

Based on research, the dependency chain clearly dictates implementation order. Range coder has zero dependencies and is required by everything. SILK and CELT can proceed in parallel once range coder works, but SILK should start first (slightly simpler, pion/opus proof point exists). Hybrid mode requires both. Encoders are 2-3x more complex than decoders and should come after decoder validation.

### Phase 1: Foundation & Range Coder
**Rationale:** Range coder is the shared dependency for all modes. Packet parsing is needed to route to decoders. Both are small, well-specified components that establish the project structure and testing approach.

**Delivers:** Working range decoder/encoder with state validation, TOC byte parsing, project structure with internal packages, test harness for RFC 8251 vectors.

**Addresses:** Infrastructure for all subsequent features, establishes bit-exactness validation pattern.

**Avoids:** Pitfall #2 (range decoder state) by implementing validation from start. Pitfall #1 by setting up libopus source cross-reference workflow.

**Research flag:** Standard patterns from RFC - no additional research needed.

### Phase 2: SILK Decoder
**Rationale:** SILK is required for NB/MB/WB speech and hybrid mode. pion/opus provides proof of concept. Starting with decoder (not encoder) allows validation against test vectors before tackling encoder complexity.

**Delivers:** Working SILK decoder for all bandwidths (NB/MB/WB), all frame sizes (10/20/40/60ms), mono and stereo, basic PLC.

**Addresses:** Table stakes features - SILK decoder compliance, Frame parsing for SILK modes, Fixed-point Q-format arithmetic.

**Avoids:** Pitfall #3 (underestimating SILK) by budgeting adequate time, implementing all frame types upfront. Pitfall #5 (overflow) with explicit fixed-point operations package. Pitfall #6 (allocations) by pre-allocating decoder state.

**Research flag:** Needs research - SILK has complex interdependencies (LSF quantization, LTP filters, stereo state). Consider `/gsd:research-phase` for LPC synthesis and pitch analysis specifics if test vectors fail.

### Phase 3: CELT Decoder
**Rationale:** Required for music/general audio and hybrid mode. Can proceed after range coder is working. CELT and SILK share no code beyond range coder, allowing parallel development if needed.

**Delivers:** Working CELT decoder for all bandwidths (NB/WB/SWB/FB), all frame sizes (2.5/5/10/20ms), mono and stereo, MDCT synthesis, PVQ decoding, band folding.

**Addresses:** Table stakes - CELT decoder compliance, Music and low-delay use cases, Broader bandwidth coverage (SWB/FB).

**Avoids:** Pitfall #4 (deferring CELT) by tackling early. Pitfall #7 (test coverage) by testing with real WebRTC captures, not just vectors.

**Research flag:** Needs research - MDCT implementation details, PVQ combinatoric indexing (CWRS), band energy prediction. Consider `/gsd:research-phase` for MDCT optimization approaches and PVQ decoding if implementation stalls.

### Phase 4: Hybrid Mode & Resampling
**Rationale:** Requires both SILK and CELT decoders working. Enables super-wideband/fullband speech, the sweet spot for Opus quality.

**Delivers:** Hybrid mode decoder, polyphase resampling (8/12/16/24/48 kHz), delay compensation, output summing, complete decoder API.

**Addresses:** Table stakes - hybrid mode compliance, Full Opus decoder functionality.

**Avoids:** Pitfall #9 (stereo state persistence) by testing mode transitions. Pitfall #2 (state validation) by verifying hybrid mode against test vectors.

**Research flag:** Standard patterns - RFC specifies delay and summing clearly. No additional research expected.

### Phase 5: SILK Encoder (Basic)
**Rationale:** Enables speech encoding. Significantly harder than decoder (analysis vs synthesis). Start with basic quality, defer advanced psychoacoustic optimization.

**Delivers:** Working SILK encoder at basic quality level, LPC analysis, pitch detection, excitation quantization, frame type selection.

**Addresses:** Should-have features for production use, Speech call encoding capability.

**Avoids:** Pitfall #15 (circular validation) by decoding encoder output with libopus. Pitfall #3 (complexity) by targeting basic quality first, not optimal.

**Research flag:** Needs research - encoder is non-normative, many implementation approaches. LPC analysis, pitch detection algorithms require deep dive. Consider `/gsd:research-phase` for encoder quality/performance tradeoffs.

### Phase 6: CELT Encoder (Basic)
**Rationale:** Enables music/general audio encoding. Extremely complex (MDCT analysis, bit allocation, psychoacoustic modeling). Start with basic quality.

**Delivers:** Working CELT encoder at basic quality, MDCT analysis, band energy quantization, PVQ encoding, bit allocation.

**Addresses:** Should-have features for production use, Music and low-delay encoding.

**Avoids:** Pitfall #7 (transcendentals) by accepting small float differences as encoder is non-normative. Pitfall #15 by cross-testing with multiple decoders.

**Research flag:** Needs research - bit allocation is complex optimization problem, PVQ encoding has multiple approaches. Consider `/gsd:research-phase` for encoder architecture patterns.

### Phase 7: Integration & Polish
**Rationale:** After core codec works, add production-ready features and optimize based on profiling.

**Delivers:** Hybrid encoder, VBR/CBR control, in-band FEC, io.Reader/Writer wrappers, performance optimization (sync.Pool, zero-alloc), API finalization.

**Addresses:** Production readiness, idiomatic Go API patterns, real-time performance targets.

**Avoids:** Pitfall #17 (heap allocations) by profiling and optimizing hot paths. Pitfall #18 (performance expectations) by targeting realistic 40-50% of native.

**Research flag:** Standard patterns - Go performance optimization well-documented. Use pprof, no additional research needed.

### Phase Ordering Rationale

- **Foundation first**: Range coder and packet parsing have zero dependencies and unblock all other work.
- **Decoders before encoders**: Decoders are normative (must be bit-exact), simpler to validate with test vectors, and establish architecture patterns. Encoders are 2-3x more complex and non-normative.
- **SILK before CELT**: SILK slightly simpler, pion/opus provides proof point, enables testing hybrid mode architecture earlier. But CELT shouldn't be deferred past Phase 3.
- **Hybrid after both codecs**: Obvious dependency, but critical for real-world quality.
- **Encoders only after decoder validation**: Allows testing encoder output against validated decoder, avoiding circular validation pitfall.
- **Polish last**: Performance optimization and API refinement need real usage patterns, which only emerge after core codec works.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 2 (SILK Decoder):** LPC synthesis filter stability, LSF quantization tables, pitch analysis edge cases. SILK appears simple but has intricate interdependencies that may require additional research if test vectors fail.
- **Phase 3 (CELT Decoder):** MDCT implementation approaches (there are multiple algorithms), PVQ combinatoric indexing (CWRS), band energy inter-prediction. MDCT is well-studied but CELT's specific variant may need research.
- **Phase 5 (SILK Encoder):** Non-normative with many implementation choices. Quality/performance tradeoffs, pitch detection algorithms, rate-distortion optimization all have deep literature. Plan for research phase.
- **Phase 6 (CELT Encoder):** Bit allocation is complex optimization problem, psychoacoustic modeling has multiple approaches. Definitely needs research phase for encoder architecture decisions.

Phases with standard patterns (skip research-phase):
- **Phase 1 (Foundation):** Range coding is well-specified in RFC, packet parsing is deterministic. Implementation is complex but patterns are known.
- **Phase 4 (Hybrid Mode):** Straightforward coordination of existing components per RFC specification.
- **Phase 7 (Polish):** Go performance optimization is well-documented (pprof, sync.Pool, escape analysis).

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | math/bits, encoding/binary, unsafe patterns proven in production Go codecs. Zero external dependencies is core requirement with precedent (pion/opus). |
| Features | HIGH | RFC 6716/8251 provide definitive feature list. Test vectors are official. Distinction between normative decoder and non-normative encoder is clear. |
| Architecture | HIGH | RFC 6716 structure maps directly to component boundaries. pion/opus validates approach. Range coder, SILK, CELT, hybrid separation is well-defined. |
| Pitfalls | HIGH | pion/opus open issues document real challenges. Concentus performance data (40-50%) is empirical. RFC 8251 errata validate stereo state and phase shift issues. |

**Overall confidence:** HIGH

### Gaps to Address

- **SILK encoder quality targets**: Research identifies basic vs advanced quality tradeoffs but doesn't specify what "basic quality" means quantitatively. During Phase 5, establish quality metrics (MOS scores, PESQ) to validate encoder is "good enough" before moving on.

- **CELT bit allocation algorithms**: Multiple approaches exist (greedy, dynamic programming, pre-computed tables). Research doesn't resolve which to implement. Phase 6 planning should research libopus's approach vs simpler alternatives.

- **Performance profiling baseline**: Research states 40-50% of native is ceiling, but actual bottlenecks unknown until implementation. Early profiling in Phase 2-3 will reveal whether target is achievable and where optimization effort should focus.

- **WebAssembly performance**: Research notes standard Go (~2-5 MB) vs TinyGo (~100-500 KB) options but doesn't profile WASM performance. If browser deployment is critical, validate performance early in Phase 4 after decoder works.

- **Test vector automation**: Research identifies RFC 8251 vectors as critical but doesn't specify integration approach. Phase 1 should establish automated download, caching, and CI integration so vectors run on every commit.

## Sources

### Primary (HIGH confidence)
- [RFC 6716: Definition of the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc6716) - Normative specification for decoder
- [RFC 8251: Updates to the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc8251) - Errata and test vectors
- [RFC 7845: Ogg Encapsulation for Opus](https://datatracker.ietf.org/doc/html/rfc7845) - Container format (deferred to v2+)
- [Opus Official Test Vectors](https://opus-codec.org/testvectors/) - Compliance validation
- [Go math/bits package](https://pkg.go.dev/math/bits) - Compiler intrinsics documentation
- [Go Fuzzing](https://go.dev/doc/security/fuzz/) - Native fuzz testing approach

### Secondary (MEDIUM confidence)
- [pion/opus GitHub](https://github.com/pion/opus) - Pure Go SILK decoder, validates feasibility, open issues document real challenges
- [concentus GitHub](https://github.com/lostromb/concentus) - Multi-language port, 40-50% performance data
- [libopus reference implementation](https://github.com/xiph/opus) - C source code (authoritative when RFC conflicts)
- [The Opus Codec (arXiv)](https://arxiv.org/pdf/1602.04845) - Architecture overview, CELT/SILK details
- [AES 135 Opus CELT Paper](https://jmvalin.ca/papers/aes135_opus_celt.pdf) - MDCT and psychoacoustic background
- [AES 135 Opus SILK Paper](https://jmvalin.ca/papers/aes135_opus_silk.pdf) - LPC and pitch analysis details

### Tertiary (LOW confidence)
- [Opus Wikipedia](https://en.wikipedia.org/wiki/Opus_(audio_format)) - General overview
- [Range Coding Wikipedia](https://en.wikipedia.org/wiki/Range_coding) - Algorithm background
- [go-audio/audio](https://pkg.go.dev/github.com/go-audio/audio) - Buffer format patterns (reference only, not dependency)

---
*Research completed: 2026-01-21*
*Ready for roadmap: yes*
