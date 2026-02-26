# Work Claims

Last updated: 2026-02-13

Purpose: coordinate concurrent agent sessions and avoid overlapping edits.

History archive: `.planning/archive/WORK_CLAIMS_2026-02-13_full.txt`

## Claim Format

Preferred single-line format:

```text
- claim: id=<id>; agent=<name>; status=<active|blocked|released>; paths=<comma-separated>; updated=<RFC3339 UTC>; expires=<RFC3339 UTC>; note=<short note>
```

Use shared path surfaces so overlap detection stays deterministic:
- `encoder/`
- `silk/`
- `celt/`
- `hybrid/`
- `testvectors/`
- `tools/`
- root docs (`AGENTS.md`, `CODEX.md`, `CLAUDE.md`, `Makefile`)

Quick commands:
- list claims: `make agent-claims`
- add claim: `make agent-claim AGENT=<name> PATHS='silk/,testvectors/' NOTE='short scope note'`
- publish claim immediately (required before edits):
  - `git add .planning/WORK_CLAIMS.md`
  - `git commit --only .planning/WORK_CLAIMS.md -m "chore(claims): <agent> claim <paths>"`
  - `git push`
- release claim: `make agent-release CLAIM_ID=<id>`
- publish release immediately (required):
  - `git add .planning/WORK_CLAIMS.md`
  - `git commit --only .planning/WORK_CLAIMS.md -m "chore(claims): release <claim_id>"`
  - `git push`

## Active Claims

- claim: id=template; agent=none; status=released; paths=; updated=2026-02-12T00:00:00Z; expires=2026-02-12T00:00:00Z; note=replace when claiming work

## Recent Released Claims

- claim: id=codex-20260213-140019; agent=codex; status=released; paths=encoder/,multistream/,.planning/; updated=2026-02-13T14:08:52Z; expires=2026-02-13T14:08:52Z; note=delay compensation parity gated by application state
- claim: id=codex-20260213-134727; agent=codex; status=released; paths=multistream/,encoder/,.planning/; updated=2026-02-13T13:54:15Z; expires=2026-02-13T13:54:15Z; note=multistream application forwarding parity
- claim: id=codex-20260213-132816; agent=codex; status=released; paths=encoder/,multistream/,.planning/; updated=2026-02-13T13:37:56Z; expires=2026-02-13T13:37:56Z; note=lookahead parity by application
- claim: id=codex-20260213-132213; agent=codex; status=released; paths=encoder/,multistream/,.planning/; updated=2026-02-13T13:22:24Z; expires=2026-02-13T13:22:24Z; note=application ctl first-frame lock parity
- claim: id=codex-20260213-125839; agent=codex; status=released; paths=encoder/,multistream/,testvectors/; updated=2026-02-13T13:08:15Z; expires=2026-02-13T13:08:15Z; note=surroundTrim producer/control parity slice
- claim: id=codex-20260213-151510; agent=codex; status=released; paths=encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T15:30:17Z; expires=2026-02-13T15:30:17Z; note=libopus vbr/cvbr default and control transition parity
- claim: id=codex-20260213-154047; agent=codex; status=released; paths=celt/,encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T15:48:34Z; expires=2026-02-13T15:48:34Z; note=gate CELT quality uplifts out of constrained-VBR path for libopus bitrate policy parity
- claim: id=codex-20260213-155347; agent=codex; status=released; paths=encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T16:18:07Z; expires=2026-02-13T16:18:07Z; note=retest default constrained-vbr parity flip after CVBR target-envelope fixes
- claim: id=codex-20260213-190151; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T19:14:10Z; expires=2026-02-13T19:14:10Z; note=libopus analysis.c feature-assembly source port
- claim: id=codex-20260213-192204; agent=codex; status=released; paths=celt/,encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T19:42:18Z; expires=2026-02-13T19:42:18Z; note=wire libopus analysis max_pitch_ratio into CELT prefilter path
- claim: id=codex-20260213-194816; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T20:03:36Z; expires=2026-02-13T20:03:36Z; note=identify and port next libopus quality/parity gap with fixture evidence
- claim: id=codex-20260213-200926; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T20:15:54Z; expires=2026-02-13T20:15:54Z; note=port libopus tonality_analysis digital-silence behavior and validate parity
- claim: id=codex-20260213-202101; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T20:31:12Z; expires=2026-02-13T20:31:12Z; note=port libopus analyzer NaN guard behavior and validate parity
- claim: id=codex-20260213-203834; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T20:50:59Z; expires=2026-02-13T20:50:59Z; note=port libopus analyzer reset semantics parity and validate gates
- claim: id=codex-20260213-210234; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T21:02:39Z; expires=2026-02-13T21:02:39Z; note=propagate LSB depth into analyzer noise-floor parity and add focused coverage
- claim: id=codex-20260213-211525; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-13T21:25:49Z; expires=2026-02-13T21:25:49Z; note=port libopus analyzer feature-vector and math parity slice with quality/parity validation
- claim: id=codex-20260213-214325; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/src/; updated=2026-02-13T22:06:20Z; expires=2026-02-13T22:06:20Z; note=port exact long-SWB control flow from libopus and validate fixtures
- claim: id=codex-20260213-222545; agent=codex; status=released; paths=multistream/,encoder/,celt/,testvectors/; updated=2026-02-13T22:35:55Z; expires=2026-02-13T22:35:55Z; note=wire per-stream surround energy mask parity with libopus control flow
- claim: id=codex-20260213-224929; agent=codex; status=released; paths=encoder/,celt/,testvectors/,tmp_check/opus-1.6.1/; updated=2026-02-13T22:57:37Z; expires=2026-02-13T22:57:37Z; note=next libopus quality parity slice after surround energy-mask merge
- claim: id=codex-20260214-000138; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T00:08:45Z; expires=2026-02-14T00:08:45Z; note=close next libopus source-parity gap (analysis/control)
- claim: id=codex-20260214-001805; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T00:31:02Z; expires=2026-02-14T00:31:02Z; note=point1: complete analyzer trace coverage matrix
- claim: id=codex-20260214-003151; agent=codex; status=released; paths=celt/,encoder/,multistream/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T00:50:06Z; expires=2026-02-14T00:50:06Z; note=point2: replace cvbr guardrails with direct libopus flow
- claim: id=codex-20260214-005142; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T00:58:12Z; expires=2026-02-14T00:58:12Z; note=point3: tighten ModeAuto analyzer-invalid fallback to libopus flow
- claim: id=codex-20260214-005904; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T01:14:44Z; expires=2026-02-14T01:14:44Z; note=point4: add frame-level mode-trace fixture parity guard
- claim: id=codex-20260214-093205; agent=codex; status=released; paths=encoder/,celt/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T10:34:29Z; expires=2026-02-14T10:34:29Z; note=post-merge quality loop: next libopus source-port gap
- claim: id=codex-20260214-105924; agent=codex; status=released; paths=encoder/,celt/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T11:55:02Z; expires=2026-02-14T11:55:02Z; note=post-merge quality loop: close CELT chirp + hybrid SWB impulse gap
- claim: id=codex-20260214-111502; agent=codex; status=released; paths=decoder.go,testvectors/,tools/; updated=2026-02-14T11:27:35Z; expires=2026-02-14T11:27:35Z; note=libopus-backed FEC/PLC parity fixtures and DecodeWithFEC semantics
- claim: id=codex-20260214-113935; agent=codex; status=released; paths=encoder/,celt/,testvectors/,.planning/; updated=2026-02-14T11:42:18Z; expires=2026-02-14T11:42:18Z; note=post-merge loop: next strict-quality libopus parity uplift
- claim: id=codex-20260214-114813; agent=codex; status=released; paths=decoder.go,decoder_test.go,testvectors/,tools/,.planning/; updated=2026-02-14T11:52:20Z; expires=2026-02-14T11:52:20Z; note=iteration2: expand decoder loss pattern coverage and parity confidence
- claim: id=codex-20260214-115710; agent=codex; status=released; paths=decoder.go,decoder_test.go,testvectors/,.planning/; updated=2026-02-14T11:59:02Z; expires=2026-02-14T11:59:02Z; note=iteration3: tighten decode_fec frame-size transition behavior
- claim: id=codex-20260214-121209; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/,.planning/; updated=2026-02-14T12:24:38Z; expires=2026-02-14T12:24:38Z; note=close remaining Hybrid-SWB mode mismatch and strict-quality gap with source-port parity
- claim: id=codex-20260216-205408; agent=codex; status=released; paths=multistream/,.planning/; updated=2026-02-16T20:59:40Z; expires=2026-02-16T20:59:40Z; note=libopus default mapping matrix parity expansion
- claim: id=codex-20260216-210527; agent=codex; status=released; paths=multistream/,.planning/; updated=2026-02-16T21:08:45Z; expires=2026-02-16T21:08:45Z; note=add multistream decode-side sample-count parity guard in libopus matrix
- claim: id=codex-20260216-211407; agent=codex; status=released; paths=multistream/,.planning/; updated=2026-02-16T21:19:58Z; expires=2026-02-16T21:19:58Z; note=add multistream libopus frame-duration matrix coverage (10/20/40/60ms)
- claim: id=codex-20260216-212713; agent=codex; status=released; paths=multistream/,.planning/; updated=2026-02-16T21:46:19Z; expires=2026-02-16T21:46:19Z; note=add libopus ambisonics family 2/3 multistream parity coverage
- claim: id=codex-20260216-214635; agent=codex; status=released; paths=examples/,README.md,.planning/; updated=2026-02-16T21:55:03Z; expires=2026-02-16T21:55:03Z; note=add staggered arrival audio mixing sample and docs
- claim: id=codex-20260216-214743; agent=codex; status=released; paths=container/ogg/,multistream/,.planning/; updated=2026-02-16T21:59:53Z; expires=2026-02-16T21:59:53Z; note=family3 demixing-matrix metadata for Ogg interop parity
- claim: id=codex-20260216-220216; agent=codex; status=released; paths=container/ogg/,multistream/,testvectors/,.planning/; updated=2026-02-16T22:18:28Z; expires=2026-02-16T22:18:28Z; note=port libopus family3 default projection matrix parity slice
- claim: id=codex-20260216-221858; agent=codex; status=released; paths=multistream/,container/ogg/,testvectors/,.planning/; updated=2026-02-16T22:31:40Z; expires=2026-02-16T22:31:40Z; note=investigate and close family3 opusdec decode interop gap
- claim: id=codex-20260216-223158; agent=codex; status=released; paths=multistream/,tmp_check/opus-1.6.1/src/,testvectors/,.planning/; updated=2026-02-16T22:44:43Z; expires=2026-02-16T22:44:43Z; note=port family3 projection mixing matrix path from libopus
- claim: id=codex-20260216-223510; agent=codex; status=released; paths=examples/,README.md,.planning/; updated=2026-02-16T22:39:24Z; expires=2026-02-16T22:39:24Z; note=harden mix-arrivals for webrtc-like streaming tracks
- claim: id=codex-20260216-224750; agent=codex; status=released; paths=multistream/,tools/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-16T23:02:53Z; expires=2026-02-16T23:02:53Z; note=replace opusdec family2/3 parity with direct libopus decoder helper
- claim: id=codex-20260216-225446; agent=codex; status=released; paths=examples/,README.md,.planning/; updated=2026-02-16T22:56:53Z; expires=2026-02-16T22:56:53Z; note=add explicit runtime AddTrack interface for stream mixer
- claim: id=codex-20260216-231420; agent=codex; status=released; paths=examples/,README.md,.planning/; updated=2026-02-16T23:23:58Z; expires=2026-02-16T23:23:58Z; note=add playback mode for mix-arrivals sample
- claim: id=codex-20260216-230303; agent=codex; status=released; paths=multistream/,tmp_check/opus-1.6.1/src/,testvectors/,.planning/; updated=2026-02-16T23:16:58Z; expires=2026-02-16T23:16:58Z; note=close internal ambisonics decode drift vs direct libopus reference helper
- claim: id=codex-20260216-231658; agent=codex; status=released; paths=multistream/,tmp_check/opus-1.6.1/src/,testvectors/,.planning/; updated=2026-02-16T23:24:02Z; expires=2026-02-16T23:24:02Z; note=identify and close next multistream libopus parity gap
- claim: id=codex-20260216-232445; agent=codex; status=released; paths=multistream/,tmp_check/opus-1.6.1/src/,testvectors/,.planning/; updated=2026-02-18T19:31:10Z; expires=2026-02-18T19:31:10Z; note=tighten surround stereo/51/71 direct libopus waveform parity
- claim: id=codex-20260218-193120; agent=codex; status=released; paths=testvectors/,.planning/; updated=2026-02-18T19:41:10Z; expires=2026-02-18T19:41:10Z; note=remove ffmpeg fallback from compliance decode path
- claim: id=codex-20260218-194956; agent=codex; status=released; paths=AGENTS.md,README.md,.planning/; updated=2026-02-18T20:10:44Z; expires=2026-02-18T20:10:44Z; note=drop Q>=0 objective and set parity-first target
- claim: id=codex-20260218-201052; agent=codex; status=released; paths=testvectors/,.planning/; updated=2026-02-18T20:34:38Z; expires=2026-02-18T20:34:38Z; note=use libopus reference decode in variant parity quality path
- claim: id=codex-20260218-204458; agent=codex; status=released; paths=encoder/,testvectors/,.planning/; updated=2026-02-18T20:57:31Z; expires=2026-02-18T20:57:31Z; note=port opus_packet_pad code3 no-pad repacketization parity
- claim: id=codex-20260218-210313; agent=codex; status=released; paths=encoder/,silk/,hybrid/,testvectors/,.planning/; updated=2026-02-18T22:05:00Z; expires=2026-02-18T22:05:00Z; note=close remaining SILK/HYBRID libopus quality gaps
- claim: id=codex-20260218-220532; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-19T02:10:20Z; expires=2026-02-19T02:10:20Z; note=next gap: restricted-silk fixture parity
- claim: id=codex-20260220-185206; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/src/; updated=2026-02-20T19:11:00Z; expires=2026-02-20T19:11:00Z; note=next gap: Hybrid-FB-10ms-mono-64k parity calibration
- claim: id=codex-20260220-191754; agent=codex; status=released; paths=testvectors/,.planning/; updated=2026-02-20T19:31:17Z; expires=2026-02-20T19:31:17Z; note=compliance no-negative-gap guard + fixture governance hardening
- claim: id=codex-20260220-193223; agent=codex; status=released; paths=encoder/,hybrid/,testvectors/,.planning/; updated=2026-02-20T19:42:32Z; expires=2026-02-20T19:42:32Z; note=close HYBRID-SWB-10ms residual variant parity gap
- claim: id=codex-20260220-194247; agent=codex; status=released; paths=decoder.go,decoder_test.go,testvectors/,.planning/; updated=2026-02-20T19:56:17Z; expires=2026-02-20T19:56:17Z; note=investigate and fix FEC quality/parity gap against libopus
- claim: id=codex-20260220-205947; agent=codex; status=released; paths=encoder/,hybrid/,testvectors/,.planning/; updated=2026-02-21T07:01:45Z; expires=2026-02-21T07:01:45Z; note=close HYBRID-SWB-10ms residual chirp parity gap via libopus source-port
- claim: id=codex-20260221-070202; agent=codex; status=released; paths=encoder/,silk/,celt/,hybrid/,testvectors/,.planning/; updated=2026-02-21T13:34:06Z; expires=2026-02-21T13:34:06Z; note=port prediction-disabled parity semantics from libopus 1.6.1
- claim: id=codex-20260220-222951; agent=codex; status=released; paths=decoder.go,decoder_test.go; updated=2026-02-20T22:33:21Z; expires=2026-02-20T22:33:21Z; note=align decode_fec CELT gating to libopus mode semantics
- claim: id=codex-20260220-223819; agent=codex; status=released; paths=decoder.go,decoder_test.go; updated=2026-02-20T22:38:57Z; expires=2026-02-20T22:38:57Z; note=align no-LBRR decode_fec fallback cadence with opus_demo/libopus behavior
- claim: id=codex-20260220-223859; agent=codex; status=released; paths=decoder.go,decoder_opus_frame.go,decoder_test.go; updated=2026-02-20T22:48:01Z; expires=2026-02-20T22:48:01Z; note=fix PLC-for-FEC state override in decodeOpusFrameInto nil path
- claim: id=codex-20260220-224808; agent=codex; status=released; paths=decoder.go,decoder_fec_hybrid.go,decoder_fec_silk.go,decoder_opus_frame.go,decoder_test.go; updated=2026-02-20T22:56:57Z; expires=2026-02-20T22:56:57Z; note=align decode_fec fallback to PLC when provided packet has no LBRR
- claim: id=codex-20260220-225657; agent=codex; status=released; paths=decoder.go,decoder_opus_frame.go,decoder_test.go; updated=2026-02-20T23:00:48Z; expires=2026-02-20T23:00:48Z; note=investigate and align remaining doublet_stride7 FEC/PLC delay cadence
- claim: id=codex-20260220-230048; agent=codex; status=released; paths=decoder.go,decoder_opus_frame.go,decoder_test.go,silk/decode.go,silk/lbrr_decode.go; updated=2026-02-23T10:44:06Z; expires=2026-02-23T10:44:06Z; note=align SILK PLC glue ordering with libopus in loss recovery paths
- claim: id=codex-20260221-133410; agent=codex; status=released; paths=encoder/,hybrid/,silk/,celt/,testvectors/,.planning/; updated=2026-02-23T10:44:06Z; expires=2026-02-23T10:44:06Z; note=close HYBRID-SWB-10ms chirp parity residual via libopus source port
- claim: id=codex-20260223-020717; agent=codex; status=released; paths=plc/,testvectors/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-23T05:44:43Z; expires=2026-02-23T05:44:43Z; note=doublet_stride7 next PLC source-port slice
- claim: id=codex-20260223-060114; agent=codex; status=released; paths=silk/,hybrid/,decoder.go,testvectors/,tmp_check/opus-1.6.1/silk/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-23T08:02:06Z; expires=2026-02-23T08:02:06Z; note=next doublet_stride7 parity slice after #178
- claim: id=codex-20260223-080228; agent=codex; status=released; paths=silk/,hybrid/,decoder.go,testvectors/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-23T08:08:34Z; expires=2026-02-23T08:08:34Z; note=lossCnt source alignment for SILK PLC cadence
- claim: id=codex-20260223-084802; agent=codex; status=released; paths=silk/,hybrid/,decoder.go,testvectors/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-23T09:11:31Z; expires=2026-02-23T09:11:31Z; note=sync outBuf state during PLC concealment
- claim: id=codex-20260223-091144; agent=codex; status=released; paths=plc/,celt/,decoder.go,testvectors/,tmp_check/opus-1.6.1/celt/,.planning/; updated=2026-02-23T10:44:06Z; expires=2026-02-23T10:44:06Z; note=next largest PLC drift: CELT stress lanes
- claim: id=codex-20260223-104410; agent=codex; status=released; paths=encoder/,celt/,hybrid/,testvectors/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-23T17:13:54Z; expires=2026-02-23T17:13:54Z; note=close HYBRID-SWB-10ms transition payload drift via libopus state-cadence port
- claim: id=codex-20260223-095352; agent=codex; status=released; paths=celt/,plc/,testvectors/,.planning/,tmp_check/opus-1.6.1/celt/; updated=2026-02-23T17:13:54Z; expires=2026-02-23T17:13:54Z; note=port remaining libopus CELT periodic PLC decay/energy cadence after early periodic uplift
- claim: id=codex-20260223-171408; agent=codex; status=released; paths=encoder/,testvectors/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-23T17:37:20Z; expires=2026-02-23T17:37:20Z; note=close HYBRID-SWB impulse parity via transition prefill/redundancy cadence port
- claim: id=codex-20260223-173726; agent=codex; status=released; paths=encoder/,hybrid/,testvectors/,tmp_check/opus-1.6.1/src/,.planning/; updated=2026-02-23T18:44:53Z; expires=2026-02-23T18:44:53Z; note=close HYBRID-SWB-10ms impulse parity residual via libopus source-port
- claim: id=codex-20260223-184519; agent=codex; status=released; paths=celt/,testvectors/,tmp_check/opus-1.6.1/celt/,.planning/; updated=2026-02-23T19:12:23Z; expires=2026-02-23T19:12:23Z; note=next gap: port additional libopus periodic CELT PLC cadence
- claim: id=codex-20260225-112131; agent=codex; status=released; paths=go.mod,.planning/; updated=2026-02-25T11:24:34Z; expires=2026-02-25T11:24:34Z; note=downgrade go version in go.mod and prepare PR
- claim: id=codex-20260225-213604; agent=codex; status=released; paths=testvectors/,tools/,.planning/; updated=2026-02-25T22:03:27Z; expires=2026-02-25T22:03:27Z; note=recalibrate libopus ref_q summary rows and add fixture honesty guard
- claim: id=codex-20260225-220350; agent=codex; status=released; paths=encoder/,testvectors/,.planning/; updated=2026-02-26T03:03:02Z; expires=2026-02-26T03:03:02Z; note=next encoder parity slice after compliance provenance alignment
- claim: id=codex-20260226-030308; agent=codex; status=released; paths=silk/,testvectors/,.planning/; updated=2026-02-26T08:22:24Z; expires=2026-02-26T08:22:24Z; note=next decoder loss parity slice after mono PLC sMid fix
- claim: id=codex-20260226-082241; agent=codex; status=released; paths=silk/,testvectors/,.planning/; updated=2026-02-26T08:54:57Z; expires=2026-02-26T08:54:57Z; note=next decoder loss parity slice after sLPC history writeback merge
- claim: id=codex-20260226-085514; agent=codex; status=released; paths=celt/,plc/,testvectors/,.planning/; updated=2026-02-26T09:24:12Z; expires=2026-02-26T09:24:12Z; note=next decoder loss parity slice after SILK CNG cadence merge
- claim: id=codex-20260226-092418; agent=codex; status=released; paths=celt/,plc/,testvectors/,.planning/; updated=2026-02-26T20:12:49Z; expires=2026-02-26T20:12:49Z; note=next decoder loss parity slice after repeated-loss decay merge
