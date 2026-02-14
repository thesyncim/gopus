# Codex Context

Use `AGENTS.md` as the canonical project context.
If this file and `AGENTS.md` differ, `AGENTS.md` is authoritative.
When in doubt, use `tmp_check/opus-1.6.1/` libopus source as the source of truth.

Session quick start:

1. Read: `AGENTS.md`, `.planning/ACTIVE.md`, `.planning/DECISIONS.md`, `.planning/WORK_CLAIMS.md`.
2. Run `make agent-preflight`.
3. If working in parallel, claim surfaces before edits and publish the claim immediately:
   - `make agent-claim AGENT=codex PATHS='silk/,testvectors/' NOTE='short scope note'`
   - `git add .planning/WORK_CLAIMS.md`
   - `git commit --only .planning/WORK_CLAIMS.md -m "chore(claims): codex claim <paths>"`
   - `git push`
4. In the first reply, state:
   - what will not be re-validated,
   - what focused test slice runs first,
   - when broad gate `make verify-production` will be run.
