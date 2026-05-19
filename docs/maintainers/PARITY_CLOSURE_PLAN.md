# Parity Closure

Reference: `tmp_check/opus-1.6.1/`.

Rules:
- Prefer libopus behavior when behavior is uncertain.
- Add fixture-backed tests before claiming support.
- Keep unsupported or experimental controls tag-gated.
- Preserve zero allocations on real-time encode/decode paths.

Current focus:
- Broaden packet/mode coverage only when backed by libopus fixtures.
- Keep DRED/QEXT support claims tied to their required gates.
- Remove stale diagnostics once equivalent assertions exist.
