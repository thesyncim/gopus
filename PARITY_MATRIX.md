# libopus 1.6.1 Parity Matrix

Reference: pinned `tmp_check/opus-1.6.1/` (libopus **1.6.1**).  
Pinned behavior wins unless a curated fixture documents an intentional divergence.

## Scope

The **stable** surface is default-build core: `Encoder`/`Decoder` (float32 / int16 /
int24), multistream encode/decode, `container/ogg`, `container/red`, and
caller-owned `Encode`/`Decode`. This is what gopus claims as a libopus 1.6.1
drop-in for normal mono/stereo/multistream/Ogg use.

The libopus 1.6 optional surface is exposed exactly the way libopus exposes it:
behind a compile flag in libopus, behind the matching Go build tag here. Every
optional feature defaults to off in a libopus `./configure` build, so the gopus
default build links **zero** of that code (enforced by
`TestDefaultBuildIsZeroCostForGatedFeatures`). The tag <-> libopus-flag mapping:

| gopus build tag | libopus compile flag | libopus default |
| --- | --- | --- |
| `gopus_dred` | `--enable-dred` / `ENABLE_DRED` | no |
| `gopus_extra_controls` | `--enable-osce` / `ENABLE_OSCE` + deep-PLC family (`ENABLE_DEEP_PLC`: PitchDNN/FARGAN) | no |
| `gopus_qext` | `--enable-qext` / `ENABLE_QEXT` | no |
| `gopus_custom` | `--enable-custom-modes` / `CUSTOM_MODES` | no |
| `gopus_fixedpoint` | `--enable-fixed-point` / `FIXED_POINT` | no |

DRED, OSCE (BWE/LACE/NoLACE), QEXT extension framing, and Opus Custom standard
modes are parity-complete and **supported under their build tag** — not
experimental. Two efforts remain in progress and are marked as such below: native
96 kHz (Opus HD) bitstream parity and the full fixed-point pipeline. The
[feature scope](#libopus-16-feature-scope) section tracks each feature,
separating *feature missing* from *implemented, parity-coverage incomplete*.

## Status legend

| Symbol | Meaning |
| --- | --- |
| **Y** | Shipped in gopus; parity gates or oracles pass on the covered surface |
| **~** | Implemented with known quality, numeric, or coverage gaps |
| **T** | Supported under a build tag (`gopus_qext`, `gopus_dred`, `gopus_extra_controls`, `gopus_custom`), mirroring a libopus compile flag; zero-cost in the default build |
| **N** | Not implemented or not exposed on the public API |
| **E** | Example / integration-only (not a stable package API) |

**Parity kinds**

- **Quality** — `opus_compare` Q, correlation, RMS ratio vs libopus on frozen packets  
- **Numeric** — bit-exact or libopus C oracle probes on isolated functions  
- **Byte** — encoded packet bytes match libopus for the probed configuration  

## Quality comparison policy

End-to-end audio parity is judged with **`opus_compare`** — the reference quality
tool shipped with libopus and the metric **RFC 8251** defines conformance with.
Trust does not depend on gopus: it is the same tool and metric the whole Opus
ecosystem and the spec use. The single canonical comparator lives in
`internal/qualitycompare` (`CompareDecodedFloat32` → delay-searched
opus_compare Q + correlation/RMS), with one trusted-bar matrix (`QualityBar`,
`QualityBarForMode`) and one assertion (`AssertQuality`); per-test ad-hoc
`minQ`/`gapQ`/`corr`/`rms`/PCM-tolerance constants are being migrated onto it.

Trusted bars are anchored to two external references, never a hand-picked number:
1. **RFC 8251 conformance** (Q ≥ 0), and
2. **libopus's own cross-build self-variation** — gopus must track the libopus
   reference at least as closely as libopus tracks itself across builds/arches.
   Requiring bit-exactness would hold gopus to a *higher* bar than libopus holds
   itself (e.g. libopus-amd64 differs from libopus-generic on 231/401 frames of a
   2.5 ms chirp), so the residual transcendental/libm/SSE rounding tail is
   governed by quality, not bytes.

Two-tier discipline keeps this honest: **bit-exact numeric oracles for isolated
kernels remain hard gates** (so a quality gate never hides an *algorithmic* bug);
quality gates govern only the end-to-end tail where bit-exactness is bounded by
transcendental/platform rounding, and each carries a documented, proven root
cause. Decode parity (SILK/CELT/Hybrid) meets the near-exact bar (Q ≥ 20,
corr ≥ 0.997); encoder quality matches libopus (gapQ ≈ 0) per arch.

The full design — self-selecting metric tiers, the coded-vs-concealment split,
externally anchored bars, and the build-config matrix that keeps bit-exact
kernels honest across `purego`/arch — is in [docs/parity-testing.md](docs/parity-testing.md).

---

## Modes (SILK / CELT / Hybrid / auto)

| Mode | Encode | Decode | Quality parity | Numeric / byte parity | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| SILK | Y | Y | Y (compliance + decoder matrix) | ~ (NLSF, gain, LTP, stereo oracles; CBR + VBR byte-exact on amd64; VoIP adaptive `hp_cutoff` fix) | arm64-only ≤1-ULP recursive-float tail in the `hp_cutoff` biquad / warped shaping-AR (amd64 exact) |
| CELT | Y | Y | Y (compliance + matrix) | ~ (PVQ/bands 800-cell byte grid amd64-exact; IMDCT + noise-PLC synthesis arm64 bit-exact; CBR byte-exact; band-allocation ≤5%) | arm64-only chirp `alloc_trim` drift from half-density tonality-analysis resampling (amd64 exact) |
| Hybrid | Y | Y | Y (matrix Q>=20, corr>=0.997 — same bar as SILK/CELT; compliance) | ~ (float32 SILK+CELT combine bit-exact stage oracle; stereo DRED carriers byte-exact; 16 kHz hybrid explicit DRED; Hybrid QEXT framing byte-exact; VBR/CVBR byte/size-exact) | Hybrid unconstrained-VBR shares the SILK iter-0 size residual |
| Auto | Y | Y (TOC-driven) | Y (mode fixtures, analysis; FFT 1/nfft normalization + VAD no-re-analysis fixes) | Y (application × rate × frame × signal × channel cross-product, **216/216** cells, no skips) | — |

---

## API sample rates (8 / 12 / 16 / 24 / 48 kHz)

| Rate | Encode API | Decode API | Resample / PLC | Parity evidence | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| 48 kHz | Y | Y | Y | Decoder matrix, compliance, DRED 48 kHz oracles | — |
| 24 kHz | Y | Y | Y | Per-rate decoder matrix (byte-exact), compliance (SILK MB), DRED convert16k | — |
| 16 kHz | Y | Y | Y | Per-rate decoder matrix (byte-exact), SILK NB, 16 kHz hybrid/SILK explicit DRED grid | — |
| 12 kHz | Y | Y | Y | Per-rate decoder matrix (byte-exact), DRED convert16k oracle | — |
| 8 kHz | Y | Y | Y | Per-rate decoder matrix (byte-exact), DRED convert16k, SILK NB | — |

Internal PCM is handled at 48 kHz; API rates use encoder/decoder resamplers. The
per-rate decoder matrix (`decoder_rate_parity_test`, 26 configs × 5 rates) is
byte-exact vs libopus at every sub-48k API rate.

---

## Channels

| Layout | Encode | Decode | Ogg / mapping | Parity evidence | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| Mono | Y | Y | Y | Matrix + compliance | — |
| Stereo | Y | Y | Y | Matrix + compliance; stereo DRED 16k latent conversion bit-exact vs libopus oracle (the channel-blind window advance is genuine libopus behavior) | — |
| Multistream (1–8 ch, family 0/1/255) | Y | Y | Y | Roundtrip + padding; per-stream DRED dormancy verified across all mapping families (mono…7.1, projection) | — (encoder DRED dormant in default build is by design — tag-gated) |
| Projection (family 3) | Y | Y | Y | Public `NewProjectionEncoder`/`Decoder`; demixing-matrix CTLs byte-exact (10 orders); MS DRED stereo carriers byte-exact | Unsupported ambisonics orders return `ErrProjectionOrderUnsupported` (no libopus matrices for them) |
| >2 ch top-level API | N | N | via multistream only | — | By design (RFC-style multistream only) |

---

## Frame sizes (ms @ 48 kHz PCM)

Valid sizes depend on mode (`encoder.ValidFrameSize`). Compliance summary uses 48 kHz sample counts (120 = 2.5 ms, …, 5760 = 120 ms).

| Frame | CELT | SILK | Hybrid | Encoder compliance | Decoder matrix | Gaps for 100% |
| --- | --- | --- | --- | --- | --- | --- |
| 2.5 ms | Y | — | — | Y (mono/stereo FB; 16-cell variant byte ratchet amd64-exact) | Y (`celt-fb-2p5ms`) | arm64-only FMA/tonality residual (amd64 exact) |
| 5 ms | Y | — | — | Y (variant byte ratchet amd64-exact) | Y (`celt-fb-5ms`) | arm64-only FMA/tonality residual (amd64 exact) |
| 10 ms | Y | Y | Y | Y | Y (silk/hybrid/celt) | — |
| 20 ms | Y | Y | Y | Y | Y | — |
| 40 ms | Y | Y | Y | long-frame fixture | partial (silk/hybrid in matrix); SILK + Hybrid stereo DRED carriers byte-exact | — |
| 60 ms | Y | Y | Y | long-frame + summary | Y (`silk-*-60ms`, `celt-fb-60ms-mono-64k`); SILK stereo DRED carriers byte-exact | libopus `audio@FB` 60 ms encodes CELT (matrix row documents that); Hybrid 60 ms compliance only |
| 80 / 100 / 120 ms | Y | Y | Y | encode valid | Y (`silk-wb-80ms`, `celt-fb-80ms`, `silk-wb-120ms`) | 100 ms matrix row; loss fixtures beyond 60 ms |

---

## Bitrate control

| Control | API | Behavior vs libopus | Test coverage | Gaps for 100% |
| --- | --- | --- | --- | --- |
| CBR | Y | Y | Compliance + `encoder_cbr_byte_parity` (SILK/CELT/Hybrid byte-exact; CELT/Hybrid hard on amd64, arm64 FMA residual) | — |
| VBR | Y | ~ | `encoder_vbr_cvbr_byte_parity` — SILK + CELT + Hybrid hard byte/size parity on amd64/CI (full CELT VBR budget for hybrid; VoIP adaptive `hp_cutoff` for SILK) | arm64-only ≤1-ULP `hp_cutoff` biquad recursive-float tail (amd64 exact) |
| Constrained VBR (CVBR) | Y | ~ | `encoder_vbr_cvbr_byte_parity` (SILK/CELT/Hybrid size+range parity on amd64) | same arm64 ≤1-ULP `hp_cutoff` tail as VBR |
| Low delay | Y | Y | `encoder_lowdelay_crossmode_parity` (CELT-only forced; lookahead Fs/400; 360 byte-exact cells) | — |
| DTX | Y | Y | `encoder/dtx_parity_test` + `dtx_sequence_parity` (multi-frame TOC, stereo, hybrid, SILK 10 ms threshold, max-consecutive reset) | — |

---

## Loss paths

| Path | Decode | Encode trigger | Parity | Gaps for 100% |
| --- | --- | --- | --- | --- |
| PLC (`Decode(nil,…)`) | Y | Y (frame_size from buffer; loss tests use last-packet duration like `opus_demo`) | Y (loss fixture, CELT PLC oracle, SILK PLC IIR edge oracles) | — |
| LBRR / in-band FEC | Y | Y | Y (decoder loss fixture, FEC tests; mono-first + stereo-warm LBRR `DecodeWithFEC` parity) | — |
| RTP RED | Y | Y | Y (public `container/red`: RFC 2198 `Parse`/`Build`/`FindRecovery` + fuzz corpus) | RTP RED is outside libopus's core API; recovery ordering (RED→FEC→DRED→PLC) shown in `examples/webrtc-dred-loopback` |
| DRED extension | T | T | Y (process/queue/window oracles; explicit decode + SILK/Hybrid/CELT carriers byte-exact incl. stereo 40/60 ms; 16 kHz; multistream encoder attach) | — |
| Cached DRED recovery | T | — | Y (48 kHz + 16 kHz mono/stereo probes; live-sequence matches cached oracle) | — |
| Multi-gap recovery | T | — | Y (long burst trains bit-exact; multistream per-stream queues corr=1.0; cross-mode SILK/Hybrid→CELT handover bit-exact corr=1.0 — fixed missing transition-PLC deep-PLC state advance) | — |

Recovery ordering in the WebRTC example: **RED → FEC → DRED → PLC** (example only).

---

## Extensions

| Extension | Build | Encode attach | Decode | Parity today | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| QEXT | `gopus_qext` | T | T | ~ (full-packet extension framing + CBR reservation + multistream QEXT byte-parity; CBR reservation bug fixed) | Main CELT-frame bytes = the byte-exact-encode cell; 96 kHz Opus HD not offered |
| DRED | `gopus_dred` | T | T (+ standalone `DREDDecoder`) | Y (RDOVAE; `ConvertTo16kMonoFloat32` bit-exact; explicit decode + carriers byte-exact; burst trains bit-exact; multistream per-stream queues corr=1.0; cross-mode handover bit-exact) | — |
| OSCE BWE | `gopus_extra_controls` | N | T | End-to-end sample parity near-exact (corr ≥ 0.9955, documented architectural −8-sample delay) + forward-pass/feature-extractor parity | — |
| LACE / NoLACE | `gopus_extra_controls` | N | T (CTL + multistream) | End-to-end sample-level **bit-exact** (Q=100, corr=1.0) mono+stereo + multistream per-stream | — |
| DNN blob (`SetDNNBlob`) | `gopus_dred` / `gopus_extra_controls` | T (model load) | T (model load) | USE_WEIGHTS_FILE record framing + libopus model-blob parity; every API DNN family (PitchDNN, PLC, FARGAN, RDOVAE enc/dec, LACE, NoLACE, BBWENet) loads with libopus-oracle parity | — (libopus `LossGen` is opus_demo-only and `FWGAN` is declared-but-unused; both out of scope) |

Default build: **no optional extensions**. `SetDNNBlob` is a zero-cost no-op
returning `ErrOptionalExtensionUnavailable`; USE_WEIGHTS_FILE model loading is
compile-gated like libopus (`ENABLE_DRED`/`ENABLE_OSCE`/`ENABLE_DEEP_PLC`) and
requires `-tags gopus_dred` or `-tags gopus_extra_controls`.

---

## Public API surface

| Area | Status | Parity / tests | Gaps for 100% |
| --- | --- | --- | --- |
| `float32` encode/decode | Y | Hot-path alloc tests, compliance, matrix; arm64 IMDCT/noise-PLC synthesis bit-exact vs libopus | Encode byte grid beyond CELT CBR matrix |
| `int16` encode/decode | Y | Roundtrip, PCM convert oracle; int16 PLC vs libopus across mode × channel × loss-pattern (30 cells Q=100) + int16==float32-quantized identity | — |
| Packet parse (`ParseTOC`, extensions) | Y | Full 256-TOC/config differential vs libopus (`packet_toc_edge_libopus_parity`): bandwidth/channels/nb_frames/samples-per-frame/parse/padding/boundary-reject all bit-exact | — |
| Decoder CTLs | Y | Full `opus_decoder_ctl` equivalence table (60-entry libopus CTL → gopus method → default → tag); fixed `Bandwidth()` pre-decode default | — |
| Encoder CTLs | Y | Full encoder CTL table + `OPUS_GET_*` mirrors; single-stream + multistream CTL parity | — |
| Output gain | Y | Decoder `SetGain`; gain-transition parity vs libopus (applied per-frame, no ramp — matches `opus_decoder.c`, verified across gains × channels) | — |
| Reset / error behavior | Y | Stream + codec reset tests; malformed-packet error-code corpus (20+ classes) 1:1 with libopus `opus_decode` (fixed code-0/code-2 oversized-frame acceptance) | — |
| Multistream API | Y | MS tests, projection oracle, per-stream CTL parity; DRED recovery-queues + QEXT + OSCE verified across layouts | — |

---

## Containers and test vectors

| Asset | Status | Role | Gaps for 100% |
| --- | --- | --- | --- |
| Ogg Opus read/write (`container/ogg`, `stream`) | Y | Demux/mux, projection headers | Fuzz corpus vs libopus ogg decode |
| RFC 6716 / libopus vectors (`testvectors/`) | Y | Decoder matrix (29 cases incl. 100 ms + true Hybrid 40/60 ms rows), per-rate matrix (8/12/16/24/48 kHz byte-exact), loss (to 120 ms), compliance, variants, RED RFC 2198 vectors | Broader real-content corpus (speech/music/noise) |
| `opus_compare` quality oracle | Y | Primary encoder/decoder quality gate + 24-case signal-class corpus (speech/music/mixed/noise/transient/tone/near-silence × modes × bitrates, Q≥99.6) | — |
| `opusdec` crossval fixture | Y | CELT cross-validation (`celt/testdata/opusdec_crossval_fixture.json`) | Regenerate when scenario Ogg hashes change (`GOPUS_UPDATE_OPUSDEC_CROSSVAL_FIXTURE=1`) |
| libopus C oracles (`tools/csrc`, `make test-*-parity`) | ~ | Submodule numerical probes | CI mandatory on all platforms |

---

## libopus 1.6 feature scope

Tracks the optional/ML surface libopus 1.6 added, toward full 1.6.1 parity.
Status: **Y** stable · **T** supported under a build tag (mirrors a libopus
compile flag; zero-cost by default) · **N** not implemented · **OOS** out of
scope. "Feature missing" (no code) is separated from "implemented,
parity-coverage incomplete".

| Feature | libopus 1.6 | gopus status | Kind | Plan for parity |
| --- | --- | --- | --- | --- |
| 24-bit encode/decode | `opus_encode24`/`opus_decode24` (+ multistream/projection) | **Y** — `EncodeInt24`/`DecodeInt24` single + multistream (SILK bit-exact, CELT/Hybrid near-exact per-arch) | implemented | DRED `DecodeInt24` once promoted |
| DRED | `OpusDREDDecoder`, parse/process, decode24 | **T** — explicit decode + carriers byte-exact; burst trains bit-exact; multistream per-stream queues + cross-mode handover bit-exact | implemented (tagged) | DRED `DecodeInt24` once promoted to stable |
| QEXT / Opus HD / 96 kHz | `--enable-qext`, `OPUS_SET_QEXT`, 96 kHz, ≤2 Mb/s | **T** `gopus_qext` — QEXT extension framing + CBR reservation + multistream QEXT byte-parity; 96 kHz `NewEncoder`/`NewDecoder` accepted (resampling wrapper over the 48 kHz CELT core) | implemented (tagged) | Native 96 kHz bitstream parity needs a 1920-sample-MDCT CELT mode (documented in `TestHD96kBoundaryDocumented`); the wrapper is not byte-identical to libopus's native 96 kHz |
| OSCE BWE | `--enable-osce`, runtime BWE, complexity ≥4 | **T** — end-to-end sample parity near-exact (corr ≥ 0.9955) with a documented architectural −8-sample BWE delay-buffer offset; forward-pass + feature-extractor bit/near-exact | implemented (tagged) | — |
| LACE / NoLACE | deep enhancement (NoLACE+BWE) | **T** — end-to-end sample-level **bit-exact** (Q=100, corr=1.0) mono+stereo + multistream per-stream | implemented (tagged) | — |
| Projection / Ambisonics | projection encode/decode(24) | **Y** — public `NewProjectionEncoder`/`NewProjectionDecoder` + demixing-matrix CTLs (byte-exact vs libopus across all 10 supported orders); unsupported orders return `ErrProjectionOrderUnsupported` | implemented | — |
| Opus Custom | optional custom-mode API | **T** `gopus_custom` (≡ libopus `CUSTOM_MODES`) — `celt/custom`: `NewMode`/`NewEncoder`/`NewDecoder` + custom CTLs; standard modes byte-identical to libopus, zero-cost in default builds | implemented (tagged) | Non-standard-rate oracle parity needs a `--enable-custom-modes` libopus build (roundtrip self-consistent today) |
| Fixed-point implementation | float + fixed-point builds | **in progress** — `gopus_fixedpoint` (unimported by default = zero-cost) carries an extensive bit-exact kernel library vs the `--enable-fixed-point` libopus oracle: CELT transform path complete (integer KISS-FFT `kf_bfly2/3/4/5` + driver, integer MDCT fwd/bwd), CELT mathops (`celt_log2`/`exp2`/`rsqrt_norm`/`rcp`/`sqrt`/`sqrt32`/`cos_norm`/`frac_div32`), `compute_band_energies`/`normalise_bands`/`denormalise_bands`/`anti_collapse`/`renormalise_vector`, PVQ `op_pvq_search`+`alg_quant`/`alg_unquant` (range-coder byte-exact), `comb_filter`, `amp2Log2`; SILK encoder analysis kernels (`corrMatrix`/`corrVector`, Burg LPC, `schur`/`k2a` + `schur64`/`k2a_Q16`, `find_LTP`, `process_gains`, `warped_autocorrelation`/`warped_gain`, `apply_sine_window`, `residual_energy`, pitch `calc_energy_st3`, NSQ noise-shape quantizer) + SILK `decode_core` synthesis; integer rangecoder already exact. **Full integer CELT CODEC assembled & bit-exact** vs FIXED_POINT. DECODE (`celt_decoder.go` `DecodeWithEC`): fresh mono/stereo + normal/transient + LM1–3 + 32k–128k, multi-frame stateful, sub-48k downsample (24/16/12/8k), PLC (periodic+noise). ENCODE (`celt_encode.go` `EncodeWithEC`): byte-exact packets mono+stereo + LM0–3 + complexity 0/5/8/10 + 6–510 kb/s, **CBR + VBR + CVBR** with reservoir/drift, multi-frame stateful (`celt_encode_vbr_oracle_test`) | bit-exact kernels + full integer encode+decode incl. rate control; see `docs/fixed-point.md` | Remaining: decode recovery good-frame after loss; encode hybrid(start>0)/LFE/surround/QEXT; SILK encode-frame glue (gopus default SILK is already integer) |
| Public utility API (`opus_pcm_soft_clip`, `opus_strerror`, version) | C-API helpers | **Y** — `PCMSoftClip` (bit-exact vs `opus_pcm_soft_clip`), `ErrorString` (mirrors `opus_strerror`), `VersionString` | implemented | — |

---

## Verification tiers

| Tier | Env | What runs |
| --- | --- | --- |
| fast | `GOPUS_TEST_TIER=fast` | Unit smoke, no heavy fixtures |
| parity | default | Compliance summary, decoder matrix, loss fixtures, oracles |
| exhaustive | `GOPUS_TEST_TIER=exhaustive` | Live `opusdec`/`opus_demo` honesty, fuzz differential |

Release bar: `make verify-production` (+ optional `verify-production-exhaustive`).

---

## Priority backlog (highest impact for “100% parity”)

1. **Native 96 kHz Opus HD / QEXT bitstream** — the `gopus_qext` 96 kHz `NewEncoder`/`NewDecoder` path is a resampling wrapper over the 48 kHz CELT core, not byte-identical to libopus's native 96 kHz. Needs a 1920-sample-MDCT CELT mode (boundary documented in `TestHD96kBoundaryDocumented`).  
2. **Full fixed-point CELT/SILK pipeline** — `gopus_fixedpoint` now carries an extensive bit-exact kernel library (CELT transform path complete: integer FFT + MDCT; CELT mathops/bands/PVQ/`alg_quant`; SILK encoder-analysis kernels + `decode_core` synthesis — all oracle-verified vs `--enable-fixed-point` libopus). Remaining: the sequential assembly of these kernels into full integer `celt_encode`/`celt_decode` + `silk_encode_frame` paths producing bit-exact packets, plus the `NLSF2A`/`A2NLSF`/`find_LPC`/`noise_shape_analysis` driver glue (see `docs/fixed-point.md`).  
3. **Opus Custom non-standard-rate oracle parity** — `gopus_custom` standard modes are byte-identical to libopus; non-standard-rate custom modes are only roundtrip self-consistent and need a `--enable-custom-modes` libopus build to oracle.  
4. **arm64-only ≤1-ULP residuals** — SILK `hp_cutoff` biquad / warped shaping-AR recursive-float tail and CELT chirp `alloc_trim` half-density tonality drift are darwin/arm64 only; amd64/CI is byte-exact. Governed by the per-arch FMA-contraction budget.  
5. **Broader real-content corpus** — decoder/quality coverage beyond the current signal-class corpus (more speech/music/noise material).  
6. **All-platform mandatory C-oracle CI** — the `tools/csrc` C oracles are not yet mandatory across every CI platform.  

---

## Related commands

```sh
make ensure-libopus
make test-core-oracles-parity
make test-qext-parity      # -tags gopus_qext
make test-dred-tag         # -tags gopus_dred
make test-extra-controls-parity
go test ./testvectors/... -run DecoderParity
```

See [README.md](README.md) for support claims and [CONTRIBUTING.md](CONTRIBUTING.md) for verification expectations.
