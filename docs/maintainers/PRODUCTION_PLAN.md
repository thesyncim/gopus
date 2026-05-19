# Production Plan

Goal: ship `gopus` as a dependable pure-Go Opus codec with libopus parity, zero-allocation hot paths, and stable release evidence.

Merge bar:
- `make test-doc-contract`
- `make lint`
- `make verify-production`
- `make release-evidence` before tags

Current baseline: core SILK/CELT/Hybrid decode and encode gates are green; DRED and QEXT remain tag-gated; OSCE controls stay quarantine-only.

Keep future docs short. Put details in tests, release evidence, or focused issue notes instead of long standing plans.
