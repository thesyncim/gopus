# Native sub-48 kHz SILK / Hybrid encode parity — architecture map and staged plan

Goal: make gopus byte-exact with libopus `opus_encode(Fs, …)` for native SILK and
Hybrid encode at the sub-48 kHz API rates (8 / 12 / 16 / 24 kHz), without
regressing the 48 kHz byte-exact encode path.

This note is the scope/architecture artifact. It does not change production code.

---

## 1. What libopus actually does (reference: `tmp_check/opus-1.6.1/`)

### 1.1 Public API contract — frame size is in NATIVE-Fs samples
`src/opus_encoder.c`:
- `opus_encoder_init`: `st->Fs = Fs`, `st->silk_mode.API_sampleRate = st->Fs`,
  `st->encoder_buffer = st->Fs/100`, `st->delay_compensation = st->Fs/250`,
  `tonality_analysis_init(&st->analysis, st->Fs)`. The whole encoder is
  parameterised by the native `Fs`.
- `frame_size_select` (line 827): rejects `frame_size < Fs/400`; valid sizes are
  `(Fs/400)<<n` (2.5 ms … ) and `n*Fs/50` (20 ms … ). So at 16 kHz a 20 ms call is
  `opus_encode(enc, pcm, 320, …)` — **320 native samples, not 960**.
- `compute_stereo_width`, bitrate helpers, `compute_dred_bitrate`, hp_cutoff, etc.
  all use `st->Fs` and the native `frame_size`.

### 1.2 SILK internal sample rate selection (`fs_kHz`) — always 8 / 12 / 16
`silk/control_codec.c`:
- `silk_control_encoder`: `psEnc->sCmn.API_fs_Hz = encControl->API_sampleRate`;
  picks `fs_kHz = silk_control_audio_bandwidth(...)` (or `force_fs_kHz` for the
  side channel == mid's `fs_kHz`); then `silk_setup_resamplers(psEnc, fs_kHz)` and
  `silk_setup_fs(psEnc, fs_kHz, payloadSize_ms)`.
- `silk/control_audio_bandwidth.c` `silk_control_audio_bandwidth`: clamps the
  internal rate to `min(desiredInternal_fs_Hz, API_fs_Hz, maxInternal_fs_Hz)` and
  `max(minInternal_fs_Hz)`, landing on **8 / 12 / 16 kHz only**. (Bandwidth-switch
  state machine steps 16↔12↔8.)
- `silk/control_codec.c` `silk_setup_fs`: `celt_assert(fs_kHz == 8 || 12 || 16)`.

So SILK internal is **never 24 kHz**. `fs_kHz` is exactly what gopus already
computes via `silk.GetBandwidthConfig(e.silkBandwidth()).SampleRate` (NB=8000,
MB=12000, WB/SWB/FB=16000). The internal-rate selection is therefore already
correct in gopus; only the resampler that feeds SILK is wrong.

### 1.3 The SILK input resampler — `API_fs_Hz → fs_kHz`, NOT `48000 → fs_kHz`
`silk/control_codec.c` `silk_setup_resamplers` (line 146):
```
silk_resampler_init(&resampler_state, psEnc->sCmn.API_fs_Hz, fs_kHz*1000, /*forEnc=*/1);
```
`silk/enc_API.c` (line 282–340): the per-frame input is resampled with
`silk_resampler(&resampler_state, ...)` and the **input sample count** is
```
nSamplesFromInput = nSamplesToBuffer * API_fs_Hz / (fs_kHz*1000);   // line 289
```
i.e. the number of API-rate input samples consumed per internal buffer chunk is
derived from `API_fs_Hz`, never from 48000.

`silk/resampler.c` `silk_resampler_init` (forEnc=1) supports
`Fs_Hz_in ∈ {8,12,16,24,48,96}k`, `Fs_Hz_out ∈ {8,12,16}k`, selecting one of:
| function (`resampler_function`) | when |
| --- | --- |
| `copy` (0) | `Fs_in == Fs_out` (8→8, 12→12, 16→16) |
| `up2_HQ_wrapper` (1) | `Fs_out == 2*Fs_in` (8→16) |
| `IIR_FIR` (2) with up2x | other upsample (8→12, 12→16) |
| `down_FIR` (3) | downsample 3:4, 2:3, 1:2, 1:3, 1:4, 1:6 |

The `delay_matrix_enc[6][3]` (in / out = {8,12,16,24,48,96} / {8,12,16}) gives the
`inputDelay`. The function/ratio table from the C comment (in `resampler.c`):
```
        out  8     12    16
 in 8    C     UF    U
    12   AF    C     UF
    16   AF    AF    C
    24   AF    AF    AF
    48   AF    AF    AF
    96   …     …     …
```
(C=copy, U=up2, UF=up2+FIR, AF=AR2+FIR, D=down2.)

### 1.4 Decode side is already correct
The decoder matrix is byte-exact at every sub-48k API rate (`decoder_rate_parity_test`).
This task is **encode-only**.

---

## 2. What gopus does today (and why sub-48k SILK/Hybrid is wrong)

### 2.1 The internal `encoder.Encoder` is already mostly native-Fs aware
`encoder/encoder.go`:
- `NewEncoder(sampleRate, channels)` stores native `sampleRate` (8/12/16/24/48k).
- Throughout it uses `sampleRate := int(e.sampleRate)`, `Fs/250`, `Fs/400`,
  `Fs/100`, `frameRate = sampleRate/frameSize`, and `NewTonalityAnalysisState(sampleRate)`.
- `encoder/analysis.go` already branches on `s.Fs != 48000` / `case 48000:` for the
  analysis downmix+resample (native-Fs aware).
- `encoder/celt_encode_fixedpoint.go` (`gopus_fixedpoint`): the integer CELT path
  **already consumes native-Fs frame sizes** — `celtFixedUpsample()` returns the
  `resampling_factor` (48k→1, 24k→2, 16k→3, 12k→4, 8k→6), and
  `celtFixedEncodeInScope` requires `frameSize*upsample ∈ {120,240,480,960}`.
  `TestPublicCELTEncodeFixedRateByteExact` drives the **public** encoder at
  16k/320 etc. and is byte-exact vs `ProbeCELTFixedEncodeRate`.

### 2.2 But the SILK input path treats input as 48 kHz
`encoder/encoder.go` SILK resampler wiring (the bug):
- `ensureSILKResampler(rate)` (line 3716): `silk.NewDownsamplingResampler(48000, rate)`
  — source rate **hardcoded 48000**.
- per-frame input count: `targetSamples := frameSize * targetRate / 48000`
  (lines 2201, 2250, 2840, 2969) — divisor **hardcoded 48000**.
- identity-bypass guard: `if targetRate != 48000 { …resample… }`
  (lines 2200, 2249, 2837, 2898, 2953) — compares to **48000**, not native Fs.

`silk.DownsamplingResampler` (`silk/resample_down_fir.go`) only implements the
**down_FIR** core + `delay_matrix_enc`. It has no copy / up2 / IIR_FIR path; any
unsupported ratio silently falls through to a 1:3 fallback.

### 2.3 The public wrapper enforces the 48k-equivalent frame-size convention
- `controls_common.go`: `validFrameSize` ∈ {120,240,480,960,1920,2880,3840,4800,5760}
  (48 kHz counts), `expertFrameDurationFrameSize` 20 ms ⇒ 960.
- `encoder.go`/`encoder_encode.go`: the public `gopus.Encoder.Encode` requires
  `len(pcm) == frameSize*channels` where `frameSize` is the 48k-equivalent count
  (default 960), then passes that straight to
  `enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, …)`.
- `encoder.ValidFrameSize` (line 4109) hardcodes the same 48k counts per mode.

Net effect at, e.g., 16 kHz today:
- Public `Encode` demands **960** samples (probe: 320 → `ErrInvalidFrameSize`).
- Those 960 "16 kHz" samples are fed to `NewDownsamplingResampler(48000,16000)`
  and downsampled **1:3 → 320**, i.e. the encoder treats native 16 kHz input as
  if it were 48 kHz. The audio content per frame is wrong (≈3× too much time).
- Internal `encoder.Encoder.Encode(pcm, 320)` in SILK mode → `ErrInvalidConfig`
  (probe), because the SILK/Hybrid dispatch + framing are written around 960.

### 2.4 The 960-based assumptions that must move to native-Fs
Grep targets (all in `encoder/encoder.go` unless noted):
- SILK input resampler: lines 2200–2206, 2249–2263, 2837–2842, 2898–2912,
  2953–2976, 3716–3731 (the `48000` literals + `NewDownsamplingResampler`).
- `packetTOCFrameSize` (2785) already does API→48k TOC scaling but is gated on
  `fixedCELTUsedForTOC()` (only the integer CELT path today).
- Mode dispatch / framing keyed on 960: 990, 1064, 1090–1095, 1471–1472,
  `encodeCELTMultiFramePacket` (3201: `frameSize/960`, `frameStride = 960`),
  `encodeHybridMultiFramePacket`, DRED-in-subframes (264 in `dred_runtime.go`,
  970–971), `hybrid.go` (109, 210, 766, 813 — `frameSize != 480 && != 960`).
- Public/MS wrapper frame-size contract: `controls_common.go`
  (`validFrameSize`, `expertFrameDurationFrameSize`), `encoder.ValidFrameSize`,
  `encoder_encode.go`, `multistream_encode.go`, `SetFrameSize`/`apiFrameSize`.

### 2.5 There is no existing sub-48k SILK/Hybrid **encode byte-parity** test
All `encodeAPIRate{SILK,CELT,Hybrid}Packet` helpers encode at **48000** and feed
the decoder matrix. PARITY_MATRIX's "Encode = Y" for sub-48k reflects "internal
PCM handled at 48 kHz; API rates use … resamplers" — i.e. decode-side. The
encoder is never exercised at native sub-48k input. So this work needs new oracle
tests, and is not currently protected by parity (low regression risk to add).

---

## 3. Key design decisions (locked)

1. **The native-Fs contract is the target.** After this work the public (and
   internal `encoder`) `Encode(pcm, frameSize)` takes `frameSize` in native-Fs
   samples (libopus `opus_encode(Fs)` semantics): 20 ms = `Fs/50`. The TOC config
   is computed from the 48k-equivalent duration via `packetTOCFrameSize` (already
   present), so on-wire packets are unchanged in meaning.

   - 48 kHz: native == 48k-equivalent, so the contract is unchanged and existing
     48k tests/callers keep working byte-for-byte.
   - This is a **public-API behavior change at sub-48k** (frame-size count
     changes 960→320 at 16 kHz). It mirrors libopus exactly. Document in
     PARITY_MATRIX + release notes; it is the same contract libopus exposes.

2. **Reuse `silk.LibopusResampler` as the SILK input resampler.**
   `silk/resample_libopus.go` `NewLibopusResampler(fsIn, fsOut)` already ports the
   full `silk_resampler_init` dispatch: down (delegates to down_FIR), `copyMode`
   (identity), `up2HQMode` (2:1), and IIR_FIR (fractional up). The ONE correctness
   gap for encoder use: it sets `inputDelay` from `delay_matrix_dec` via `rateID`,
   but the encoder needs `delay_matrix_enc` via `rateID` for forEnc. Add a
   `forEnc` constructor variant (or a `NewLibopusResamplerEnc`) that selects
   `delayMatrixEnc[rateIDEnc(fsIn)][outIdx]`. Everything else (the IIR_FIR/up2/copy
   math + state snapshot/restore used for lookahead) is already present and
   decoder-proven.

3. **Internal SILK rate stays exactly `GetBandwidthConfig(silkBandwidth()).SampleRate`.**
   No change to internal-rate selection; it already matches
   `silk_control_audio_bandwidth` (8/12/16k).

4. **Keep the integer CELT-only native path as-is.** It already works at sub-48k
   (`gopus_fixedpoint`). The float CELT-only sub-48k path needs the same native
   frame-size handling as SILK/Hybrid (it currently piggybacks on the 48k-count
   convention); fold it into the frame-size-contract stage.

5. **Zero-cost / build-tag contract.** No new default-build struct fields beyond
   what's needed; mirror libopus default-vs-flag exactly. `gopus_qext` 96 kHz keeps
   its existing 2:1-to-48k wrapper (it is a separate, already-shipped path).

---

## 4. Staged implementation plan

Each stage must keep **48 kHz encode byte-identical** and the hard-constraint
test set green:
```
GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
  go test -run 'Encode|ByteParity|EncoderCompliance' ./encoder/ .
go test -timeout 2400s ./testvectors/        # EXIT=0
# plus: TestDefaultBuildIsZeroCostForGatedFeatures, tools/check_type_parity.py
```

### Stage 0 — Oracle + test scaffolding (no production change) — INDEPENDENT
Add a libopus oracle that runs `opus_encode(Fs, pcm, Fs/50, …)` natively at
8/12/16/24 kHz (mono + stereo, SILK + Hybrid, a few bitrates/bandwidths) and a
gopus-side test that drives the **native** frame size, asserting byte-exact. It
will fail/skip until later stages land. Lives next to
`celt_encode_fixedpoint_rate_parity_test.go` (extend its harness; reuse
`libopustest.Probe*`). This unblocks every other stage with a red→green signal.

Files: new `encoder/silk_encode_rate_parity_test.go` (+ `internal/libopustest` probe).
Risk: none (test-only).

### Stage 1 — SILK encoder input resampler: native `API_fs → fs_kHz` — CORE
Make the SILK input resampler use the native API rate as its source.

1a. (silk pkg, INDEPENDENT) Add `forEnc` support to `LibopusResampler`:
    `newLibopusResampler(fsIn, fsOut, forEnc bool)` selecting `delayMatrixEnc`
    (via `rateIDEnc`) when `forEnc`, else `delayMatrixDec`. Public
    `NewLibopusResamplerEnc(fsIn, fsOut)`. Unit-test the `inputDelay` + a 16→16
    copy / 8→16 up2 / 24→16 down round-trip vs the `silk_resampler` reference.
    This is pure-additive in `silk` and cannot affect the encoder until wired.

1b. (encoder pkg) Replace the SILK-input plumbing in `encoder/encoder.go`:
    - `ensureSILKResampler(rate)` builds `silk.NewLibopusResamplerEnc(int(e.sampleRate), rate)`
      (and a right-channel twin) instead of `NewDownsamplingResampler(48000, rate)`.
      Store the resampler behind its existing `silkResampler*`-shaped fields (the
      `LibopusResampler` type already exposes `State/SetState/ProcessInto/
      ProcessInt16Into`), or introduce a small interface so both 48k-down and
      native paths share call sites.
    - Replace `targetRate != 48000` guards with `targetRate != int(e.sampleRate)`
      (identity when API rate already equals the internal rate — e.g. 16 kHz WB).
    - Replace `frameSize * targetRate / 48000` (and the lookahead variant) with
      `frameSize * targetRate / int(e.sampleRate)`.
    At 48 kHz every one of these is numerically identical to today ⇒ 48k path
    byte-unchanged. At sub-48k it now matches `silk_setup_resamplers` +
    `nSamplesFromInput`.

    Sub-risk: `LibopusResampler` vs `DownsamplingResampler` must be bit-identical
    for the 48k→{8,12,16} down case the 48k path uses. `LibopusResampler` delegates
    down to `newDecoderDownsamplingResampler` (decoder delay matrix). The **encoder**
    48k-down path currently uses `NewDownsamplingResampler` (encoder delay matrix).
    `delay_matrix_enc[48][·] = {18,10,12}` vs `delay_matrix_dec` has no 48-in row,
    so the down delegate must also honor `forEnc` (1a wires this). Verify the 48k
    SILK byte-exact tests stay green after 1b — this is the critical gate.

Files: `silk/resample_libopus.go` (+ small test), `encoder/encoder.go` (resampler
call sites only). Risk to 48k: medium — guarded by the 48k SILK byte-exact tests.

### Stage 2 — Native frame-size contract through the encode dispatch — CORE
Teach the encode pipeline that `frameSize` is native-Fs.

2a. (encoder pkg) Generalise the 960-keyed framing to `Fs/50`:
    - Define `e.frame20ms()` = `int(e.sampleRate)/50` and use it in the SILK/
      Hybrid/CELT dispatch (`encodeXMultiFramePacket` split count = `frameSize /
      frame20ms`, stride = `frame20ms*channels`), the `frameSize > 960` / `<= 960`
      mode gates, DRED-in-subframes, and `hybrid.go` `frameSize ∈ {480,960}`
      (→ `{Fs/100, Fs/50}`).
    - Make `packetTOCFrameSize` unconditional (drop the `fixedCELTUsedForTOC()`
      gate) so the TOC config is always the 48k-equivalent duration. Verify 48k is
      unchanged (at 48k it returns `frameSize`).
    - Ensure SILK frame-length validation accepts native counts (the
      `ErrInvalidConfig` seen at 16k/320) — route SILK `payloadSize_ms` from
      `frameSize / (Fs/1000)` rather than `frameSize/48`.

2b. (public + MS wrapper) Switch the public frame-size contract to native:
    - `controls_common.go`: `validFrameSize`/`validateFrameSize`/
      `expertFrameDurationFrameSize` parameterised by `Fs` (valid = `(Fs/400)<<n`
      and `n*Fs/50`), and `encoder.ValidFrameSize` likewise. Keep a 48k overload
      so existing 48k callers/tests are unaffected.
    - `encoder.go`/`encoder_encode.go`/`multistream_encode.go`/`SetFrameSize`/
      `apiFrameSize`: expected `len(pcm)` and the default frame size become native
      (`Fs/50` default; 48k ⇒ 960 as today). `is96kHz` path unchanged.

Files: `encoder/encoder.go`, `encoder/hybrid.go`, `encoder/dred_runtime.go`,
`controls_common.go`, `encoder.go`, `encoder_encode.go`, `encoder_controls.go`,
`multistream_encode.go`, `multistream.go`. Risk to 48k: medium — guarded by the
full encode parity + testvectors set. Depends on Stage 1.

### Stage 3 — Hybrid native-Fs specifics — CORE (depends on 1+2)
Hybrid = SILK lowband (internal 16 kHz, fed by Stage-1 resampler) + CELT highband
at native Fs. Confirm the CELT highband path consumes the native frame size
(it shares the CELT seam) and that the SILK/CELT delay-compensation split
(`Fs/250`, `Fs/400`) is taken at native Fs (it already reads `e.sampleRate`).
Drive the Stage-0 Hybrid oracle to green at 24 kHz (SWB-ish) and 16 kHz.

Files: `encoder/hybrid.go`, `encoder/encoder.go` (hybrid frame seam). Risk: medium.

### Stage 4 — Multistream + cleanup — INDEPENDENT-ish (depends on 2)
- Multistream encode already delegates per-stream to `encoder.Encoder`; once the
  per-stream contract is native, verify MS sub-48k SILK/Hybrid byte-parity and the
  per-stream `curr_max` budgeting (`bitrateToBitsFs` already uses `Fs`).
- Update PARITY_MATRIX (sub-48k encode rows → genuinely native), docs, and the
  `parity_matrix_sync_test`. Refresh `tools/check_type_parity.py` baseline only if
  cleanup removed legacy findings (per AGENTS.md).

Files: `multistream*.go`, `PARITY_MATRIX.md`, `docs/`. Risk: low.

---

## 5. Parallel decomposition (for fan-out)

Dependency DAG (→ = "must land first"):

```
Stage 0  (oracle/tests)         ── independent, do first; gives every other stage a signal
Stage 1a (silk resampler forEnc)── independent (silk pkg only)
Stage 1b (wire SILK input rate) ── needs 1a
Stage 2a (dispatch 960→Fs/50)   ── independent of 1 at the code level; can be built in parallel,
                                    but only verifiable end-to-end after 1b
Stage 2b (public frame contract)── needs 2a
Stage 3  (hybrid)               ── needs 1b + 2a/2b
Stage 4  (multistream + docs)   ── needs 2b
```

Three agents can work concurrently with low collision risk:
- **Agent A — silk package:** Stage 1a entirely inside `silk/` (resampler `forEnc`
  + unit tests). No `encoder/` edits. Zero collision.
- **Agent B — encoder dispatch framing:** Stage 2a (960→`Fs/50` in the dispatch /
  multi-frame / hybrid framing) plus making `packetTOCFrameSize` unconditional.
  Touches `encoder/encoder.go` framing + `encoder/hybrid.go` + `encoder/dred_runtime.go`.
- **Agent C — public/MS frame-size contract:** Stage 2b/4 (`controls_common.go`,
  `encoder.go`, `encoder_encode.go`, `encoder_controls.go`, `multistream_encode.go`,
  `encoder.ValidFrameSize`). Touches the wrapper layer, not the SILK math.

The only shared file is `encoder/encoder.go` (Agents B touches framing/dispatch,
the Stage-1b integration touches the resampler call sites). Split by line region
(resampler wiring ~2200–3010 + 3716–3731 vs dispatch/framing ~990–1500/3198+),
or serialise 1b → 2a on that one file. Stages 0 and 1a have no overlap with
anything and should start immediately.

Recommended order for a single driver: 0 → 1a → 1b (gate on 48k SILK byte-exact) →
2a → 2b (gate on full encode parity + testvectors) → 3 → 4.

---

## 6. Regression guardrails (run after every production change)

- 48k byte-parity (hard constraint):
  `GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test -run 'Encode|ByteParity|EncoderCompliance' ./encoder/ .`
- `go test -timeout 2400s ./testvectors/` (EXIT=0).
- `TestDefaultBuildIsZeroCostForGatedFeatures` + `tools/check_type_parity.py`.
- New sub-48k native oracle tests (Stage 0) flip red→green as stages land; they
  must never be relaxed to pass.

Baseline at the time of writing: the hard-constraint set is GREEN
(`./encoder/` ~18 s, top-level ~185 s, both `ok`). The integer CELT-only sub-48k
native path (`TestPublicCELTEncodeFixedRateByteExact`, `gopus_fixedpoint`) is
already byte-exact and is the proof that native-Fs frame sizes work end-to-end
through the public encoder for CELT.
