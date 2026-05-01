# Release Notes

Store one Markdown file per public release in this directory.

Naming:
- `docs/releases/v0.1.0.md`
- `docs/releases/v0.1.1.md`

The release workflow expects the tag name and the release-note filename to match exactly.
No release exists until the tag and matching GitHub Release are both published.

Each release note should cover:
- the intended stable core
- the optional extension contract, including supported default-build methods and any tag-gated or absent surfaces
- support matrix expectations
- verification evidence that accompanies the release, including commit SHA, Go version, OS/platform, pinned libopus version/SHA256, command pass/fail summaries, benchmark guardrail result, fuzz/safety summary, parity summary, and consumer-smoke result

The GitHub `Release` workflow publishes the matching notes file and attaches the generated release-evidence summary plus archive for the tag.
