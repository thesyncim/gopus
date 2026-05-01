# Security Policy

## Supported Versions

`gopus` is pre-v1. No tagged public release exists until a version tag and
matching GitHub Release are both published.

| Version | Security support |
| --- | --- |
| `master` | Supported for private reports and fixes |
| `v0.1.x` | Supported after `v0.1.0` is tagged and published |
| Other branches, forks, or unreviewed snapshots | Not supported |

## Private Reporting

Do not open a public issue for a suspected vulnerability.

Prefer GitHub private vulnerability reporting for this repository if it is
enabled. If that is unavailable, email `thesyncim@gmail.com`.

Include:

- The affected commit, pseudo-version, or release tag.
- Go version, OS, architecture, and build tags.
- A short impact summary.
- Reproduction steps, proof of concept, packet/file input, or crash log.
- Any mitigation you already confirmed.

## Do Not File Publicly

Use private reporting for:

- Panics, crashes, memory exhaustion, CPU exhaustion, or unbounded allocation
  triggered by untrusted packets, Ogg pages, or examples.
- Input that causes unsafe behavior in decode, encode, container parsing, or
  optional-extension control paths.
- Supply-chain, release-integrity, CI-token, provenance, or dependency-update
  concerns.
- Cases where an explicitly unsupported or quarantine-gated feature is reachable
  in a way that can affect ordinary users.
- Any report that includes a weaponized sample or exploit instructions.

Public issues are appropriate for ordinary correctness bugs, documentation gaps,
or unsupported-feature requests that do not expose users to security risk.

## Response Windows

Targets are best effort:

- Acknowledgment within 5 business days.
- Initial triage or next-step update within 10 business days.
- Status update at least every 14 calendar days while a report remains open.

## Disclosure And Fix Process

1. Confirm receipt privately and assign an initial severity.
2. Reproduce the issue against the reported version and current `master`.
3. Prepare a minimal fix with focused regression coverage.
4. Run the relevant security, safety, parity, and release evidence gates.
5. Coordinate disclosure timing with the reporter when possible.
6. Publish a GitHub Security Advisory for confirmed vulnerabilities.
7. Ship a patched release when a public release line is affected; before
   `v0.1.0`, publish the fix on `master` and keep the no-release status visible.

Release notes for security fixes should identify the affected versions, fixed
version, impact summary, and verification evidence without publishing exploit
details before users have had a reasonable update window.
