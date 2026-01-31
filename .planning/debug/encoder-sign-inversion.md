---
status: investigating
trigger: "Investigate why the gopus encoder produces packets that decode with sign inversion and poor correlation"
created: 2026-01-30T10:00:00Z
updated: 2026-01-31T09:30:00Z
symptoms_prefilled: true
---

## Current Focus

hypothesis: TF Analysis produces different tfRes values than libopus due to different inputs or algorithm implementation
test: Compare gopus TFAnalysis inputs (normalized coefficients, importance, tfEstimate) with libopus
expecting: Find what causes different TF decisions leading to byte 7 divergence
next_action: Add CGO test to trace TF Analysis inputs and outputs between gopus and libopus

## Symptoms

expected: Decoded samples match original with correlation ~0.999
actual: Sign inversion (original -0.4330, decoded +0.0721) and poor correlation (~-0.54)
errors: None (silent corruption)
reproduction: Encode any audio, decode, compare samples
started: Unknown - encoder implementation issue

## Eliminated

- hypothesis: Sign inversion in MDCT forward transform
  evidence: MDCT SNR > 138 dB vs libopus, verified correct
  timestamp: 2026-01-30T10:30:00Z

- hypothesis: Decoder bug causing sign inversion
  evidence: gopus decoder and libopus decoder produce IDENTICAL output for gopus packets (both show +0.072 at [400])
  timestamp: 2026-01-31T09:00:00Z

- hypothesis: Simple delay/timing issue
  evidence: Even with delay compensation, correlation is only ~0.25 and SNR is negative (-1.77 dB)
  timestamp: 2026-01-31T09:00:00Z

- hypothesis: Range encoder implementation bug
  evidence: All range encoder operations verified matching libopus exactly (EC_CODE_TOP, normalization, carry propagation, etc.)
  timestamp: 2026-01-30 (from ENCODER_DEBUG_TRACKER.md)

- hypothesis: Coarse energy encoding bug
  evidence: Bytes 0-6 match exactly between gopus and libopus - coarse energy is correct
  timestamp: 2026-01-31T09:30:00Z

## Evidence

- timestamp: 2026-01-30T10:00:00Z
  checked: User report
  found: Sample at index 400 shows -0.4330 original vs +0.0721 decoded
  implication: Sign is inverted AND magnitude is completely different

- timestamp: 2026-01-30T10:00:00Z
  checked: Decoder tests
  found: 12/12 decoder tests pass
  implication: Decoder is correct, problem is in encoder

- timestamp: 2026-01-30T10:30:00Z
  checked: MDCT forward transform vs libopus
  found: SNR > 138 dB (TestMDCT_GoVsLibopusMDCT passes)
  implication: MDCT transform itself is correct

- timestamp: 2026-01-31T09:00:00Z
  checked: gopus decoder vs libopus decoder for gopus packets
  found: IDENTICAL outputs - both produce +0.072 at index 400
  implication: gopus decoder is CORRECT, issue is 100% in encoder

- timestamp: 2026-01-31T09:30:00Z
  checked: TestByte7Trace - byte-by-byte comparison
  found: |
    Bytes 0-6: MATCH exactly (7B 5E 09 50 B7 8C 08)
    Byte 7: DIVERGES (gopus=0x33, libopus=0xD0)
    Binary: gopus=00110011, libopus=11010000
    5 bits differ in byte 7
  implication: CONFIRMED - Divergence starts at byte 7 (bit 56-63), which is TF resolution encoding

- timestamp: 2026-01-31T09:30:00Z
  checked: Encoding phase analysis
  found: |
    Bytes 0-1: Header flags - MATCH
    Bytes 1-6: Coarse energy - MATCH
    Byte 7+: TF resolution - DIVERGES
  implication: TF Analysis or TF Encoding is producing different results than libopus

## Resolution

root_cause: **TF RESOLUTION ENCODING DIVERGENCE AT BYTE 7**

The gopus encoder produces packets that:
1. Match libopus exactly through byte 6 (header + coarse energy)
2. Diverge at byte 7 where TF resolution encoding begins
3. gopus byte 7 = 0x33 (00110011)
4. libopus byte 7 = 0xD0 (11010000)
5. 5 out of 8 bits differ

The TF (time-frequency) resolution determines how MDCT coefficients are allocated between time and frequency resolution. Wrong TF decisions cascade to affect:
- All PVQ band encoding (wrong bit allocation)
- Decoded coefficient magnitudes and signs
- Overall audio quality

Prior investigation (ENCODER_DEBUG_TRACKER.md) identified potential causes:
1. bandLogE input to TFAnalysis may differ
2. Missing toneishness modification was fixed, but issue persists
3. TF analysis gating conditions may differ (enable_tf_analysis)

The underlying TF Analysis algorithm was verified correct by Agent 1, so the issue is likely in the INPUTS to TFAnalysis:
- Normalized coefficients (normL)
- Importance weights from DynallocAnalysis
- tfEstimate value
- transient flag value

fix:
verification:
files_changed: []
