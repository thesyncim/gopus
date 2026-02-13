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
- release claim: `make agent-release CLAIM_ID=<id>`

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
