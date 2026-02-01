---
status: resolved
trigger: "Fuzz test found decoder mismatch on fast-decoder branch - seed=22222, ch=1, fs=1920, br=104000, bw=1104 (superwideband), max diff 0.578680 at sample 1817"
created: 2026-02-01T10:00:00Z
updated: 2026-02-01T10:00:00Z
---

## Current Focus

hypothesis: CONFIRMED - The kissFFT64Forward function in kiss_fft.go has numerical precision issues causing decoder divergence
test: Disabled KissFFT path in dftTo function
expecting: Test should pass with O(n^2) DFT fallback
next_action: Verify this finding and either fix KissFFT or disable it

## Symptoms

expected: gopus decoder output matches libopus decoder output (correlation >= 0.99)
actual: correlation=0.678976, max sample diff=0.578680 at sample 1817, RMS diff=0.157299
errors: Fuzz test mismatch
reproduction: seed=22222, channels=1, frameSize=1920 (40ms), bitrate=104000, bandwidth=1104 (superwideband)
started: Found on fast-decoder branch during fuzz testing

## Eliminated

## Evidence

- timestamp: 2026-02-01T10:10:00Z
  checked: Minimal reproduction test with exact fuzz parameters
  found: |
    - TOC 0xd9: config=27 (CELT-only SWB), stereo=0, frameCode=1 (2 equal frames)
    - First 480 samples (0-479): PERFECT match - maxDiff=0.0, corr=1.0
    - Second 480 samples (480-959): PERFECT match - maxDiff=0.0, corr=1.0
    - Third 480 samples (960-1439): SEVERE mismatch - maxDiff=0.572667, corr=0.355678
    - Fourth 480 samples (1440-1919): SEVERE mismatch - maxDiff=0.578680, corr=0.213476
  implication: The first CELT frame (960 samples) decodes correctly, but second CELT frame (960 samples) has significant errors. This strongly suggests state corruption/mismatch between decoding frame 1 and frame 2.

- timestamp: 2026-02-01T10:20:00Z
  checked: Tested single-frame vs multi-frame encoding
  found: |
    - Single 20ms packet (first 960 samples): PERFECT match (maxDiff=0, corr=1.0)
    - Single 20ms packet (second 960 samples): FAILS (maxDiff=0.598, corr=0.298)
    - This is true for BOTH:
      1. Two frames in one 40ms packet (frameCode=1)
      2. Two separate 20ms packets decoded sequentially
  implication: The problem is NOT multi-frame packet handling. The problem is decoder STATE that persists after first frame decode. After ANY first frame, subsequent frames fail. Key states: prevEnergy, prevLogE, overlapBuffer, preemphState, RNG.

- timestamp: 2026-02-01T10:30:00Z
  checked: Checked differences between fast-decoder branch and compliance branch
  found: |
    - compliance branch: Test PASSES perfectly (maxDiff=0, corr=1.0)
    - fast-decoder branch: Test FAILS (maxDiff=0.578, corr=0.679)
    - Key changes on fast-decoder:
      1. internal/celt/cwrs.go: Added precomputed U(N,K) table for O(1) PVQ lookup
      2. internal/celt/mdct.go: Added mixed-radix KissFFT path in dftTo function
      3. internal/celt/kiss_fft.go: New file implementing mixed-radix FFT
  implication: One of the fast-decoder optimizations is causing the issue

- timestamp: 2026-02-01T10:35:00Z
  checked: Disabled KissFFT path in dftTo function (mdct.go lines 686-693)
  found: |
    - With KissFFT DISABLED: Test PASSES (maxDiff=0, corr=1.0)
    - With KissFFT ENABLED: Test FAILS (maxDiff=0.578, corr=0.679)
    - KissFFT test shows accuracy issues at n=480: max diff = 2.90e-03 vs O(n^2) DFT
  implication: ROOT CAUSE FOUND - The kissFFT64Forward function has numerical precision issues

## Resolution

root_cause: The kissFFT64Forward implementation in internal/celt/kiss_fft.go has numerical precision errors. At FFT size n=480 (used for 960-sample CELT frames), the max difference vs reference DFT is ~2.9e-03. These errors accumulate through the IMDCT pipeline and corrupt the decoded audio, especially in frames after the first (where the overlap buffer carries forward corrupted values).
fix: Disable or fix the KissFFT optimization in mdct.go dftTo function
verification: Disabling KissFFT makes TestMonoSWB40msMismatch pass with perfect match
files_changed: [internal/celt/mdct.go]

root_cause:
fix:
verification:
files_changed: []
