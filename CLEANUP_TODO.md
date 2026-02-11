# Repo Cleanup TODO

Last updated: 2026-02-11

## Completed
- [x] Remove redundant/diagnostic test suites and temporary debug tests (49 files removed).
- [x] Remove additional low-value diagnostics in `testvectors/`:
  - `decoder_packet_profile_test.go`
  - `diag_test.go`
  - `nb10ms_nsq_diag_test.go`
- [x] Replace root stereo diagnostic test with assertion-driven regression:
  - removed `stereo_diag_test.go`
  - added `stereo_roundtrip_regression_test.go`
- [x] Re-home stereo benchmark from diagnostic naming:
  - removed `stereo_profile_test.go`
  - added `benchmark_stereo_test.go`
- [x] Replace SILK NB/WB diag suite with focused resampler/decode regressions:
  - removed `silk/nb10ms_diag_test.go`
  - added `silk/nb10ms_resampler_regression_test.go`
- [x] Reduce total test-file count from `255` to `210`.
- [x] Replace lost branch coverage with focused regression tests:
  - `silk/coverage_regression_test.go`
  - `silk/lpc_helpers_regression_test.go`
  - `decoder_test.go` (`TestNewDecoder_DefaultMaxPacketLimits`)
- [x] Preserve coverage baseline after cleanup (`63.3%`).

## In Progress
- [x] Remove temporary command package:
  - `cmd/tmp_analysis/main.go` deleted.
- [x] Remove tracked runtime artifact:
  - `mem.out` deleted.
- [x] Clear root-level generated artifacts (`*.test`, `*.prof`, `*.out`, `*.o`).
- [x] Add cleanup commands to `Makefile`:
  - `make clean` for root-generated artifacts.
  - `make clean-vectors` for downloaded `opus_testvectors` cache.
- [x] Tighten `.gitignore` test-vector cache rule:
  - from `testvectors/testdata/` to `testvectors/testdata/opus_testvectors/`.

## Remaining Cleanup Candidates
- [ ] Optional: decide whether to keep `testvectors/libopus_trace_test.go` always-on or gate under an env/tier flag due runtime cost.
- [ ] Optional: decide whether to keep `testvectors/silk_trace_fixture_test.go` always-on or gate under targeted runs.
- [ ] Confirm whether `CLAUDE.md` and `CODEX.md` should remain as lightweight pointers or be consolidated into a single agent-context shim.

## Notes
- `tmp_check/opus-1.6.1/` is intentionally retained (required reference baseline).
- Large fixture JSON files under `testvectors/testdata/` are intentionally tracked.

## Commit Chunk Plan
1. `chore(repo): clean artifacts and maintenance tooling`
   - `.gitignore`
   - `Makefile`
   - `cmd/tmp_analysis/main.go`
   - `mem.out`
   - `CLEANUP_TODO.md`

2. `test(cleanup): remove low-value diagnostic suites`
   - all deleted diagnostic/trace/debug tests in `celt/`, `encoder/`, `silk/`, root, and `testvectors/`

3. `test(regression): add focused replacements and benchmark re-home`
   - `decoder_test.go`
   - `silk/coverage_regression_test.go`
   - `silk/lpc_helpers_regression_test.go`
   - `silk/nb10ms_resampler_regression_test.go`
   - `stereo_roundtrip_regression_test.go`
   - `benchmark_stereo_test.go`
   - `testvectors/mdct_helpers_test.go`
