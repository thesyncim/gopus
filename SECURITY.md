# Security Policy

## Supported Versions

`gopus` is still pre-v1 and does not have a tagged public release yet. Security fixes are tracked against the current `master` branch.

| Version | Supported |
| --- | --- |
| `master` | Yes |
| Tagged releases | No tagged releases yet |

## Reporting a Vulnerability

Please do not open a public issue for security reports.

Send reports to `thesyncim@gmail.com` with:

- The affected version, commit, or pseudo-version
- A short impact summary
- Reproduction steps, proof of concept, or the triggering input
- Any mitigation ideas you already confirmed

If GitHub private vulnerability reporting is enabled for this repository, you may use that instead of email.

## Response Expectations

Response times are best effort, but the target is:

- Acknowledgment within 5 business days
- Initial triage or next-step update within 10 business days
- Coordinated disclosure after a fix or mitigation is ready

## Scope

Useful reports include:

- Panics, crashes, or resource-exhaustion bugs triggered by untrusted input
- Unexpected file, process, or network exposure in examples or tooling
- Supply-chain or release-integrity issues
- Cases where documented unsupported features can still be reached in an unsafe way
