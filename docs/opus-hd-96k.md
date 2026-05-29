# Native 96 kHz Opus HD (QEXT)

How gopus targets byte-parity with libopus 1.6.1's native 96 kHz CELT mode, and
what the foundation layer under the `gopus_qext` build tag currently provides.

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

## What the foundation provides

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

## Remaining work (not in this layer)

This is the mode definition only. Still to come for full native-96k byte parity:

1. Wire the 3840-point forward/inverse MDCT and the larger KISS-FFT states
   (nfft up to 960) into the CELT encode/decode pipelines at 96 kHz.
2. Route the public API at `Fs=96000` to the native mode instead of the current
   2:1 resampling wrapper (`sample_rate_qext.go`, `encoder_96k_qext.go`,
   `decoder_96k_qext.go`), which decimates to 48 kHz today.
3. The QEXT extension bitstream (>20 kHz bands, `compute_qext_mode` /
   `qext_cache`, extension framing) layered on top of the base 96 kHz mode.
4. Full-packet encode/decode parity against `opus_demo`/`qext_compare` from the
   QEXT build.
