---
phase: 12-compliance-polish
plan: 01
subsystem: encoder
tags: [testing, import-cycle, refactoring]
dependency-graph:
  requires: []
  provides: ["import-cycle-free encoder tests"]
  affects: ["CI/CD", "go test ./..."]
tech-stack:
  added: []
  patterns: ["external test package pattern"]
key-files:
  created: ["internal/encoder/export_test.go"]
  modified:
    - "internal/encoder/encoder_test.go"
    - "internal/encoder/integration_test.go"
    - "internal/encoder/libopus_test.go"
    - "internal/encoder/packet_test.go"
    - ".gitignore"
decisions:
  - id: "use-types-package"
    choice: "Import types.Bandwidth/Mode for encoder API"
    reason: "Encoder uses internal types package; tests need to match"
  - id: "cast-toc-comparisons"
    choice: "Cast types.Bandwidth to gopus.Bandwidth when comparing TOC"
    reason: "gopus.ParseTOC returns gopus types, not internal types"
metrics:
  duration: "16 minutes"
  completed: "2026-01-22"
---

# Phase 12 Plan 01: Fix Import Cycle Summary

Fixed the import cycle in internal/encoder tests using external test package pattern

## What Changed

### Problem
The internal/encoder test files used `package encoder` and imported both `gopus` (root package) and `gopus/internal/encoder`. Since gopus imports internal/encoder, this created an import cycle that caused `go test ./...` to fail.

### Solution
1. Converted all test files from `package encoder` to `package encoder_test`
2. Created `export_test.go` to expose unexported functions needed by tests
3. Added proper type imports (`gopus/internal/types`) and type casts where needed

### Files Modified

| File | Changes |
|------|---------|
| `encoder_test.go` | Changed to `package encoder_test`, added types import, prefixed encoder types |
| `integration_test.go` | Changed to `package encoder_test`, updated bandwidth/mode types |
| `libopus_test.go` | Changed to `package encoder_test`, updated bandwidth/mode types |
| `packet_test.go` | Changed to `package encoder_test`, updated mode/bandwidth for BuildPacket |
| `export_test.go` | **Created** - exports unexported functions for testing |
| `.gitignore` | Added `*.test` to ignore compiled test binaries |

### Key Technical Details

**Exported Functions (via export_test.go):**
- `Downsample48to16` - downsampling function for hybrid mode
- `TargetBytesForBitrate` - bitrate calculation
- `ClassifySignal` - signal classification for DTX
- `ComputeLBRRBitrate` - FEC bitrate calculation
- `WriteFrameLength` - packet frame length encoding
- `ShouldUseFEC` - FEC decision method
- `UpdateFECState` - FEC state update method

**Type Handling:**
- Encoder API uses `types.Bandwidth` and `types.Mode` (from internal/types)
- TOC parsing uses `gopus.Bandwidth` and `gopus.Mode` (from root package)
- Added casts like `gopus.Bandwidth(tc.bandwidth)` for comparisons

## Commits

| Hash | Message |
|------|---------|
| 6024e1b | refactor(12-01): convert encoder_test.go to external test package |
| 87ecd16 | refactor(12-01): convert remaining test files to external test package |
| c2e3083 | test(12-01): verify full test suite passes |

## Verification

```
$ go test ./...
ok      gopus                    (cached)
ok      gopus/container/ogg      (cached)
ok      gopus/internal/celt      (cached)
ok      gopus/internal/encoder   5.974s
ok      gopus/internal/hybrid    (cached)
ok      gopus/internal/multistream (cached)
ok      gopus/internal/plc       (cached)
ok      gopus/internal/rangecoding (cached)
ok      gopus/internal/silk      (cached)
```

All packages pass. The import cycle error is eliminated.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added *.test to .gitignore**
- **Found during:** Task 3
- **Issue:** Compiled test binary was accidentally committed
- **Fix:** Removed binary, added `*.test` to .gitignore
- **Commit:** c2e3083

## Next Phase Readiness

- Import cycle eliminated
- `go test ./...` passes without errors
- All existing test assertions preserved
- Ready for remaining Phase 12 plans (test vectors, documentation)
