# Production TODO

Last updated: 2026-02-12

## Completed in this change

- [x] Eliminate per-frame FFT scratch allocation in encoder analysis path.
- [x] Add zero-allocation hot-path guard tests for encode/decode float32 and int16 APIs.
- [x] Add production verification make targets (`test-race`, `test-fuzz-smoke`, `verify-production`, `verify-production-exhaustive`).
- [x] Wire Linux CI to run `make verify-production`.
- [x] Document production plan and verification workflow.

## Next high-impact items

- [ ] Close strict quality gap (`Q >= 0`) in remaining SILK/Hybrid/CELT profiles.
- [ ] Add profile-by-profile quality ratchet baselines and prevent backward movement.
- [ ] Add scheduled CI `verify-production-exhaustive` run (nightly or weekly).
- [ ] Investigate and reduce parity-tier `-race` runtime in `testvectors` (currently needs elevated timeout).
- [ ] Add release checklist template requiring:
  - [ ] compliance summary output,
  - [ ] SILK trace parity output,
  - [ ] race test output,
  - [ ] hot-path alloc guard output.
- [ ] Add stability/perf tracking artifact per release (`bench + alloc + compliance diff`).

## Optional stretch goals

- [ ] Provide integration load test harness (long-running encode/decode soak with packet loss simulation).
- [ ] Add architecture-specific performance dashboards (arm64 vs amd64).
