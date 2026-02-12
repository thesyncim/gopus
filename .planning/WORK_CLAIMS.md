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
- claim: id=codex-20260212-204240; agent=codex; status=released; paths=README.md,docs/,AGENTS.md,.planning/; updated=2026-02-12T20:49:22Z; expires=2026-02-12T20:49:22Z; note=sync README/docs/assembly docs with current code and commands
- claim: id=codex-20260212-211003; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-12T21:15:52Z; expires=2026-02-12T21:15:52Z; note=next issue: CELT 2.5ms short-frame quality uplift round 2
- claim: id=codex-20260212-211731; agent=codex; status=released; paths=testvectors/; updated=2026-02-12T21:22:37Z; expires=2026-02-12T21:22:37Z; note=tighten precision floors generally after celt short-frame uplift
- claim: id=codex-20260212-214208; agent=codex; status=released; paths=celt/,testvectors/; updated=2026-02-12T21:56:43Z; expires=2026-02-12T21:56:43Z; note=next issue: celt short-frame stereo quality uplift after PR31 merge
- claim: id=codex-20260212-213600; agent=codex; status=released; paths=.github/workflows/,Makefile,CI_GUARDRAILS.md,.planning/; updated=2026-02-12T21:37:34Z; expires=2026-02-12T21:37:34Z; note=speed up CI by parallelizing linux verification without reducing coverage
- claim: id=codex-20260212-214301; agent=codex; status=released; paths=.github/workflows/,CI_GUARDRAILS.md,.planning/; updated=2026-02-12T22:44:58Z; expires=2026-02-12T22:44:58Z; note=PR + CI timing iteration for faster pipeline
- claim: id=codex-20260212-224458; agent=codex; status=released; paths=encoder.go,multistream.go,packet.go,packet_test.go,testvectors/; updated=2026-02-12T22:58:04Z; expires=2026-02-12T22:58:04Z; note=parity slice: public ctl/api + repacketizer + ambisonics tests
