---
status: investigating
trigger: "CELT encoder divergence - dynalloc encoding differs by 1 bit"
created: 2026-02-01T14:00:00Z
updated: 2026-02-01T14:00:00Z
---

## Current Focus

hypothesis: DynallocAnalysis produces correct offsets but encoded packet still differs by 1 bit
test: Trace band 2 boost encoding step-by-step
expecting: Find root cause of 1-bit difference in dynalloc encoding
next_action: Compare actual band energies used by DynallocAnalysis vs libopus

## Progress Update (2026-02-01)

- boost formula fixed: removed incorrect /256 and /128 scaling (agent a5ea3b1)
- matching bytes improved from 6 to 8
- synthetic input tests show matching offsets
- real encoding still shows 1-bit difference at byte 8:
  - Tell after dynalloc: lib=96 go=95 (1 bit difference)
  - Band 2: lib_fb=4 go_fb=3 (different fine bits due to allocation)

## Symptoms

expected: Dynalloc encoding should produce identical bits as libopus
actual: Tell after dynalloc is 95 bits for gopus vs 96 bits for libopus (1 bit difference)
errors: |
  - First divergence at byte 8 (bit 64)
  - Band 2: lib_fb=4 go_fb=3 (fine bits differ due to different allocation)
  - PVQ indices completely different due to allocation cascade
reproduction: Run TestTraceRealEncodingDivergence in internal/celt/cgo_test/
started: Identified during encoder debugging

## Eliminated

## Evidence

- timestamp: 2026-02-01T14:00:00Z
  checked: Coarse energy encoding
  found: First 50 bits match (coarse energy correct). Divergence happens after spread encoding.
  implication: Issue is in dynalloc or later encoding stages

- timestamp: 2026-02-01T14:00:00Z
  checked: TF and Spread encoding
  found: TF values match, Spread=2 for both. Tell after spread is 57 bits for both.
  implication: Divergence starts in dynalloc encoding

- timestamp: 2026-02-01T14:00:00Z
  checked: Dynalloc flag decoding from packets
  found: |
    Band 2: libopus encodes flags (1,1,0) = 2 boosts
    Band 2: gopus encodes flags (1,0) = 1 boost
  implication: gopus computes offset=1 for band 2 while libopus uses offset=2

- timestamp: 2026-02-01T14:00:00Z
  checked: DynallocAnalysis output
  found: Offsets vary significantly from expected. When using bandLogE2=energies, offsets are [0,2,2,0,0,0,0,...]. When using proper long-block bandLogE2, offsets are much higher [3,5,5,5,5,3,2,4,3,3,...].
  implication: The issue may be in how bandLogE2 is computed or used in encoding

## Resolution

root_cause:
fix:
verification:
files_changed: []
