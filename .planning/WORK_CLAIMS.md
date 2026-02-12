# Work Claims

Last updated: 2026-02-12

Purpose: coordinate concurrent agent sessions and avoid overlapping edits.

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
- claim: id=codex-20260212-185901; agent=codex; status=released; paths=encoder/,celt/,hybrid/,testvectors/; updated=2026-02-12T20:09:24Z; expires=2026-02-12T20:09:24Z; note=CELT quality uplift finalized with full parity/bench/race validation
- claim: id=codex-20260212-200240; agent=codex; status=released; paths=tools/,celt/,Makefile; updated=2026-02-12T20:09:24Z; expires=2026-02-12T20:09:24Z; note=fix amd64 opusdec crossval fixture generation for CI parity
- claim: id=codex-20260212-200806; agent=codex; status=released; paths=.planning/ACTIVE.md,.planning/DECISIONS.md,.planning/WORK_CLAIMS.md; updated=2026-02-12T20:09:24Z; expires=2026-02-12T20:09:24Z; note=record amd64 crossval CI-fix evidence and decision
- claim: id=codex-20260212-201823; agent=codex; status=released; paths=encoder/,silk/,testvectors/; updated=2026-02-12T20:25:48Z; expires=2026-02-12T20:25:48Z; note=next big issue: SILK/Hybrid absolute quality uplift
- claim: id=codex-20260212-203618; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-12T20:42:30Z; expires=2026-02-12T20:42:30Z; note=post-merge ratchet + next short-frame quality fix
