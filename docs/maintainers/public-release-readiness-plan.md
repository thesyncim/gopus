# Public Release Readiness

Release only when:
- `make verify-production` passes.
- `make release-evidence` produces a PASS summary.
- Release notes exist for the tag.
- Branch protection requires the checks in `CI_GUARDRAILS.md`.
- Supported extension claims match `docs/optional-extensions.md`.

Do not publish a tag from an unverified tree.
