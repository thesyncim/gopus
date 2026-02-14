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
