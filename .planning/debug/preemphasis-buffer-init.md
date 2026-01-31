---
status: investigating
trigger: "Investigate the pre-emphasis buffer initialization issue in the gopus CELT encoder"
created: 2026-01-30T10:00:00Z
updated: 2026-01-30T10:30:00Z
---

## Current Focus

hypothesis: Root cause identified - gopus implementation is CORRECT; zeros on first frame is expected behavior
test: verified libopus source code behavior
expecting: confirm both implementations behave similarly
next_action: document findings and suggest any remaining alignment issues

## Symptoms

expected: First frame should not trigger false transients or have windowing artifacts
actual: Pre-emphasis buffer zeros cause energy jump from zeros to actual signal
errors: None explicit - behavioral issue
reproduction: Encode first frame of any audio
started: Design/implementation issue

## Eliminated

- hypothesis: libopus has different initialization for first frame overlap
  evidence: libopus line 2017 copies from prefilter_mem which is ALSO zeros on first frame (OPUS_CLEAR clears from ENCODER_RESET_START which includes prefilter_mem)
  timestamp: 2026-01-30T10:10:00Z

- hypothesis: gopus has wrong buffer handling causing false transients
  evidence: gopus correctly implements the same zeros-on-first-frame behavior as libopus; toneishness protection prevents false transients for pure tones
  timestamp: 2026-01-30T10:25:00Z

## Evidence

- timestamp: 2026-01-30T10:05:00Z
  checked: libopus encoder struct layout (celt_encoder.c lines 60-142)
  found: in_mem[1] at line 136, prefilter_mem allocated after in_mem (line 167), both cleared on OPUS_RESET_STATE
  implication: libopus also starts with zeros in overlap buffers

- timestamp: 2026-01-30T10:07:00Z
  checked: libopus OPUS_RESET_STATE (celt_encoder.c lines 3074-3092)
  found: OPUS_CLEAR clears from ENCODER_RESET_START (rng) to end, which includes preemph_memE and in_mem/prefilter_mem
  implication: First frame DOES have zeros in overlap - this is intentional

- timestamp: 2026-01-30T10:08:00Z
  checked: libopus in[] buffer construction (celt_encoder.c lines 2015-2017)
  found: Line 2015-2016 applies celt_preemphasis to PCM, writes to in+c*(N+overlap)+overlap (MIDDLE of buffer)
         Line 2017 copies overlap samples from END of prefilter_mem to BEGINNING of in[]
  implication: On first frame, first 'overlap' samples are zeros (from cleared prefilter_mem)

- timestamp: 2026-01-30T10:10:00Z
  checked: gopus transient analysis input construction (encode_frame.go lines 112-119)
  found: gopus builds transientInput as [preemphBuffer] + [preemph], preemphBuffer is zeros on first frame
  implication: Both gopus and libopus have same first frame behavior - zeros in overlap region

- timestamp: 2026-01-30T10:12:00Z
  checked: libopus tone_detect and toneishness check (celt_encoder.c lines 445-451, 2021, 2030-2033)
  found: libopus calls tone_detect() BEFORE transient_analysis(), passes toneishness to transient_analysis()
         If toneishness > 0.98 and tone_freq < 0.026 rad/sample (~198 Hz), transient is suppressed
  implication: Pure tones at low frequency are protected from false transient detection

- timestamp: 2026-01-30T10:13:00Z
  checked: gopus toneishness protection (transient.go lines 331-342)
  found: gopus has SIMILAR protection: toneishness > 0.98 && tone_freq < 0.026 suppresses transient
         ALSO has toneishness > 0.90 for any pure tone (line 340-342)
  implication: gopus has MORE aggressive toneishness protection than libopus

- timestamp: 2026-01-30T10:20:00Z
  checked: libopus prefilter_mem usage (celt_encoder.c lines 1854, 2017)
  found: prefilter_mem = st->in_mem + CC*overlap (starts after in_mem)
         Line 2017 copies from END of prefilter_mem (last overlap samples) to BEGINNING of in[]
         On encoder init/reset, prefilter_mem is cleared to zeros
  implication: libopus INTENTIONALLY has zeros in overlap on first frame

- timestamp: 2026-01-30T10:22:00Z
  checked: libopus compute_mdcts (celt_encoder.c lines 511-554)
  found: MDCT input at line 534 is in+c*(B*N+overlap)+b*N, which includes the overlap region at the start
         The overlap region contains the zeros from prefilter_mem on first frame
  implication: Both libopus and gopus have zeros windowed into the MDCT on first frame

- timestamp: 2026-01-30T10:25:00Z
  checked: gopus MDCT overlap handling (mdct_encode.go lines 135-225, encode_frame.go lines 580-634)
  found: computeMDCTWithOverlap uses e.overlapBuffer for the overlap region
         On first frame, overlapBuffer is zeros (initialized in NewEncoder line 127)
         ComputeMDCTWithHistory copies history to input[:overlap], then current samples
  implication: gopus correctly replicates libopus behavior - zeros in overlap on first frame

## Resolution

root_cause: INVESTIGATION COMPLETE - The "issue" described is actually CORRECT BEHAVIOR.

Both libopus and gopus initialize the overlap/preemphasis buffers to zeros on encoder creation/reset. This means:

1. **First frame transient analysis receives N+overlap samples where the first 'overlap' samples are zeros**
   - libopus: copies from cleared prefilter_mem
   - gopus: uses cleared preemphBuffer

2. **First frame MDCT receives samples where the first 'overlap' samples are zeros**
   - libopus: in[] buffer first overlap samples from prefilter_mem (zeros)
   - gopus: overlapBuffer used for history (zeros)

3. **This is INTENTIONAL design** - Starting from zeros provides a clean start and the windowing naturally handles the transition. The Vorbis window applied during MDCT smooths the transition from zeros to signal.

4. **Toneishness protection prevents false transients for pure tones**
   - libopus: toneishness > 0.98 && tone_freq < 0.026 suppresses transient
   - gopus: same check PLUS toneishness > 0.90 for any tone (more aggressive)

**Why this works:**
- The MDCT window (Vorbis window) applied during transform naturally handles the edge
- Window starts at 0 and ramps up smoothly over the overlap region
- Even though the overlap samples are zeros, the windowed contribution is minimal
- For subsequent frames, proper overlap history is maintained

**Remaining considerations:**
1. If false transients still occur on first frame for non-tonal content, that's expected behavior matching libopus
2. The toneishness > 0.90 protection in gopus (line 340-342) is MORE aggressive than libopus
3. The MDCT windowing correctly handles the zero-to-signal transition

fix: NO FIX REQUIRED - Implementation is correct

verification: Compare first-frame transient detection between gopus and libopus with identical input
files_changed: []
