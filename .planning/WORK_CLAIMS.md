# Work Claims

Last updated: 2026-03-01

Purpose: coordinate concurrent sessions and prevent overlapping edits.

Older claim history was intentionally pruned on 2026-03-01 to keep this file fast to scan and update.

## Claim Format

```text
- claim: id=<id>; agent=<name>; status=<active|blocked|released>; paths=<comma-separated>; updated=<RFC3339 UTC>; expires=<RFC3339 UTC>; note=<short note>
```

Quick commands:
- `make agent-claims`
- `make agent-claim AGENT=<name> PATHS='silk/,testvectors/' NOTE='short scope note'`
- `make agent-release CLAIM_ID=<id>`

## Active Claims

- claim: id=codex-20260301-142005; agent=codex; status=released; paths=.planning/; updated=2026-03-01T14:23:10Z; expires=2026-03-01T14:23:10Z; note=loop-53 planning docs heavy compaction and archive refresh

## Recent Released Claims

- claim: id=codex-20260301-125328; agent=codex; status=released; paths=testvectors/,encoder/,celt/; updated=2026-03-01T14:22:50Z; expires=2026-03-01T14:22:50Z; note=loop-52: tighten next remaining parity ratchet slack lane
- claim: id=codex-20260301-122333; agent=codex; status=released; paths=testvectors/,encoder/,celt/; updated=2026-03-01T12:53:23Z; expires=2026-03-01T12:53:23Z; note=loop-51: close remaining CELT compliance residual to remove final override
- claim: id=codex-20260301-033044; agent=codex; status=released; paths=testvectors/,encoder/,celt/; updated=2026-03-01T12:23:27Z; expires=2026-03-01T12:23:27Z; note=loop-50: close remaining material compliance override lane (CELT 2.5ms mono)
- claim: id=codex-20260228-232138; agent=codex; status=released; paths=encoder/,hybrid/,silk/,testvectors/,tmp_check/opus-1.6.1/src/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-03-01T01:26:59Z; expires=2026-03-01T01:26:59Z; note=loop-49: close next remaining encoder parity/compliance gap
- claim: id=codex-20260228-203358; agent=codex; status=released; paths=encoder/,hybrid/,silk/,testvectors/,tmp_check/opus-1.6.1/src/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-28T23:21:30Z; expires=2026-02-28T23:21:30Z; note=loop-48: close next worst remaining encoder parity lane
- claim: id=codex-20260228-201422; agent=codex; status=released; paths=encoder/,hybrid/,silk/,testvectors/,tmp_check/opus-1.6.1/src/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-28T20:33:52Z; expires=2026-02-28T20:33:52Z; note=loop-47: close next worst remaining encoder parity lane
- claim: id=codex-20260228-194459; agent=codex; status=released; paths=encoder/,hybrid/,silk/,testvectors/,tmp_check/opus-1.6.1/src/,tmp_check/opus-1.6.1/silk/,.planning/; updated=2026-02-28T20:14:11Z; expires=2026-02-28T20:14:11Z; note=loop-46: close remaining SILK/HYBRID residual parity lanes
- claim: id=codex-20260301-142611; agent=codex; status=released; paths=testvectors/,.planning/; updated=2026-03-01T14:33:12Z; expires=2026-03-01T14:33:12Z; note=loop-54 tighten next SILK ratchet slack lanes
- claim: id=codex-20260301-143444; agent=codex; status=released; paths=testvectors/,.planning/; updated=2026-03-01T14:41:52Z; expires=2026-03-01T14:41:52Z; note=loop-55 tighten SILK WB 20ms am amd64 ratchet floor
- claim: id=codex-20260301-155735; agent=codex; status=released; paths=multistream/,.planning/; updated=2026-03-01T16:04:14Z; expires=2026-03-01T16:04:14Z; note=loop-56: harden libopus ambisonics family3 parity coverage for higher orders
- claim: id=codex-20260301-160817; agent=codex; status=active; paths=testvectors/,.planning/; updated=2026-03-01T16:08:17Z; expires=2026-03-01T20:08:17Z; note=loop-57: tighten next weakest variant parity ratchet lane
