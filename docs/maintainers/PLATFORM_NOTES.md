# Platform Notes

Last updated: 2026-05-17

Host-specific issues that affect local maintainer workflows but not CI. CI on
Linux remains the authoritative oracle for correctness; this file documents
workarounds for cases where the local toolchain is silently blocked.

## macOS Local Test Binary Quarantine

### Symptom

Freshly built local Go test binaries can be rejected by macOS Gatekeeper /
`syspolicyd` after `go test -c` (or an in-place `go test` run that materializes
a temporary binary), with no useful Go-side error. The signal looks like:

- `spctl --assess --type execute <binary>` reports the binary as rejected
  (typically `source=Unnotarized Developer ID` or `source=No matching signature`).
- `log show --predicate 'subsystem == "com.apple.syspolicy"' --last 5m` (or
  `Console.app` filtered on `syspolicyd`) shows repeated
  `Unable to initialize qtn_proc: 3` entries each time the test binary launches.
- The test process stalls at `_dyld_start` (visible via `sample <pid>` or a
  paused debugger attach) and never reaches `main`, so `go test` looks "hung"
  with zero progress and no panic, deadlock, or stderr output.

When this happens, the binary on disk is fine; the host policy daemon is
refusing to let dyld initialize it. Re-running `go test` will rebuild a new
binary and hit the same wall.

### Workarounds

These are ordered from least invasive to most invasive. Prefer the first option
that unblocks you; do not disable Gatekeeper globally.

1. Strip the quarantine extended attribute on the offending binary (and on the
   `go-build` cache directory it came from, if recurring):

   ```sh
   xattr -d com.apple.quarantine <binary>
   # Recurring case: clear quarantine attrs on the active go-build cache.
   xattr -dr com.apple.quarantine "$(go env GOCACHE)"
   ```

   Caveat: only addresses binaries that have already been tagged. New builds
   can pick the attribute back up depending on how the toolchain was installed
   (e.g. via a quarantined `.pkg` or downloaded `tar.gz`). Safe for local
   workflows; never run `xattr -dr` on a path you did not produce.

2. Ad-hoc codesign the test binary so `syspolicyd` has a stable identity to
   evaluate:

   ```sh
   go test -c -o ./bin/foo.test ./...
   codesign -s - --force --preserve-metadata=entitlements,requirements ./bin/foo.test
   ./bin/foo.test -test.v
   ```

   Caveat: ad-hoc signatures (`-s -`) are not trusted by Gatekeeper for
   distribution. This is fine for local-only test binaries but must not be
   shipped or used as a substitute for real signing in release artifacts.

3. Whitelist a specific local build path via `spctl`:

   ```sh
   sudo spctl --add --label "gopus-local-test" <binary-or-directory>
   # Undo later with:
   sudo spctl --remove --label "gopus-local-test"
   ```

   Caveat: requires `sudo`, mutates the system policy database, and only helps
   for binaries at the exact added path. Do not `spctl --master-disable`;
   turning Gatekeeper off globally affects every downloaded binary on the
   machine and is a real security regression. Remove the label when finished.

### Reliable Oracle

Until the host-policy issue is cleared, treat CI (see
[CI_GUARDRAILS.md](CI_GUARDRAILS.md)) as the authoritative runtime oracle for
parity, race, and tagged-build coverage. Local macOS results that "hang at
start" with the symptoms above should not be reported as test failures; rerun
in CI or apply one of the workarounds above and re-verify.
