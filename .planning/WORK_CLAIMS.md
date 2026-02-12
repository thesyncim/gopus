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
- claim: id=codex-20260212-185901; agent=codex; status=active; paths=encoder/,celt/,hybrid/,testvectors/; updated=2026-02-12T23:28:00Z; expires=2026-02-13T02:00:00Z; note=CELT quality uplift finalized with full parity/bench/race validation
