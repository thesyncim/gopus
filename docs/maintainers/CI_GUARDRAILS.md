# CI Guardrails

Required branch checks for `master`:
<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

Local merge bar: `make test-doc-contract`, `make lint`, and `make verify-production`.

Keep markdown-only changes on the same checks. Do not add workflow shortcuts that turn skipped parity, provenance, fuzz, race, or performance checks into green results.

Trust-sensitive files remain owner-reviewed through `.github/CODEOWNERS`.
