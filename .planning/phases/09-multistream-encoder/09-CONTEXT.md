# Phase 9: Multistream Encoder - Context

**Gathered:** 2026-01-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Encode surround sound audio to multistream Opus packets. Supports standard channel configurations (mono, stereo, 5.1, 7.1) with coupled stereo streams and Vorbis channel order. Output must be decodable by Phase 5 multistream decoder and libopus.

</domain>

<decisions>
## Implementation Decisions

### Channel Coupling Strategy
- Front left/right (FL/FR) always encoded as coupled stereo stream
- Surround left/right (SL/SR) always encoded as coupled stereo stream
- Center channel encoded as independent mono stream
- LFE channel encoded as independent mono stream
- Use Vorbis channel order for compatibility with Phase 5 decoder

### Surround Configurations
- Support standard set: mono (1), stereo (2), 5.1 (6), 7.1 (8) channels
- Mono/stereo use Phase 8 unified encoder directly (not multistream wrapper)
- Multistream format only for 3+ channels
- Invalid channel counts return error at encoder creation
- No custom channel mappings - standard Vorbis mappings only

### Bitrate Allocation
- 5.1 surround target: 256-510 kbps range
- 7.1 surround scales proportionally

### Claude's Discretion
- Exact bitrate distribution formula (equal vs weighted)
- Whether coupled stereo streams get 2x vs equal bitrate
- Whether LFE gets reduced bitrate (low-frequency only)
- Per-stream bitrate clamping

### Decoder Compatibility
- Round-trip validation required: encode must decode with Phase 5 decoder
- Compose using Phase 8 unified Encoder (MultistreamEncoder contains multiple Encoders)
- Include libopus cross-validation (encode gopus, decode libopus)
- API mirrors Phase 5 decoder: NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping)

</decisions>

<specifics>
## Specific Ideas

- Mirror Phase 5 multistream decoder architecture - it already handles stream parsing, channel mapping, Vorbis order
- Reuse existing mapping.go, stream.go patterns from internal/multistream/
- Round-trip tests are primary validation - if Phase 5 decoder works, encoder is correct

</specifics>

<deferred>
## Deferred Ideas

None - discussion stayed within phase scope

</deferred>

---

*Phase: 09-multistream-encoder*
*Context gathered: 2026-01-22*
