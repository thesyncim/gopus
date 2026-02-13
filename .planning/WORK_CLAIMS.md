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

- claim: id=codex-20260213-151510; agent=codex; status=released; paths=encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T15:30:17Z; expires=2026-02-13T15:30:17Z; note=libopus vbr/cvbr default and control transition parity
- claim: id=codex-20260213-154047; agent=codex; status=released; paths=celt/,encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T15:48:34Z; expires=2026-02-13T15:48:34Z; note=gate CELT quality uplifts out of constrained-VBR path for libopus bitrate policy parity
- claim: id=codex-20260213-155347; agent=codex; status=released; paths=encoder/,multistream/,testvectors/, .planning/; updated=2026-02-13T16:18:07Z; expires=2026-02-13T16:18:07Z; note=retest default constrained-vbr parity flip after CVBR target-envelope fixes
- claim: id=codex-20260213-170100; agent=codex; status=released; paths=encoder/,testvectors/; updated=2026-02-13T18:56:22Z; expires=2026-02-13T18:56:22Z; note=port libopus run_analysis cadence for long SWB parity
