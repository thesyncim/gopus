# Phase 3: CELT Decoder - Context

**Gathered:** 2026-01-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Decode CELT-mode Opus packets (music and general audio) following RFC 6716 Section 4.3. This is internal decoder infrastructure — not a public API. Outputs feed into Hybrid decoder (Phase 4) and eventual public API (Phase 10).

Scope includes:
- CELT mono/stereo decoding at all bandwidths (NB to FB)
- All frame sizes (2.5/5/10/20ms)
- Intensity stereo handling
- Transient detection and short MDCT blocks

</domain>

<decisions>
## Implementation Decisions

### Claude's Discretion

User elected to let Claude make all engineering decisions for this phase. The following are open:

- **Numeric representation** — Fixed-point (Q15/Q31) vs floating-point. Standard choice: float64 for clarity, convert to float32 at API boundary.
- **IMDCT implementation** — Direct, FFT-based, or library. Standard choice: FFT-based for efficiency.
- **Internal API design** — How CELT exposes frames to Hybrid decoder. Standard choice: frame-based with sample output.
- **PVQ/CWRS decoding** — Implementation approach for combinatorial coding.
- **Band folding strategy** — How to handle bands above the coded region.
- **Test approach** — Unit tests against known patterns; full validation deferred to Phase 12.

</decisions>

<specifics>
## Specific Ideas

No specific requirements — follow RFC 6716 normative behavior. Match libopus behavior where spec is ambiguous.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 03-celt-decoder*
*Context gathered: 2026-01-21*
