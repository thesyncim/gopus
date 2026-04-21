# Public Release Readiness Plan

Last updated: 2026-04-21

## Goal

Raise `gopus` from a strong pre-v1 engineering repo to a public dependency that new users can trust by default.

Current external assessment:
- Overall grade: `B`
- Current positioning: shareable as experimental/pre-v1
- Target after this plan: `A-` readiness for a first tagged public release

## Summary Of Gaps

1. Release packaging is incomplete.
- No tagged version on pkg.go.dev.
- No published GitHub release.
- Release evidence exists, but the public release flow has not been exercised.

2. The public API contract is too broad and currently blurs supported vs unsupported features.
- Unsupported optional-extension controls such as DRED and OSCE BWE appear in the public package docs.
- README guidance and pkg.go.dev surface are not aligned tightly enough.

3. Static analysis is not a meaningful gate yet.
- `.golangci.yml` currently enables only `unused`.
- CI does not run a required lint/static-analysis lane.

4. New-user documentation still reads like an engineering project more than a consumable library.
- README is strong technically, but stability boundaries and support promises need to be easier to scan.
- Maintainer/process material should keep moving out of the landing path.

5. Public project hygiene is incomplete.
- Missing `SECURITY.md`, `CONTRIBUTING.md`, and `CODE_OF_CONDUCT.md`.
- Issue templates/support policy need to be explicit.

6. Consumer-proofing is incomplete.
- The repo should prove that an external module can import and use `gopus`.
- Examples should clearly mark external-tool and network requirements.
- The most important examples should be deterministic on pkg.go.dev.

7. GitHub repository settings still need fail-closed enforcement.
- Required checks and branch protection must match the documented CI guardrails.

## Success Criteria

- `v0.1.0` can be tagged and published with release evidence attached.
- Public docs clearly separate stable core APIs from experimental or unsupported surfaces.
- CI has a required static-analysis lane in addition to the existing correctness/performance lanes.
- A new user can understand installation, support level, stable surfaces, and verification flow from the landing docs alone.
- Security, contribution, conduct, and issue/reporting paths are visible in the repo root and GitHub UI.
- At least one smoke test proves external-consumer import/build/run behavior.
- Branch protection and required checks match `docs/maintainers/CI_GUARDRAILS.md`.

## Workstreams

### 1. Release Packaging

Deliverables:
- Prepare `v0.1.0` release notes draft.
- Tighten the release checklist so it matches the actual first-release flow.
- Generate and attach release-evidence artifacts when the tag is cut.

Notes:
- Tag creation and GitHub release publication are repository actions, not just code changes.
- This track can prepare everything needed locally, but final publish still requires the actual repo release step.

### 2. Public Contract And API Fencing

Deliverables:
- Audit exported APIs that represent unsupported optional extensions.
- Either remove them from the default public surface, clearly gate them, or move them behind an explicitly experimental contract.
- Align README, package docs, and examples with the actual supported default build.

Acceptance signals:
- pkg.go.dev no longer implies support for intentionally unsupported default-build features.
- Supported core surface is easy to describe in one short section.

### 3. Static Analysis And CI Enforcement

Deliverables:
- Expand `.golangci.yml` to meaningful baseline checks.
- Add a CI lint/static-analysis job.
- Document the required-check name in `docs/maintainers/CI_GUARDRAILS.md`.

Acceptance signals:
- `make lint` becomes a useful gate locally.
- CI reports a separate lint/static-analysis status check.

### 4. Landing Docs And Support Matrix

Deliverables:
- Rewrite README around: what it is, stable core, experimental/unsupported areas, install, quick start, support matrix, verification.
- Move or reference deep validation/process material from maintainer docs instead of the landing page.
- Make supported Go versions and tested platform expectations explicit.

Acceptance signals:
- A first-time visitor can answer "is this safe to try?" and "what is supported?" in under a minute.

### 5. Public Project Hygiene

Deliverables:
- Add `SECURITY.md`.
- Add `CONTRIBUTING.md`.
- Add `CODE_OF_CONDUCT.md`.
- Add issue templates for bug reports and feature requests.
- Add a short support/triage policy if it does not fit naturally inside `CONTRIBUTING.md`.

Acceptance signals:
- Security disclosure, contribution workflow, and community expectations are visible without reading code.

### 6. Consumer-Proofing

Deliverables:
- Add an external-module smoke test that imports `github.com/thesyncim/gopus`.
- Cover basic encode/decode and Ogg read/write behavior in that smoke path.
- Label examples as offline, needs external tools, or downloads assets.
- Convert key pkg.go.dev examples to deterministic `// Output:` examples where practical.

Acceptance signals:
- A consumer can clone or `go get` the module and copy a known-good example without surprises.

### 7. GitHub Settings And Release Enforcement

Deliverables:
- Apply branch protection on `master`.
- Require the documented CI checks.
- Require branches to be up to date before merge.
- Verify the release workflow is actually used when publishing.

Notes:
- This is partly a GitHub settings task rather than a code task.
- The repo should keep the policy documented even after settings are applied.

## Parallel Scout Assignments

### Scout A: Public Contract

Owns:
- Exported API audit for unsupported optional extensions
- Package docs alignment
- Any code/test changes needed to fence unsupported controls cleanly

Likely files:
- `doc.go`
- `encoder_controls.go`
- `decoder_controls.go`
- `multistream_encoder_controls.go`
- Related tests/docs

### Scout B: Static Analysis And CI

Owns:
- `.golangci.yml`
- `Makefile` lint targets if needed
- `.github/workflows/ci.yml`
- `docs/maintainers/CI_GUARDRAILS.md`

### Scout C: Docs And Hygiene

Owns:
- `README.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `.github/ISSUE_TEMPLATE/*`

### Scout D: Consumer-Proofing

Owns:
- External import smoke test module or script
- `example_test.go`
- `container/ogg/example_test.go`
- `examples/README.md`

### Coordinator Track

Owned centrally:
- Final release-plan integration
- Release-note/checklist prep
- Any GitHub settings follow-up that cannot be changed from the worktree alone

## Verification Plan

Run focused checks per workstream while iterating:
- Public contract: targeted package tests covering the affected exported controls and docs examples
- Static analysis: `make lint`
- Consumer proofing: targeted example tests and the external-module smoke path

Run before calling the work merge-ready:
- `make verify-production`

## Open Decisions

1. Unsupported default-build extension controls:
- Preferred direction: remove or clearly quarantine them before `v0.1.0` rather than relying on "returns unsupported" alone.

2. First public release scope:
- Preferred direction: tag `v0.1.0` only after the stable-core contract is written down and reflected in pkg.go.dev output.

3. Maintainer docs layout:
- Preferred direction: continue moving maintainer/process docs under `docs/maintainers/` over time instead of keeping them in the repo root.
