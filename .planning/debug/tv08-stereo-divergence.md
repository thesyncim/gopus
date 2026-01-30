---
status: gathering
trigger: "TV08 is failing with Q=-92.46 (catastrophic failure). Stereo decoder test with mode switching."
created: 2026-01-30T10:00:00Z
updated: 2026-01-30T10:00:00Z
---

## Current Focus

hypothesis: Stereo decoder mishandles MONO CELT packets - wrong sample count or duplication
test: Examine DecodeInt16Slice handling when stereo decoder receives mono packet
expecting: Find that mono packet decoded with stereo decoder produces wrong sample layout
next_action: Examine gopus decoder logic for mono-to-stereo conversion in CELT mode

## Symptoms

expected: Q > 0 (matching reference output)
actual: Q = -92.46 (catastrophic failure)
errors: Massive divergence from reference
reproduction: Run TV08 compliance test
started: Unknown

## Eliminated

## Evidence

- timestamp: 2026-01-30T10:05:00Z
  checked: TV08 packet-by-packet comparison
  found: First divergence at packet 208
  implication: Not SILK issue - divergence in CELT section

- timestamp: 2026-01-30T10:05:01Z
  checked: Packet 208 details
  found: CELT Config=27 (SWB), Stereo=false (MONO), FrameCount=2, FrameSize=960
  implication: Mono packet being decoded by stereo decoder

- timestamp: 2026-01-30T10:05:02Z
  checked: Packet 207 details
  found: CELT Config=21, Stereo=true
  implication: STEREO->MONO transition within CELT mode

- timestamp: 2026-01-30T10:05:03Z
  checked: Sample comparison at divergence
  found: First 10 samples match perfectly, divergence starts around sample 3330 (of 3840)
  implication: Divergence in second frame of 2-frame packet (each frame = 1920 stereo samples)

## Resolution

root_cause:
fix:
verification:
files_changed: []
