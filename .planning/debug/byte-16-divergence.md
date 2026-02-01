---
status: investigating
trigger: "Byte 16 bitstream divergence in CELT"
created: 2026-02-01T00:00:00Z
updated: 2026-02-01T12:30:00Z
---

## Current Focus

hypothesis: CONFIRMED - DynallocAnalysis computes different offsets[2] (gopus=1, libopus=2)
test: TestTraceDynallocStepByStep shows the exact difference
expecting: N/A - root cause found
next_action: Fix DynallocAnalysis to match libopus for band 2

## Symptoms

expected: gopus encoding produces identical bytes to libopus for same input
actual: First 8 bytes match, then divergence at byte 8 (bit 64)
errors: None - decodes without error, but bytes differ
reproduction: Run TestTraceRealEncodingDivergence with CBR mode
started: Always been this way

## Key Evidence (Session 2026-02-01)

1. **Fixed TF decode bug in test** - Test was using simple 1-bit-per-band TF decode instead of proper differential TF decode. This caused apparent "spread mismatch" that was actually a decoder position error.

2. **With corrected TF decode:**
   - Bytes 0-7 MATCH perfectly
   - First divergence at byte 8 (bit 64)
   - Tell after TF: 57 bits for BOTH packets
   - Tell after spread: 57 bits for BOTH packets (spread=2 for both)
   - Tell after dynalloc: lib=96, go=95 (1 bit difference)

3. **Libopus debug output:**
   - Added fprintf debug to libopus celt_encoder.c
   - Confirmed: "SPREAD_DEBUG: shortBlocks=8 complexity=10 nbAvailableBytes=159 C=1 isTransient=1 spread=2"
   - Both gopus and libopus encode spread=2

4. **Coarse energy matches:**
   - TestCompareBandEnergiesWithPreprocessing shows all 21 bands have identical coarse energy values
   - QI values match when same input energies are used

5. **TF values match:**
   - TestTFCompareLibopus, TestTFEncodingDetailedCompare, TestTFEncodeMatchesLibopus all PASS
   - TF encoding produces identical bytes

## Eliminated Hypotheses

- Range encoder normalization: PASSES isolated tests
- Laplace encoding: PASSES when given same QI values
- CWRS/PVQ encoding: PASSES in isolation
- Spread decision: Both encode spread=2 (test had decoding bug)
- TF encoding: Both produce identical TF bytes
- Coarse energy values: All 21 bands match

## Current Status

The divergence is now narrowed down to:
- 1 bit difference in dynalloc encoding (tell after: lib=96, go=95)
- This 1 bit cascades through the rest of the frame

The actual bytes:
- Bytes 0-7: MATCH (header, coarse energy, TF, spread)
- Byte 8: lib=0xBB, go=0xBA (1 bit diff in LSB)

## Next Steps

1. Compare dynalloc offsets arrays between gopus and libopus
2. Compare bit-by-bit encoding of dynalloc
3. Check if the 1-bit difference is in offset values or encoding logic

## Resolution

root_cause: FOUND - DynallocAnalysis computes offsets[2]=1 while libopus computes offsets[2]=2 for the same input. This causes 1 less boost bit to be encoded for band 2, resulting in the 1-bit difference that cascades through the rest of the frame.

Evidence from TestTraceDynallocStepByStep:
- LIB Band 2: j=0 flag=1, j=1 flag=1, j=2 flag=0 (2 boost iterations)
- GO  Band 2: j=0 flag=1, j=1 flag=0 (1 boost iteration)

fix: Debug DynallocAnalysis to find why offsets[2] differs
verification: TestTraceDynallocStepByStep should show matching boost iterations for all bands
files_changed: []
