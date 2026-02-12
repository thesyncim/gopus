# Codex Context

Use `AGENTS.md` as the canonical project context.
If this file and `AGENTS.md` differ, `AGENTS.md` is authoritative.

Session quick start:

1. Read: `AGENTS.md`, `.planning/ACTIVE.md`, `.planning/DECISIONS.md`, `.planning/WORK_CLAIMS.md`.
2. Run `make agent-preflight`.
3. If working in parallel, claim surfaces before edits:
   - `make agent-claim AGENT=codex PATHS='silk/,testvectors/' NOTE='short scope note'`
4. In the first reply, state:
   - what will not be re-validated,
   - what focused test slice runs first.
