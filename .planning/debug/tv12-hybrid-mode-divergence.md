---
status: investigating
trigger: "TV12 is the last failing test vector (Q=-32.06). Hybrid mode has 12x higher failure rate than SILK-only mode."
created: 2026-01-30T10:00:00Z
updated: 2026-01-30T10:00:00Z
symptoms_prefilled: true
goal: find_and_fix
---

## Current Focus

hypothesis: Error at bandwidth transitions is isolated to FIRST packet after change, grows through IIR resampler
test: Compare per-sample error pattern at transition packets
expecting: Error starts tiny (0.000001) and grows through packet, subsequent packets are perfect
next_action: Investigate why first packet after BW change has growing error while subsequent packets are perfect

## CRITICAL FINDING (Jan 30, 2026)

**Error pattern at bandwidth transitions:**
- First sample diff: 0.000001 (essentially correct)
- Sample 19 diff: 0.001562 (1562x growth)
- Packet 137 SNR: 6.6 dB
- Packet 138 SNR: 999.0 dB (PERFECT!)

**This means:**
1. The error is isolated to the FIRST packet after bandwidth change
2. The error grows within that packet (IIR accumulation pattern)
3. Subsequent packets are perfect - the state at END of transition packet is correct
4. The resampler starts fresh (sIIR=[0,0,0,0,0,0]) at transitions

**Key data at packet 137 (NB→MB):**
- sMid before decode: [-603, -323]
- sMid[1] used as first resampler input: -323 (-0.009857 as float)
- MB resampler sIIR starts at zeros
- First 48kHz output: -0.007492 (gopus), -0.007493 (libopus) - diff=0.000001

**Root cause candidates:**
1. sMid sample rate mismatch: sMid[1] is from 8kHz frame, used as 12kHz resampler input
2. delayBuf handling: MB resampler uses 4 zeros + 8 samples, NB uses 8 samples directly
3. Subtle fixed-point arithmetic difference in first frame processing

## Symptoms

expected: All 1332 packets decode identically to libopus reference
actual: Q=-32.06 overall, 9.5% Hybrid packet failure rate (vs 0.8% SILK)
errors: Hybrid mode packets have 12x higher error rate than SILK-only
reproduction: Run TV12 test vector comparison
started: Ongoing decoder compliance effort

## Eliminated

- hypothesis: Resampler causes sign inversions
  evidence: |
    1. Fresh vs stateful resampler produces identical output (SNR=999dB, diff=0)
    2. Native SILK output (BEFORE resampling) already has severe divergence
    3. At packet 826 native rate: go=+0.000366 lib=-0.019253 (factor of 50x difference BEFORE resampling)
    4. Earlier claim of "native SILK output matches" was testing with FRESH decoder, not accumulated state
  timestamp: 2026-01-30T14:00:00Z

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

- timestamp: 2026-01-30T11:15:00Z
  checked: Native SILK rate comparison (before resampling) for packet 826
  found: |
    At NATIVE SILK rate (8kHz for NB packet 826):
    - GoSamples=160, LibSamples=160 (both match)
    - Differences are VERY SMALL (< 0.001)
    - NO SIGN INVERSIONS at native rate!
    - Sample [0]: go=0.000366 lib=-0.000173 diff=0.000539
    - Sample [28]: go=0.000092 lib=0.000098 diff=-0.000006 (nearly identical)
  implication: SILK core decoding is CORRECT. The sign inversion happens AFTER native decode - likely in RESAMPLER

- timestamp: 2026-01-30T14:00:00Z
  checked: Native SILK WITH ACCUMULATED STATE (re-verified)
  found: |
    CORRECTION: The earlier test used a FRESH decoder. With STATEFUL decoder (packets 0-825 processed):
    - Packet 826 native (8kHz):
      - go=+0.000366 lib=-0.019253 (50x difference, OPPOSITE SIGNS)
      - go=+0.000336 lib=-0.015500
      - go=+0.000183 lib=-0.014521
    - Native SNR = -0.2 dB (FAILURE before resampling)
    - Fresh decoder SNR = 1.5 dB (also poor, but less severe)
  implication: Bug is in SILK CORE decoder, NOT resampler. State accumulation causes divergence.

- timestamp: 2026-01-30T14:05:00Z
  checked: Native SNR analysis across bandwidth transitions
  found: |
    Packets showing native SNR by bandwidth transition:
    - Packet 0 (NB): 999.0 dB (perfect at start)
    - Packet 136 (NB): 6.0 dB (diverging)
    - Packet 137 (MB): 4.8 dB <-- BW CHANGE NB->MB
    - Packet 213 (MB): 9.9 dB (partial recovery)
    - Packet 214 (WB): -3.3 dB <-- BW CHANGE MB->WB, severe failure
    - Packets 215-300 (WB): -2 to -4 dB (ALL FAIL)
    - Packet 826 (NB): -0.2 dB (accumulated error)
  implication: Bandwidth transitions desync SILK decoder state. WB has worst divergence.

- timestamp: 2026-01-30T14:10:00Z
  checked: Gopus resampler code comparison with libopus
  found: |
    Resampler analysis (.planning/debug/tv12-resampler-logic.md):
    1. Coefficients match exactly (silkResamplerUp2HQ0/1, silkResamplerFracFIR12)
    2. Fixed-point arithmetic functions match (SMULWB, SMLAWB, SMULBB, etc.)
    3. Algorithm structure matches (2x IIR upsample + FIR interpolation)
    4. Fresh vs stateful resampler produces identical output
  implication: Resampler implementation is CORRECT. Root cause is upstream.

## Resolution

root_cause:
fix:
verification:
files_changed: []
