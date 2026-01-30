---
status: investigating
trigger: "TV12 is the last failing test vector (Q=-32.06). Hybrid mode has 12x higher failure rate than SILK-only mode."
created: 2026-01-30T10:00:00Z
updated: 2026-01-30T10:00:00Z
symptoms_prefilled: true
goal: find_and_fix
---

## Current Focus

hypothesis: SILK packets have SIGN INVERSIONS mid-frame - decoded signal flips polarity relative to reference
test: Track down where the polarity inversion originates (LPC/excitation/resampler)
expecting: Find bug in SILK LPC synthesis or excitation processing that causes polarity flip
next_action: Investigate SILK decoder LPC and excitation code paths

## Symptoms

expected: All 1332 packets decode identically to libopus reference
actual: Q=-32.06 overall, 9.5% Hybrid packet failure rate (vs 0.8% SILK)
errors: Hybrid mode packets have 12x higher error rate than SILK-only
reproduction: Run TV12 test vector comparison
started: Ongoing decoder compliance effort

## Eliminated

## Evidence

- timestamp: 2026-01-30T10:05:00Z
  checked: TV12 compliance test
  found: Q=-32.06, 1332 packets total (1068 SILK, 264 Hybrid), mono stream (stereo=false), 20ms frames
  implication: Need to find first diverging packet and compare state

- timestamp: 2026-01-30T10:20:00Z
  checked: Per-packet SNR analysis
  found: |
    - SILK packets: 1068 total, 9 failing (0.8%) - but these 9 have MAJOR errors
    - Hybrid packets: 264 total, 9 failing (3.4%) - small errors (maxDiff=3-21)
    - Mode transitions are PERFECT (SNR=200dB)
    - Worst packets ALL in SILK:
      - Packet 826: SNR=-4.7dB, maxDiff=687
      - Packet 213: SNR=6.0dB, maxDiff=429
      - Packet 137: SNR=6.6dB, maxDiff=1082
      - Packet 758: SNR=8.6dB, maxDiff=1190
      - Packet 1041: SNR=9.9dB, maxDiff=261
      - Packet 1118: SNR=11.9dB, maxDiff=1667
  implication: The bug is in SILK mono decoding, NOT Hybrid mode transitions

- timestamp: 2026-01-30T10:35:00Z
  checked: Deep analysis of worst SILK packets (826, 137, 758)
  found: |
    SIGN INVERSION patterns detected:
    - Packet 826: sample 124 decoded=+276, reference=-411 (inverted)
    - Packet 137: sample 242 decoded=-722, reference=+360 (inverted)
    - Packet 758: sample 208 decoded=+828, reference=-362 (inverted)
    Pattern: Error starts small at frame start, grows, then SIGN INVERTS mid-frame
    Bandwidth mix: NB (826), MB (137, 758), WB (1118)
    All are mono, continuous SILK (no mode transitions)
  implication: Bug likely in SILK LPC synthesis, excitation, or resampler causing polarity flip

## Resolution

root_cause:
fix:
verification:
files_changed: []
