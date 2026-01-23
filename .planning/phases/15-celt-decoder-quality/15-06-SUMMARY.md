---
phase: 15-celt-decoder-quality
plan: 06
subsystem: celt-decoder
tags: [tracing, debugging, celt, decoder, pvq, energy]

# Dependency graph
requires:
  - phase: 15-celt-decoder-quality
    provides: "CELT decoder pipeline (15-01 through 15-05)"
provides:
  - "Debug trace infrastructure for CELT decoder"
  - "Tracer interface for extensible trace capture"
  - "LogTracer for formatted trace output"
  - "NoopTracer for zero-overhead production use"
  - "Trace calls at all decoder pipeline stages"
  - "Trace-enabled tests with sample output"
affects:
  - "Future Q=-100 debugging efforts"
  - "Comparison with libopus reference values"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Global tracer pattern with interface for zero-overhead switching"
    - "Trace output format: [CELT:stage] key=value for parsing"

key-files:
  created:
    - internal/celt/debug_trace.go
    - internal/celt/debug_trace_test.go
  modified:
    - internal/celt/decoder.go
    - internal/celt/energy.go
    - internal/celt/bands.go
    - internal/celt/pvq.go
    - internal/celt/decoder_test.go

key-decisions:
  - "D15-06-01: Use global DefaultTracer with SetTracer() for runtime control"
  - "D15-06-02: Trace format [CELT:stage] key=value for easy grep/parsing"
  - "D15-06-03: Array truncation at 8 elements with '...' suffix"
  - "D15-06-04: DecodePVQWithTrace variant for band-aware PVQ tracing"

patterns-established:
  - "Tracer interface: six methods covering all pipeline stages"
  - "NoopTracer: empty implementations for zero overhead"
  - "LogTracer: io.Writer-based formatted output"

# Metrics
duration: 8min
completed: 2026-01-23
---

# Phase 15 Plan 06: Add Debug Tracing Summary

**Debug trace infrastructure for CELT decoder with zero-overhead NoopTracer default and LogTracer for divergence analysis**

## Performance

- **Duration:** 8 min
- **Started:** 2026-01-23T16:59:00Z
- **Completed:** 2026-01-23T17:07:39Z
- **Tasks:** 3
- **Files created:** 2
- **Files modified:** 5

## Accomplishments

- Created Tracer interface with 6 methods covering all CELT decoder stages
- Implemented NoopTracer (zero overhead) and LogTracer (io.Writer output)
- Added trace calls throughout decoder pipeline: header, energy, allocation, PVQ, coeffs, synthesis
- Created comprehensive tests for trace format, truncation, and decoder integration
- Trace output enables direct comparison with libopus reference values

## Task Commits

Each task was committed atomically:

1. **Task 1: Create debug trace infrastructure** - `65ad4dc` (feat)
2. **Task 2: Add trace calls to decoder pipeline** - `e9bbbee` (feat)
3. **Task 3: Create trace-enabled test and capture sample output** - `60fc925` (test)

## Files Created/Modified

- `internal/celt/debug_trace.go` - Tracer interface, NoopTracer, LogTracer, global SetTracer()
- `internal/celt/debug_trace_test.go` - Tests for format, truncation, interface, formatter functions
- `internal/celt/decoder.go` - TraceHeader after flags, TraceSynthesis after de-emphasis
- `internal/celt/energy.go` - TraceEnergy after each band's coarse energy
- `internal/celt/bands.go` - TraceAllocation and TraceCoeffs per band
- `internal/celt/pvq.go` - DecodePVQWithTrace for TracePVQ with band context
- `internal/celt/decoder_test.go` - TestDecodeWithTrace, TestDecodeWithTraceSilence, TestDecodeWithTraceMultipleFrames

## Decisions Made

1. **D15-06-01:** Use global DefaultTracer with SetTracer() for runtime control - enables test isolation and production zero-overhead
2. **D15-06-02:** Trace format [CELT:stage] key=value - easy to grep/parse, unambiguous stage identification
3. **D15-06-03:** Truncate arrays at 8 elements with '...' suffix - balances detail with readability
4. **D15-06-04:** Added DecodePVQWithTrace variant - allows band index context for TracePVQ calls from bands.go

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

1. **Silence flag detection:** Initial test data patterns triggered silence flag in range decoder. Fixed by using 0xFF byte patterns which avoid silence flag threshold.

## Sample Trace Output

From TestDecodeWithTrace:
```
[CELT:header] frameSize=480 channels=1 lm=2 intra=0 transient=0
[CELT:energy] band=0 coarse=-18.1562 fine=0.0000 total=-18.1562
[CELT:alloc] band=0 bits=23 k=23
[CELT:pvq] band=0 index=127 k=23 n=4 pulses=[0,-1,-4,-18]
[CELT:coeffs] band=0 coeffs=[0.0000,-0.0000,-0.0000,-0.0000]
[CELT:synthesis] stage=final samples=[0.0000,0.0000,0.0000,0.0000...]
```

This output enables direct comparison with libopus debug output to identify Q=-100 divergence.

## Next Phase Readiness

- Debug trace infrastructure is complete and tested
- Can now decode CELT frames with full intermediate value visibility
- Ready for comparative analysis with libopus reference decoder
- No blockers or concerns

---
*Phase: 15-celt-decoder-quality*
*Completed: 2026-01-23*
