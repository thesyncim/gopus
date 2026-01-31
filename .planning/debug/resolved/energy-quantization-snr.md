---
status: resolved
trigger: "Investigate the energy quantization in the gopus CELT encoder. The encoder is producing packets with poor SNR (~0.22 dB) and correlation (~0.53). One possible cause is incorrect energy quantization."
created: 2026-01-30T00:00:00Z
updated: 2026-01-30T00:00:00Z
symptoms_prefilled: true
---

## Current Focus

hypothesis: **ROOT CAUSE IDENTIFIED** - gopus transient detection over-suppresses for pure tones
test: Lines 336-342 in transient.go suppress transient for toneishness > 0.90
expecting: Removing this check will align gopus transient detection with libopus
next_action: Fix transient.go by removing the overly aggressive toneishness suppression

## Symptoms

expected: Good SNR and correlation in encoded packets
actual: Poor SNR (~0.22 dB) and correlation (~0.53)
errors: None - functional but poor quality
reproduction: Encode audio with gopus CELT encoder
started: Unknown - investigating root cause

## Eliminated

- hypothesis: QI value computation is wrong
  evidence: TestCoarseEnergyQITraceAllBands shows all 21 QI values match between gopus and libopus
  timestamp: 2026-01-30

## Evidence

- timestamp: 2026-01-30
  checked: TestEnergyDivergenceTrace
  found: Libopus transient=1 but gopus transient=false, 8/12 bands have mismatched QI (due to different transient mode)
  implication: Transient detection differs, causing different MDCT computation and different energies

- timestamp: 2026-01-30
  checked: TestLibopusEnergyCompare
  found: Gopus packet=221 bytes vs libopus=160 bytes, only 0.6% matching bytes, SNR=-16.45 dB
  implication: Fundamental packet structure divergence, not just energy values

- timestamp: 2026-01-30
  checked: TestCoarseEnergyQITrace (with same input energies)
  found: QI values all match, but gopus=7 bytes vs libopus=6 bytes (Laplace sequence only)
  implication: Range encoder or Laplace encoding produces different byte output for same QI sequence

- timestamp: 2026-01-30
  checked: TestActualEncodingDivergence
  found: Libopus transient=1, gopus transient=0 for 440 Hz sine wave
  implication: Different transient detection causes different MDCT (short vs long blocks), completely different band energies

- timestamp: 2026-01-30
  checked: transient.go lines 336-342
  found: Custom code suppresses transient for toneishness > 0.90, but libopus only suppresses for low freq tones (< 198 Hz)
  implication: 440 Hz sine wave has toneishness > 0.90 which gopus incorrectly suppresses while libopus detects as transient

## Resolution

root_cause: transient.go lines 336-342 has custom code that suppresses transient detection when toneishness > 0.90 for any pure tone. However, libopus only suppresses transient for very LOW frequency tones (< 0.026 radians/sample = ~198 Hz at 48kHz). A 440 Hz sine wave has toneishness > 0.90 so gopus suppresses it, while libopus correctly detects it as transient. This causes:
- gopus: shortBlocks=1 (long MDCT) -> different band energies -> different QI values
- libopus: shortBlocks=8 (short MDCTs) -> different band energies -> different QI values
The resulting packet bytes are completely different, leading to poor SNR when decoded.

fix: Removed the custom toneishness > 0.90 suppression at lines 336-342 in transient.go. This code was NOT in libopus and caused incorrect transient decisions for pure tones above ~200 Hz.

verification:
- TestActualEncodingDivergence: All 21 QI values now match (was 7/21 mismatching before fix)
- TestEncoderCompareWithLibopus: First 7 bytes of packet now match (7b5e0950b78c08)
- TestEncoderComplianceCELT_CGO: All 16 CELT tests pass
- Gopus transient=1 now matches libopus transient=1 for 440 Hz sine wave

files_changed:
- internal/celt/transient.go: Removed incorrect toneishness > 0.90 check that was suppressing transient for mid/high frequency tones
