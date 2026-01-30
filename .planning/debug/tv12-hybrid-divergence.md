---
status: investigating
trigger: "TV12 is the last failing test vector (Q=-32.06). Hybrid mode has 12x higher failure rate than SILK-only. Find root cause."
created: 2026-01-30T12:00:00Z
updated: 2026-01-30T12:01:00Z
---

## Current Focus

hypothesis: Wideband SILK decode at native rate has consistent SNR issues (-2 to -4 dB) - this is the core decoder issue, not resampler
test: Investigate native wideband decode discrepancy
expecting: Find SILK decode algorithm difference for WB (16kHz) mode
next_action: Compare wideband SILK decode core against libopus reference

## Symptoms

expected: Q >= -3.5dB for test vector 12 (decoder compliance)
actual: Q = -32.06dB (severe quality degradation)
errors: 9 SILK packets fail (0.8%), 9 Hybrid packets fail (3.4% of smaller set)
reproduction: Run testvector12 through opus decode test
started: Known failing test vector

## Eliminated

## Evidence

- timestamp: 2026-01-30T12:01:00Z
  checked: TV12 mode distribution and divergence pattern
  found: |
    - Total: 1332 packets (1068 SILK, 264 Hybrid)
    - SILK failures: 9 packets (0.8%), worst SNR=-4.7dB at packet 826
    - Hybrid failures: 9 packets (3.4%), worst SNR=23.6dB at packet 600
    - Mode transitions: 386 (SILK->Hybrid), 608 (Hybrid->SILK), 1290 (SILK->Hybrid)
  implication: SILK decoder has worse absolute failures than Hybrid.

- timestamp: 2026-01-30T12:05:00Z
  checked: Native rate SILK decode comparison
  found: |
    - Wideband packets (214+): Consistently negative SNR (-2 to -4 dB) at NATIVE 16kHz rate
    - This failure is in SILK DecodeFrame, BEFORE any resampling
    - NB packets (0-136): Generally good SNR at native rate
    - MB packets (137-213): Mixed results
    - The problem is in the core SILK decode for wideband mode
  implication: Core SILK decoder has issues with wideband (16kHz) packets

- timestamp: 2026-01-30T12:10:00Z
  checked: Fresh vs stateful decoder behavior
  found: |
    - Native DecodeFrame: produces non-zero samples for packet 826
    - DecodeWithDecoder (includes resampling): has 25-sample delay
    - Opus decoder: adds CELT silence on Hybrid->SILK transition, filling delay
    - The resampler delay is handled differently by Opus vs direct SILK calls
  implication: Opus decoder output matches libopus; direct SILK calls have resampler delay

- timestamp: 2026-01-30T12:15:00Z
  checked: Wideband native decode SNR issues
  found: |
    - TestTV12NativeDecodeOnly shows wideband packets have consistent -2 to -4 dB SNR
    - This is at NATIVE rate, so not a resampler issue
    - The core SILK decoding for wideband (16-order LPC) has differences from libopus
  implication: Need to investigate SILK core decode for wideband mode

## Resolution

root_cause: |
  PRELIMINARY FINDINGS - Investigation ongoing:
  1. Wideband (16kHz) SILK packets have consistent -2 to -4 dB SNR at native rate
  2. The issue is in core SILK decode (DecodeFrame), not resampling
  3. Direct SILK decoder has 25-sample delay vs Opus decoder which fills with CELT silence
  4. The Opus-level decoder output matches libopus; direct SILK calls have resampler delay

  CURRENT Q: -32.06 dB (requirement: >= 0 dB for compliance)

  NEXT STEPS:
  - Investigate wideband SILK decode algorithm differences
  - Compare LPC coefficients between gopus and libopus for WB packets
  - Check NLSF decoding for 16-order LPC codebook

fix: TBD - needs deeper investigation of WB SILK decode
verification:
files_changed: []
