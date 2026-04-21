# Release Notes

Store one Markdown file per public release in this directory.

Naming:
- `docs/releases/v0.1.0.md`
- `docs/releases/v0.1.1.md`

The release workflow expects the tag name and the release-note filename to match exactly.

Each release note should cover:
- the intended stable core
- the optional extension contract, including supported default-build methods and any tag-gated or absent surfaces
- support matrix expectations
- verification evidence that accompanies the release

The GitHub `Release` workflow publishes the matching notes file and attaches the generated release-evidence archive for the tag.
