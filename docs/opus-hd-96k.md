# Native 96 kHz Opus HD (QEXT)

How gopus implements libopus 1.6.1's native 96 kHz CELT mode under the
`gopus_qext` build tag. Native **decode** is sample-exact vs the QEXT oracle, and
native **encode** is byte-exact: the public `Encode` at `Fs=96000` runs the
native HD96k path and emits packets identical to libopus `--enable-qext`.

## libopus reference (QEXT oracle)

QEXT is a compile-time option in libopus, gated by `ENABLE_QEXT`. The pinned
1.6.1 tree builds it with:

```
./configure --enable-static --disable-shared --enable-qext
```

This is already wired into the harness: `make ensure-libopus-qext` runs
`tools/ensure_libopus.sh` with `LIBOPUS_ENABLE_QEXT=1`, which configures with the
flags above and produces `tmp_check/opus-1.6.1-qext/`. Oracle helpers select it
with `QEXTRef: true` in `libopustest.CHelperConfig` (compiled with
`-DENABLE_QEXT`, linked against `tmp_check/opus-1.6.1-qext/.libs/libopus.a`).

The native-96k mode oracle is `tools/csrc/libopus_celt_qext_mode_info.c`. It
calls `opus_custom_mode_create(96000, 1920, ...)` and dumps every mode field and
table (scalars, preemph, eBands, logN, window, MDCT twiddles) over the standard
length-prefixed oracle protocol.

## The native 96 kHz CELT mode

Without QEXT, `static_mode_list` holds one mode (`mode48000_960_120`). With
`ENABLE_QEXT`, `static_modes_float.h` is generated with arguments
`48000 960 96000 1920` and the list gains a second entry,
`mode96000_1920_240`. `opus_custom_mode_create(96000, 1920)` selects it via the
static lookup (`shortMdctSize*nbShortMdcts == frame_size<<j`), so it resolves
without `CUSTOM_MODES`.

The 96 kHz mode is a clean 2x scale-up of the 48 kHz fullband mode:

| field          | 48 kHz (`mode48000_960_120`) | 96 kHz (`mode96000_1920_240`) |
|----------------|------------------------------|-------------------------------|
| `Fs`           | 48000                        | 96000                         |
| `overlap`      | 120                          | 240                           |
| `nbEBands`     | 21                           | 21                            |
| `effEBands`    | 21                           | 21                            |
| `maxLM`        | 3                            | 3                             |
| `nbShortMdcts` | 8                            | 8                             |
| `shortMdctSize`| 120                          | 240                           |
| `eBands`       | `eBand5ms`                   | `eBand5ms` (identical)        |
| `logN`         | `logN400`                    | `logN400` (identical)         |
| `window`       | `window120` (overlap 120)    | `window240` (overlap 240)     |
| MDCT `n`       | 1920                         | 3840                          |
| MDCT twiddles  | `mdct_twiddles960` (1800)    | `mdct_twiddles1920` (3600)    |
| `preemph`      | {0.85, 0, 1, 1}              | {0.92300415, 0.22000122, 1.5128347, 0.66101074} |

Key consequences:

- **Bands are unchanged.** `eBand5ms` (the 22-entry boundary table) and `logN400`
  are shared verbatim. 96 kHz spreads the same 21 pseudo-critical bands over
  twice as many MDCT bins; the top band edge (bin 100) now reaches ~40 kHz
  instead of ~20 kHz. The QEXT extension layer (separate from the base mode)
  adds the >20 kHz bands and refinement; that framing is the `qext_cache` /
  `compute_qext_mode` path and is **not** part of this base-mode foundation.
- **Everything length-dependent doubles.** overlap, shortMdctSize, and the
  long-block MDCT N all double. The four MDCT shifts use block sizes
  3840/1920/960/480 (vs 1920/960/480/240 at 48 kHz), so the per-shift KISS-FFT
  states are the larger `fft_state96000_1920_*` (nfft 960/480/240/120).
- **The window and twiddles follow the same closed forms** as 48 kHz, only with
  the doubled length: window `w[i] = sin(.5*pi*sin(.5*pi*(i+.5)/overlap)^2)` with
  overlap=240, and MDCT twiddles `trig[i] = cos(2*pi*(i+.125)/N)` per shift
  segment. gopus computes both from these forms rather than transcribing magic
  numbers, then verifies byte/numeric parity against the oracle.
- **preemph differs** and is taken verbatim from libopus.

The MDCT twiddle table laid out in `mdct_lookup.trig` is the concatenation of the
per-shift segments, length `N - (N2 >> maxshift) = 3840 - (1920>>3) = 3600`,
matching `mdct_twiddles1920[3600]`.

## The mode definition

`celt/mode_hd96k_qext.go` (`//go:build gopus_qext`) defines `HD96kMode` and
`NewHD96kMode()`: the scalar fields above, the shared `eBands`/`logN`, the
overlap-240 window, and the 3600-entry concatenated MDCT twiddle table built from
the libopus closed forms.

`celt/mode_hd96k_qext_test.go` verifies every field against the QEXT oracle:
scalars and the integer eBands/logN tables are exact on all platforms; the
float window and twiddle tables are byte-exact on amd64 (CI hard gate) and bound
the documented darwin/arm64 cosine-kernel residual (see
`project_arm64_celt_1ulp_drift.md`) without failing.

The mode is `gopus_qext`-gated and imported by nothing in the default build.

## Decode: native and sample-exact

The native 96 kHz mode is wired through the public decoder. At `Fs=96000`,
`decoder_96k_qext.go` routes decode to the native HD96k CELT driver
(`celtDecoder.EnableHD96kMode()` + `DecodeFrame` at frameSize=1920): overlap 240,
the 3840-sample MDCT, the HD pre-emphasis, and the larger KISS-FFT states. **No
resampling.** A real native 96 kHz QEXT bitstream decodes to genuine 96 kHz PCM
carrying >24 kHz energy, sample-exact against the QEXT-enabled libopus reference:

- `TestNative96kDecodeMatchesQEXTOracleMono` / `…Stereo` — strict per-frame sample
  parity (amd64/CI hard gate; arm64 bounds the documented ≤1-ULP CELT-kernel tail).
- `TestQEXTDecode96kOracleProducesNative96k` — confirms the decoded output is true
  native 96 kHz (30 kHz energy a 2:1-resampled 48 kHz decode could not produce).

## Encode: native and byte-exact

Native 96 kHz CELT **encode** is wired at the CELT layer (`encoder_96k_qext.go`,
`celt.Encoder.EnableHD96kMode`): the HD-scale analysis MDCT (3840/480), the
band-energy bin scaling (`ScaledBandStart = eBand*frameSize/120`), the HD-scale
comb prefilter (`run_prefilter` max_period = `QEXT_SCALE(COMBFILTER_MAXPERIOD)` =
2048), the 2-tap HD pre-emphasis, the Fs=96000 bitrate/QEXT-reservation budget,
and the >20 kHz extension-band encode into the secondary range coder.

The public `Encode` at `Fs=96000` routes to this native HD96k path
(`tryEncodeNative96k` -> `encoder.EncodeNativeHD96k`) and assembles the full Opus
packet — TOC code 3, padding-length field, main CELT payload, and the reserved
0xF8 QEXT extension — byte-for-byte like libopus `--enable-qext` at Fs=96000.
Both mono and stereo are byte-exact for the main and QEXT payloads
(`TestHD96kNativeEncodeMainPayloadParity` fails on any byte mismatch).

Two implementation details carried the last divergences to zero:

- The HD comb prefilter (`comb_filter_qext`, x!=y) filters the even/odd phases
  with the input delay line (`mem_buf`) and the output buffer kept **separate**,
  so an already-written output sample is never read back as comb input.
- The forward MDCT folds the `1/nfft` FFT scale into the post-rotation twiddles
  under the ENABLE_QEXT scale placement (`mdctQEXTScalePlacement`), matching the
  QEXT `clt_mdct_forward()`; the pre-rotation placement of the default build
  rounds the >20 kHz extension bins by tens of ULP.

SILK/Hybrid modes are not supported at 96 kHz — libopus has no 8/12 kHz resampler
path at Fs=96000, so the native mode is CELT-only fullband.
