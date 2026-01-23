---
phase: 15-celt-decoder-quality
plan: 11
subsystem: decoder
tags: [celt, divergence, debugging, range-coding, silence-flag]

# Dependency graph
requires:
  - phase: 15-09
    provides: Multi-frame packet handling fixed, sample counts correct
provides:
  - Exact divergence point identification between gopus and libopus
  - Stage-by-stage decode comparison tests
  - Root cause diagnosis for Q=-100
affects: [rangecoding, celt-decoder, compliance]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Frame-by-frame comparison with reference output"
    - "Diagnostic tests that document findings without fixing"

key-files:
  created:
    - internal/testvectors/bitstream_comparison_test.go
    - internal/celt/libopus_comparison_test.go
  modified: []

key-decisions:
  - "D15-11-01: DecodeBit() comparison logic is inverted - root cause of Q=-100"
  - "D15-11-02: Silence flag always returns 1 because val >= r is always true"
  - "D15-11-03: Fix requires changing threshold to (rng - r) instead of r"

patterns-established:
  - "Diagnostic tests document root cause with evidence before fixing"
  - "Frame extraction helpers for multi-frame packet analysis"

# Metrics
duration: 5min
completed: 2026-01-23
---

# Phase 15 Plan 11: Identify CELT Divergence Point Summary

**Found root cause of Q=-100: DecodeBit() logic inverted in rangecoding/decoder.go causes all frames to be treated as silence**

## Performance

- **Duration:** 5 minutes
- **Started:** 2026-01-23T18:24:51Z
- **Completed:** 2026-01-23T18:29:56Z
- **Tasks:** 3
- **Files created:** 2

## Accomplishments

- Created comprehensive first-packet trace test with full decode pipeline tracing
- Created sample-by-sample comparison test identifying exact divergence point
- Identified root cause: DecodeBit() threshold comparison is inverted
- Documented fix: change `val >= r` to `val >= (rng - r)` in rangecoding/decoder.go

## Task Commits

1. **Task 1: Create comprehensive first-packet trace test** - `abd1b14` (test)
2. **Task 2: Compare decoded output with reference byte-by-byte** - (included in Task 1)
3. **Task 3: Create divergence diagnosis summary** - `f6d6c07` (test)

## Root Cause Analysis

### Divergence Finding

**Divergence point:** Sample 0 (100% of frames diverge immediately)

**Pattern observed:** All decoded output is zeros while reference has substantial audio content

**Energy ratio:** 0% (decoded has no energy whatsoever)

### Root Cause

**Bug location:** `internal/rangecoding/decoder.go` function `DecodeBit()`

**Current (WRONG):**
```go
func (d *Decoder) DecodeBit(logp uint) int {
    r := d.rng >> logp
    if d.val >= r {
        // Bit is 1
        ...
    }
}
```

**Correct:**
```go
func (d *Decoder) DecodeBit(logp uint) int {
    r := d.rng >> logp
    threshold := d.rng - r  // '1' region is at TOP of range
    if d.val >= threshold {
        // Bit is 1 (rare case)
        ...
    }
}
```

### Evidence

1. **All decoded output is zeros** - matches silence frame behavior
2. **Tracer never fires** - silence path bypasses entire decode pipeline
3. **DecodeBit(15) returns 1 for all frames** - even high-energy reference frames
4. **The math:** With typical values `val=0x181D3BE7`, `r=0x10000`, `val >= r` is always true

### Why This Happens

Per RFC 6716, the range coder divides [0, rng) into probability regions:
- `[0, rng-r)` = probability region for 0 (32767/32768 of range with logp=15)
- `[rng-r, rng)` = probability region for 1 (1/32768 of range)

The silence flag uses logp=15, meaning P(silence) = 1/32768 (very rare).

Current code checks `val >= r` which is almost always true because `val` (normalized to ~0x181D3BE7) is much larger than `r` (~0x10000).

The correct check should be `val >= (rng - r)` to test if val is in the TOP 1/32768 of the range.

## Files Created

- `internal/testvectors/bitstream_comparison_test.go` - First-packet analysis and comparison tests
- `internal/celt/libopus_comparison_test.go` - Divergence diagnosis with root cause documentation

## Decisions Made

| ID | Decision | Rationale |
|----|----------|-----------|
| D15-11-01 | DecodeBit() comparison logic is the root cause | Evidence from all tests shows silence flag always true |
| D15-11-02 | Fix requires threshold = rng - r | Per RFC 6716, "1" probability region is at TOP of range |
| D15-11-03 | Document before fixing | Diagnosis tests provide evidence for the fix |

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None - investigation proceeded smoothly and root cause was identified.

## Next Phase Readiness

**Root cause identified and documented.** The fix is straightforward:

1. Modify `DecodeBit()` in `internal/rangecoding/decoder.go`
2. Change threshold comparison from `val >= r` to `val >= (rng - r)`
3. Update range/val updates to match correct probability regions

**Recommended next step:** Create plan 15-12 to apply the DecodeBit() fix and verify Q improvement.

**Blockers:** None

---
*Phase: 15-celt-decoder-quality*
*Completed: 2026-01-23*
