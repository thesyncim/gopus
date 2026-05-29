# libopus 1.6.1 Parity Matrix

Reference: pinned `tmp_check/opus-1.6.1/` (libopus **1.6.1**).  
Pinned behavior wins unless a curated fixture documents an intentional divergence.

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

---

## Modes (SILK / CELT / Hybrid / auto)

| Mode | Encode | Decode | Quality parity | Numeric / byte parity | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| SILK | Y | Y | Y (compliance + decoder matrix) | ~ (NLSF, gain, LTP, stereo oracles) | Auto mode selection vs libopus on edge signals; long-frame stereo DRED carriers; 16 kHz API-path explicit DRED |
| CELT | Y | Y | Y (compliance + matrix) | ~ (PVQ, bands oracles; IMDCT + noise-PLC synthesis arm64 bit-exact stage oracles; CELT encode byte-exact across CBR matrix) | 2.5/5 ms variant byte ratchets; full PVQ/bands byte grid |
| Hybrid | Y | Y | Y (matrix Q>=20, corr>=0.997 — same bar as SILK/CELT; compliance) | ~ (float32 SILK+CELT combine bit-exact stage oracle; Hybrid QEXT vs libopus int16) | SWB/FB stereo DRED byte layout; 16 kHz hybrid explicit decode; QEXT extension byte parity |
| Auto | Y | Y (TOC-driven) | ~ (mode fixtures, analysis) | N | Full cross-product of application × rate × frame × signal class |

---

## API sample rates (8 / 12 / 16 / 24 / 48 kHz)

| Rate | Encode API | Decode API | Resample / PLC | Parity evidence | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| 48 kHz | Y | Y | Y | Decoder matrix, compliance, DRED 48 kHz oracles | — |
| 24 kHz | Y | Y | Y | Compliance (SILK MB), DRED convert16k | Decoder matrix is 48 kHz only; explicit DRED offset matrices thin |
| 16 kHz | Y | Y | Y | SILK NB paths, DRED `ConvertTo16kMonoFloat64`/`Float32` (bit-exact) | Hybrid/SWB explicit DRED; cached multistream 16 kHz matrices partial |
| 12 kHz | Y | Y | Y | DRED convert16k oracle | No dedicated decoder-matrix cases at 12 kHz |
| 8 kHz | Y | Y | Y | DRED convert16k, SILK NB | NB resampler regression guards only |

Internal PCM is handled at 48 kHz; API rates use encoder/decoder resamplers. **100% parity** needs per-rate decoder-matrix rows and explicit-DRED cases at each API rate, not only 48 kHz anchors.

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
| CBR | Y | ~ | Compliance (primary), variants fixture | Per-mode byte-exact CBR packets |
| VBR | Y | ~ | `SetVBR`, compliance bitrates test | Unconstrained VBR byte parity grid |
| Constrained VBR (CVBR) | Y | ~ | Encoder mode + CELT bound scale | CVBR packet-size distribution vs libopus |
| Low delay | Y | ~ | `SetLowDelay` / application | Cross-mode low-delay matrix |
| DTX | Y | ~ | `encoder/dtx_parity_test` (decide_dtx_mode) | Multi-frame DTX TOC sequences; stereo DTX |

---

## Loss paths

| Path | Decode | Encode trigger | Parity | Gaps for 100% |
| --- | --- | --- | --- | --- |
| PLC (`Decode(nil,…)`) | Y | Y (frame_size from buffer; loss tests use last-packet duration like `opus_demo`) | Y (loss fixture, CELT PLC oracle) | Periodic PLC IIR edge cases; hybrid float32 PLC vs legacy widen (guarded in hybrid tests) |
| LBRR / in-band FEC | Y | Y | Y (decoder loss fixture, FEC tests; cadence matches `opus_demo` lossfile) | Mono first packets and stereo warm LBRR packet byte-exact; LBRR gain bump aligned with per-packet `silk_setup_LBRR` |
| RTP RED | E | E | E (`examples/webrtc-dred-loopback`) | **No public `gopus` RED parse/recover API**; no RFC RED vectors in CI |
| DRED extension | T | T | ~ (process/queue/window oracles; explicit decode partial) | SILK explicit decoder; 16 kHz; stereo carriers; live-sequence vs cached oracle; multistream encoder attach |
| Cached DRED recovery | T | — | ~ (48 kHz mono/stereo probes) | 16 kHz cached matrices; CELT NB/SWB explicit; hybrid stereo half-byte divergence |
| Multi-gap recovery | T | — | ~ (recovery queue/window parity) | Long burst trains; cross-mode handover; multistream per-stream queues |

Recovery ordering in the WebRTC example: **RED → FEC → DRED → PLC** (example only).

---

## Extensions

| Extension | Build | Encode attach | Decode | Parity today | Gaps for 100% |
| --- | --- | --- | --- | --- | --- |
| QEXT | `gopus_qext` | T | T | ~ (`celtGLog` energy; Hybrid QEXT decode vs libopus) | Full-packet byte parity all modes/frames; multistream QEXT |
| DRED | `gopus_dred` | T | T (+ standalone `DREDDecoder`) | ~ (RDOVAE; `ConvertTo16kMonoFloat32` bit-exact; process parity) | Encoder carrier bytes per matrix cell; explicit decode grid; `SetDREDDuration` multistream |
| OSCE BWE | `gopus_extra_controls` | N | T (CTL only) | Numeric forward-pass + model blob oracles | End-to-end decode apply; feature extractor; PLC crossfade sample parity |
| LACE / NoLACE | `gopus_extra_controls` | N | T (CTL + multistream) | Forward-pass stage parity (~) | Sample-level decode path; multistream per-stream parity |
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
| `int16` encode/decode | Y | Roundtrip, PCM convert oracle | int16 PLC vs float32 on all modes |
| Packet parse (`ParseTOC`, extensions) | Y | Packet tests, extension scanners | Rare TOC edge codes |
| Decoder CTLs | ~ | Gain, complexity, phase, ignore extensions; DNN blob under `gopus_dred`/`gopus_extra_controls` | Full `opus_decoder_ctl` equivalence table |
| Encoder CTLs | ~ | Bitrate, VBR, FEC, DTX, bandwidth, frame, signal | `OPUS_GET_*` mirror coverage; multistream CTL parity |
| Output gain | Y | Decoder `SetGain` | libopus gain smoothing on transitions |
| Reset / error behavior | Y | Stream + codec reset tests | Error codes 1:1 with libopus for all invalid packets |
| Multistream API | Y | MS tests, projection oracle | DRED/QEXT/OSCE on all channel layouts |

---

## Containers and test vectors

| Asset | Status | Role | Gaps for 100% |
| --- | --- | --- | --- |
| Ogg Opus read/write (`container/ogg`, `stream`) | Y | Demux/mux, projection headers | Fuzz corpus vs libopus ogg decode |
| RFC 6716 / libopus vectors (`testvectors/`) | ~ | Decoder matrix (26 cases incl. `celt-fb-60ms-mono-64k`, `silk-wb-80ms`, `celt-fb-80ms`, `silk-wb-120ms`), loss, compliance, variants | Expand matrix: API decode rates 8–24 kHz, RED, multistream |
| `opus_compare` quality oracle | Y | Primary encoder/decoder quality gate | Broader corpus than summary cases |
| `opusdec` crossval fixture | Y | CELT cross-validation (`celt/testdata/opusdec_crossval_fixture.json`) | Regenerate when scenario Ogg hashes change (`GOPUS_UPDATE_OPUSDEC_CROSSVAL_FIXTURE=1`) |
| libopus C oracles (`tools/csrc`, `make test-*-parity`) | ~ | Submodule numerical probes | CI mandatory on all platforms |

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
