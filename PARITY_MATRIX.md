# libopus 1.6.1 Parity Matrix

Reference: pinned `tmp_check/opus-1.6.1/` (libopus **1.6.1**).  
Pinned behavior wins unless a curated fixture documents an intentional divergence.

## Scope

The **stable** surface is default-build core: `Encoder`/`Decoder` (float32 / int16 /
int24), multistream encode/decode, `container/ogg`, `container/red`, and
caller-owned `Encode`/`Decode`. This is what gopus claims as a libopus 1.6.1
drop-in for normal mono/stereo/multistream/Ogg use.

The libopus 1.6 optional/ML surface — QEXT (Opus HD / 96 kHz), DRED, OSCE
(BWE/LACE/NoLACE), Opus Custom, and the fixed-point implementation — is
**build-tagged and experimental** (or out of scope), not part of the stable
claim. The ambition is full libopus 1.6.1 parity; the [feature scope](#libopus-16-feature-scope)
section tracks each toward that, separating *feature missing* from *implemented,
parity-coverage incomplete*.

## Status legend

| Symbol | Meaning |
| --- | --- |
| **Y** | Shipped in gopus; parity gates or oracles pass on the covered surface |
| **~** | Implemented with known quality, numeric, or coverage gaps |
| **T** | Requires build tag (`gopus_qext`, `gopus_dred`, or `gopus_extra_controls`) |
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
| SILK | Y | Y | Y (compliance + decoder matrix) | ~ (NLSF, gain, LTP, stereo oracles; CBR byte-exact) | SILK unconstrained-VBR ≤3-byte/frame size drift (SILK-FLP iter-0 shaping-gain) |
| CELT | Y | Y | Y (compliance + matrix) | ~ (PVQ, bands oracles; IMDCT + noise-PLC synthesis arm64 bit-exact stage oracles; CELT encode byte-exact across CBR matrix) | 2.5/5 ms variant byte ratchets; full PVQ/bands byte grid |
| Hybrid | Y | Y | Y (matrix Q>=20, corr>=0.997 — same bar as SILK/CELT; compliance) | ~ (float32 SILK+CELT combine bit-exact stage oracle; stereo DRED carriers byte-exact; 16 kHz hybrid explicit DRED; QEXT framing byte-exact; VBR/CVBR byte/size-exact) | Hybrid unconstrained-VBR shares the SILK iter-0 size residual |
| Auto | Y | Y (TOC-driven) | Y (mode fixtures, analysis) | Y (application × rate × frame × signal × channel cross-product, 214/216 cells) | 2 arm64 VOIP cells: documented 1-ULP tonality-analysis FMA drift (amd64 all 216 pass) |

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
| Stereo | Y | Y | Y | Matrix + compliance | Stereo DRED latent pointer bug documented in `internal/dred` trace test |
| Multistream (1–8 ch, family 0/1/255) | Y | Y | Y | Roundtrip + padding tests | Encoder DRED dormant default build; decoder dormancy on some mappings |
| Projection (family 3) | Y | Y | Y | Demixing matrix libopus oracle | Ambisonics orders outside {4,6,9,…} unsupported; multistream DRED stereo carriers partial |
| >2 ch top-level API | N | N | via multistream only | — | By design (RFC-style multistream only) |

---

## Frame sizes (ms @ 48 kHz PCM)

Valid sizes depend on mode (`encoder.ValidFrameSize`). Compliance summary uses 48 kHz sample counts (120 = 2.5 ms, …, 5760 = 120 ms).

| Frame | CELT | SILK | Hybrid | Encoder compliance | Decoder matrix | Gaps for 100% |
| --- | --- | --- | --- | --- | --- | --- |
| 2.5 ms | Y | — | — | Y (mono/stereo FB) | Y (`celt-fb-2p5ms`) | Variant-byte ratchet surface incomplete |
| 5 ms | Y | — | — | Y | Y (`celt-fb-5ms`) | Same |
| 10 ms | Y | Y | Y | Y | Y (silk/hybrid/celt) | — |
| 20 ms | Y | Y | Y | Y | Y | — |
| 40 ms | Y | Y | Y | long-frame fixture | partial (silk/hybrid in matrix) | SILK stereo 40 ms DRED carrier; hybrid 40 ms stereo DRED |
| 60 ms | Y | Y | Y | long-frame + summary | Y (`silk-*-60ms`, `celt-fb-60ms-mono-64k`) | libopus `audio@FB` 60 ms encodes CELT (matrix row documents that); Hybrid 60 ms compliance only; SILK stereo 60 ms DRED |
| 80 / 100 / 120 ms | Y | Y | Y | encode valid | Y (`silk-wb-80ms`, `celt-fb-80ms`, `silk-wb-120ms`) | 100 ms matrix row; loss fixtures beyond 60 ms |

---

## Bitrate control

| Control | API | Behavior vs libopus | Test coverage | Gaps for 100% |
| --- | --- | --- | --- | --- |
| CBR | Y | Y | Compliance + `encoder_cbr_byte_parity` (SILK/CELT/Hybrid byte-exact; CELT/Hybrid hard on amd64, arm64 FMA residual) | — |
| VBR | Y | ~ | `encoder_vbr_cvbr_byte_parity` (CELT + Hybrid byte/size-exact, hard-asserted; full CELT VBR budget for hybrid) | SILK ≤3-byte/frame size drift — SILK-FLP iter-0 shaping-gain path, cross-platform; needs frame-level SILK encoder-control oracle to bisect |
| Constrained VBR (CVBR) | Y | ~ | `encoder_vbr_cvbr_byte_parity` (CELT + Hybrid size+range parity) | SILK CVBR per-frame size (same SILK-FLP iter-0 root as VBR) |
| Low delay | Y | Y | `encoder_lowdelay_crossmode_parity` (CELT-only forced; lookahead Fs/400; 360 byte-exact cells) | — |
| DTX | Y | Y | `encoder/dtx_parity_test` + `dtx_sequence_parity` (multi-frame TOC, stereo, hybrid, SILK 10 ms threshold, max-consecutive reset) | — |

---

## Loss paths

| Path | Decode | Encode trigger | Parity | Gaps for 100% |
| --- | --- | --- | --- | --- |
| PLC (`Decode(nil,…)`) | Y | Y (frame_size from buffer; loss tests use last-packet duration like `opus_demo`) | Y (loss fixture, CELT PLC oracle, SILK PLC IIR edge oracles) | — |
| LBRR / in-band FEC | Y | Y | Y (decoder loss fixture, FEC tests; mono-first + stereo-warm LBRR `DecodeWithFEC` parity) | — |
| RTP RED | Y | Y | Y (public `container/red`: RFC 2198 `Parse`/`Build`/`FindRecovery` + fuzz corpus) | RTP RED is outside libopus's core API; recovery ordering (RED→FEC→DRED→PLC) shown in `examples/webrtc-dred-loopback` |
| DRED extension | T | T | ~ (process/queue/window oracles; explicit decode partial) | SILK explicit decoder; 16 kHz; stereo carriers; live-sequence vs cached oracle; multistream encoder attach |
| Cached DRED recovery | T | — | ~ (48 kHz mono/stereo probes) | 16 kHz cached matrices; CELT NB/SWB explicit; hybrid stereo half-byte divergence |
| Multi-gap recovery | T | — | Y (long burst trains bit-exact; multistream per-stream queues corr=1.0; cross-mode handover structurally exact — ret/Blend/LossCount) | Cross-mode (SILK/Hybrid→CELT) DRED-recovery PCM corr≈0.97: SILK feeds different LPCNet spectral features to the concealment bridge (16k CELT-PLC feature gap) |

Recovery ordering in the WebRTC example: **RED → FEC → DRED → PLC** (example only).

---

## Extensions

| Extension | Build | Encode attach | Decode | Parity today | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| QEXT | `gopus_qext` | T | T | ~ (full-packet extension framing + CBR reservation + multistream QEXT byte-parity; CBR reservation bug fixed) | Main CELT-frame bytes = the byte-exact-encode cell; 96 kHz Opus HD not offered |
| DRED | `gopus_dred` | T | T (+ standalone `DREDDecoder`) | ~ (RDOVAE; `ConvertTo16kMonoFloat32` bit-exact; explicit decode + carriers byte-exact; burst trains bit-exact; multistream per-stream queues corr=1.0) | Cross-mode (SILK/Hybrid→CELT) recovery PCM corr≈0.97 (16k CELT-PLC feature gap) |
| OSCE BWE | `gopus_extra_controls` | N | T | End-to-end sample parity near-exact (corr ≥ 0.9955, documented architectural −8-sample delay) + forward-pass/feature-extractor parity | — |
| LACE / NoLACE | `gopus_extra_controls` | N | T (CTL + multistream) | End-to-end sample-level **bit-exact** (Q=100, corr=1.0) mono+stereo + multistream per-stream | — |
| DNN blob (`SetDNNBlob`) | `gopus_dred` / `gopus_extra_controls` | T (model load) | T (model load) | USE_WEIGHTS_FILE record framing + libopus model-blob parity | Model coverage beyond pitch/PLC/FARGAN/RDOVAE families |

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
| Decoder CTLs | ~ | Gain, complexity, phase, ignore extensions; DNN blob under `gopus_dred`/`gopus_extra_controls` | Full `opus_decoder_ctl` equivalence table |
| Encoder CTLs | ~ | Bitrate, VBR, FEC, DTX, bandwidth, frame, signal | `OPUS_GET_*` mirror coverage; multistream CTL parity |
| Output gain | Y | Decoder `SetGain` | libopus gain smoothing on transitions |
| Reset / error behavior | Y | Stream + codec reset tests; malformed-packet error-code corpus (20+ classes) 1:1 with libopus `opus_decode` (fixed code-0/code-2 oversized-frame acceptance) | — |
| Multistream API | Y | MS tests, projection oracle | DRED/QEXT/OSCE on all channel layouts |

---

## Containers and test vectors

| Asset | Status | Role | Gaps for 100% |
| --- | --- | --- | --- |
| Ogg Opus read/write (`container/ogg`, `stream`) | Y | Demux/mux, projection headers | Fuzz corpus vs libopus ogg decode |
| RFC 6716 / libopus vectors (`testvectors/`) | Y | Decoder matrix (29 cases incl. 100 ms + true Hybrid 40/60 ms rows), per-rate matrix (8/12/16/24/48 kHz byte-exact), loss (to 120 ms), compliance, variants, RED RFC 2198 vectors | Broader real-content corpus (speech/music/noise) |
| `opus_compare` quality oracle | Y | Primary encoder/decoder quality gate | Broader corpus than summary cases |
| `opusdec` crossval fixture | Y | CELT cross-validation (`celt/testdata/opusdec_crossval_fixture.json`) | Regenerate when scenario Ogg hashes change (`GOPUS_UPDATE_OPUSDEC_CROSSVAL_FIXTURE=1`) |
| libopus C oracles (`tools/csrc`, `make test-*-parity`) | ~ | Submodule numerical probes | CI mandatory on all platforms |

---

## libopus 1.6 feature scope

Tracks the optional/ML surface libopus 1.6 added, toward full 1.6.1 parity.
Status: **Y** stable · **T** tagged-experimental · **N** not implemented · **OOS**
out of scope. "Feature missing" (no code) is separated from "implemented,
parity-coverage incomplete".

| Feature | libopus 1.6 | gopus status | Kind | Plan for parity |
| --- | --- | --- | --- | --- |
| 24-bit encode/decode | `opus_encode24`/`opus_decode24` (+ multistream/projection) | **Y** — `EncodeInt24`/`DecodeInt24` single + multistream (SILK bit-exact, CELT/Hybrid near-exact per-arch) | implemented | DRED `DecodeInt24` once promoted |
| DRED | `OpusDREDDecoder`, parse/process, decode24 | **T** — tagged control/standalone; explicit decode + carriers byte-exact; burst trains bit-exact; multistream per-stream queues corr=1.0 | coverage incomplete | cross-mode (SILK/Hybrid→CELT) recovery PCM corr≈0.97 (16k CELT-PLC feature gap) |
| QEXT / Opus HD / 96 kHz | `--enable-qext`, `OPUS_SET_QEXT`, 96 kHz, ≤2 Mb/s | **T** — QEXT extension framing + CBR reservation + multistream QEXT byte-parity (CBR reservation bug fixed) / **N** 96 kHz API | coverage incomplete (main CELT-frame bytes = the byte-exact-encode cell) + 96 kHz out of public scope | 96 kHz Opus HD **not** offered at the public API |
| OSCE BWE | `--enable-osce`, runtime BWE, complexity ≥4 | **T** — end-to-end sample parity near-exact (corr ≥ 0.9955) with a documented architectural −8-sample BWE delay-buffer offset; forward-pass + feature-extractor bit/near-exact | implemented (tagged) | — |
| LACE / NoLACE | deep enhancement (NoLACE+BWE) | **T** — end-to-end sample-level **bit-exact** (Q=100, corr=1.0) mono+stereo + multistream per-stream | implemented (tagged) | — |
| Projection / Ambisonics | projection encode/decode(24) | **Y** — public `NewProjectionEncoder`/`NewProjectionDecoder` + demixing-matrix CTLs (byte-exact vs libopus across all 10 supported orders); unsupported orders return `ErrProjectionOrderUnsupported` | implemented | — |
| Opus Custom | optional custom-mode API | **OOS** | out of scope | nonstandard frame sizes; not a Go-library goal unless requested |
| Fixed-point implementation | float + fixed-point builds | **OOS** | out of scope | gopus is pure-Go float; int16/int24 are I/O conveniences, not a fixed-point pipeline |
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

1. **Decoder matrix** — add 8/12/16/24 kHz rows, 80/120 ms, multistream, and loss+FEC+DRED combinations (60 ms CELT row landed).  
2. **DRED** — close SILK explicit decode, stereo/long-frame carriers, 16 kHz hybrid, multistream encoder attach.  
3. **RED** — promote WebRTC RED helpers to tested public API or document permanent example-only scope.  
4. **Byte-exact encode** — CBR/CVBR/VBR grid per `encoderComplianceSummaryCases` + variants ratchet.  
5. **OSCE** — wire BWE/LACE decode apply with sample-level libopus oracles (not only forward-pass).  
6. **QEXT** — extension byte parity beyond Hybrid waveform gates; multistream QEXT.  
7. **Hybrid long frames** — find libopus `opus_demo` params that emit true Hybrid at 40/60 ms for matrix rows (today `audio@FB` 60 ms is CELT).  

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
