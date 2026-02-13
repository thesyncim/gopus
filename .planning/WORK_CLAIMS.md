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
- claim: id=codex-20260212-230337; agent=codex; status=released; paths=multistream/,encoder/,testvectors/,.planning/; updated=2026-02-12T23:11:14Z; expires=2026-02-12T23:11:14Z; note=complete next libopus parity checklist slice
- claim: id=codex-20260212-231705; agent=codex; status=released; paths=multistream.go,multistream/,encoder.go,decoder.go,testvectors/,.planning/; updated=2026-02-12T23:28:55Z; expires=2026-02-12T23:28:55Z; note=close remaining CTL/API parity gaps slice
- claim: id=codex-20260212-233822; agent=codex; status=released; paths=multistream/encoder.go,multistream/encoder_test.go,.planning/; updated=2026-02-12T23:45:24Z; expires=2026-02-12T23:45:24Z; note=task1: libopus-parity surround analysis and energy-mask production into surroundTrim flow
- claim: id=codex-20260212-235043; agent=codex; status=released; paths=multistream/encoder.go,multistream/encoder_test.go,encoder/encoder.go,celt/encoder.go,celt/encode_frame.go,.planning/; updated=2026-02-13T00:02:16Z; expires=2026-02-13T00:02:16Z; note=task2: implement LFE-aware multistream parity (stream flag/mapping/allocation effects)
- claim: id=codex-20260213-000802; agent=codex; status=released; paths=multistream/encoder.go,multistream/encoder_test.go,.planning/; updated=2026-02-13T00:15:31Z; expires=2026-02-13T00:15:31Z; note=task3: surround per-stream control policy parity (mode/channel/bandwidth)
- claim: id=codex-20260213-014233; agent=codex; status=released; paths=encoder/,celt/,testvectors/,.planning/; updated=2026-02-13T01:48:59Z; expires=2026-02-13T01:48:59Z; note=next loop: strict quality-gap closure slice after parity completion
- claim: id=codex-20260213-015423; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-13T01:59:35Z; expires=2026-02-13T01:59:35Z; note=next loop: improve CELT 10ms stereo strict-quality profile
- claim: id=codex-20260213-020423; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-13T02:10:09Z; expires=2026-02-13T02:10:09Z; note=next loop: improve CELT 2.5ms mono strict-quality profile
- claim: id=codex-20260213-021536; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-13T02:20:40Z; expires=2026-02-13T02:20:40Z; note=next loop: identify and close next strict-quality hotspot after PR45 merge
- claim: id=codex-20260213-022642; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-13T02:31:24Z; expires=2026-02-13T02:31:24Z; note=next loop: continue strict-quality closure on remaining worst CELT profile
- claim: id=codex-20260213-023657; agent=codex; status=released; paths=celt/,testvectors/,.planning/; updated=2026-02-13T02:41:47Z; expires=2026-02-13T02:41:47Z; note=next loop: continue strict-quality closure on CELT 10ms stereo hotspot
