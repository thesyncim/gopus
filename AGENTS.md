# Agent Instructions

## Libopus Byte Parity Rule

This repository targets libopus 1.6.1 byte parity. The active goal is exact packet payload and final-range alignment with the pinned libopus fixtures, not broad type churn.

- Prioritize byte-identical encoder/decoder behavior: packet bytes, final range, range-coder event order, allocation decisions, and fixture provenance.
- Do not change quality thresholds, fixture baselines, allowlists, or ratchets to hide byte drift. Fix the root codec decision, state, or math ordering issue first.
- When changing codec/runtime behavior, compare against libopus C sources and existing oracle helpers. Cite the libopus file/function in code comments or test names when the fix depends on a subtle ordering rule.
- Scratch/type changes are allowed only when they directly explain a byte or final-range mismatch. Do not spend agent time on type-parity cleanup as a standalone objective.
- Before finishing a byte-parity change, run the narrow failing fixture or oracle test first, then the relevant package test. For encoder fixtures, prefer `GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run '<focused fixture>' -count=1 -v`.
- If a mismatch remains, document the first divergent case, frame, packet byte, final range, and suspected codec stage in the commit or report so the next agent can continue from evidence.

Treat fixture/baseline edits as review-visible evidence, not a shortcut.
