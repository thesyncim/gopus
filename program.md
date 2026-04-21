# gopus Workflow

Keep the workflow small and judge-driven.

## Work Order

1. Read `README.md` and `AGENTS.md`.
2. Pick one priority:
   - parity with libopus in quality and features
   - performance
   - maintainability
   - documentation
   - dead-test cleanup
3. Make one idea-sized change at a time.
4. Run the narrowest relevant checks first.
5. Run `make verify-production` before merge-ready changes.

## Default Checks

- Parity/quality work: `make test-quality`
- Performance work: focused benchmarks plus `make bench-guard`
- Safety/parser/container work: the relevant fuzz or soak checks
- Release confidence: `make verify-production`

## Guardrails

- Compare behavior against pinned libopus before inventing heuristics.
- Preserve zero-allocation hot paths.
- Do not change `testvectors/testdata/` or `tmp_check/` unless the task is explicitly about refreshing those references.
- Prefer small, reviewable slices over broad rewrites.
