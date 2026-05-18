# Optional Extensions

`gopus` tracks a small set of libopus build-time extension surfaces. Optional
features have separate support and parity gates: a feature tag makes a surface
supported, while `gopus_unsupported_controls` compiles quarantine surfaces for
parity work without reporting feature support.

Use `SupportsOptionalExtension(...)` before relying on an extension-backed control:

```go
if gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT) {
	_ = enc.SetQEXT(true)
}
```

The default `SetDNNBlob(...)` control surface is also a parity-gated optional
extension surface: `make test-dnn-blob-parity` builds the pinned libopus
USE_WEIGHTS_FILE model-blob helpers, checks the top-level and multistream
encoder/decoder controls against those blobs, and fails if required helper
coverage is skipped.

## Feature Matrix

| Extension | Support status | Probe | Notes |
| --- | --- | --- | --- |
| DNN blob loading | Supported by default | `OptionalExtensionDNNBlob` | Available through `SetDNNBlob` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`; `make test-dnn-blob-parity` validates the default control surface against libopus USE_WEIGHTS_FILE model blobs and fails on skipped helper coverage; decoder-side default support covers loader-derived validation and retained control state, while optional model-backed audio runtimes stay tag-gated/quarantined. Tagged DRED/quarantine builds may bind DRED-capable model families on this control path; encoder runtime work remains dormant until a DRED duration is armed, core-only decoder blobs keep DRED payload scanning dormant, and combined core+DRED decoder blobs arm the DRED parser so cached recovery can consume packet extensions on green seams |
| QEXT | Tagged support | `OptionalExtensionQEXT` | Build with `-tags gopus_qext` to support `SetQEXT` / `QEXT` on `Encoder` and `MultistreamEncoder`; default builds keep those controls absent and compile the packet-extension payload scan/encode plumbing behind a constant false gate |
| DRED | Tagged control/standalone support | `OptionalExtensionDRED` | Build with `-tags gopus_dred` to support `SetDREDDuration(...)` / `DREDDuration()` on `Encoder` and `MultistreamEncoder`, plus standalone `DREDDecoder` / `DRED`; quarantine builds may expose the same controls/helpers under `gopus_unsupported_controls` for parity work without reporting DRED support; this does not claim broad DRED audio-path parity beyond the required mono explicit/live decoder matrix, selected 16 kHz Hybrid mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, the 48 kHz SILK WB explicit stereo first-loss seam, and the single-coupled multistream CELT/Hybrid/SILK neural DRED consumer seams; broader SILK stereo matrices and support-surface graduation remain seam-specific, and default builds keep DRED absent with runtime hooks dormant |
| OSCE BWE | Unsupported and quarantined | `OptionalExtensionOSCEBWE` | `SetOSCEBWE(...)` / `OSCEBWE()` are absent from the default public API surface, and low-level OSCE model helpers stay quarantine-gated |

## Supported Feature Tags

Build tag-gated QEXT support explicitly when you need the libopus
ENABLE_QEXT-compatible extended-precision theta path:

```bash
go test -tags gopus_qext ./...
```

`SupportsOptionalExtension(gopus.OptionalExtensionQEXT)` reports `true` only in
that tagged QEXT build. Current release support is scoped to encoder controls,
packet extension carriage, and decoder-side QEXT parity covered by
`make test-qext-parity`, which uses a separate pinned
`tmp_check/opus-1.6.1-qext` reference build configured with `--enable-qext`
and fails on skipped libopus-helper coverage.
Default builds keep QEXT controls absent and do not scan, arm, or encode QEXT
packet extensions in the public encode/decode hot paths.

Build tag-gated DRED control/standalone support explicitly when you need the
verified DRED control and standalone surfaces:

```bash
go test -tags gopus_dred ./...
```

`SupportsOptionalExtension(gopus.OptionalExtensionDRED)` reports `true` only in
that tagged DRED build. Current release support is scoped to exposed controls,
the standalone DRED wrapper, and the selected green parity seams.
`make test-dred-tag` exercises standalone DRED wrapper lifecycle,
zero-allocation, libopus parse/decode/process metadata coverage, and real-packet
standalone process state/feature parity, standalone recovery scheduling parity,
and decoder cached recovery bookkeeping parity plus the supported-tag SILK
wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload/packet-envelope
seams, Hybrid fullband 20/40 ms mono and stereo carried-payload/packet-envelope
seams, and the single-coupled multistream SILK/CELT/Hybrid 20 ms stereo DRED
carrier fan-out seams.
`make test-unsupported-controls-tag` pins the quarantine
API exposure, standalone/control smoke, cached DRED recovery bookkeeping, and
dormant-runtime checks without changing support probes.
`make test-unsupported-controls-parity` mirrors the supported encoder seams and
adds parser availability, internal converter/payload/basic-analysis coverage,
real-model PitchDNN and RDOVAE encoder oracles, the conceal-analysis oracle,
OSCE BWE/LACE numerical forward-pass, raw-signal/int8/crossfade/PLC-continuity,
and runtime integration coverage, 48 kHz bootstrap coverage, the required mono
decoder explicit/live numerical matrix, selected 16 kHz Hybrid mono
live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and
next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit
first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, the
48 kHz SILK WB explicit stereo first-loss seam, and single-coupled
multistream CELT/Hybrid/SILK neural DRED consumer seams.
Required DRED parity gates fail on skipped libopus-helper tests instead
of treating missing helpers as green. In
default builds, DRED controls are absent and
encode/decode hot paths do not enter DRED runtime hooks. The internal encoder
DRED runtime, top-level decoder DRED internals, and multistream decoder DRED
cache/runtime helpers are build-tag split, so default `./encoder`, `.`, and
`./multistream` builds use no-op stubs instead of importing the DRED/RDOVAE or
LPCNet runtime packages. Tagged DRED builds also pin the inactive encoder case:
`SetDNNBlob(...)` may retain DRED-capable model families, but `Encode` remains
zero-allocation and leaves the encoder DRED runtime dormant while
`SetDREDDuration(...)` is unset. The public caller-buffer `Encoder` path keeps
DRED model-only control state from arming the encoder latent path, and
core-only decoder blobs keep decoder payload scanning and good-packet marker
work dormant. Single-stream and multistream decoders may arm the RDOVAE parser
from a combined core+DRED decoder blob and consume cached DRED on green seams,
including the single-coupled CELT/Hybrid/SILK multistream seams. The
required mono decoder explicit/live numerical matrix, selected 16 kHz Hybrid
mono live-sequence seams, CELT/Hybrid stereo cached/live first/second-loss and
next-packet handoff matrices, selected 16 kHz CELT/Hybrid stereo explicit
first-loss probes, explicit first-loss and recovery lifecycle/cursor seams, the
48 kHz SILK WB explicit stereo first-loss seam, and focused multistream
Hybrid/SILK DRED consumers are parity-gated in quarantine.
Hybrid 20/40 ms mono/stereo packet-envelope
exactness is required in both DRED parity gates; Hybrid/SILK primary-frame byte
exactness remains outside the supported gate unless a seam is explicitly named
in the byte-exact test matrix.
Broader SILK stereo packet/mode matrices, broader multistream packet/mode
coverage, and support-surface graduation remain seam-specific and unsupported
unless covered by green libopus-backed parity tests.

Supported feature tags can be combined when both surfaces are needed. A
`-tags "gopus_dred gopus_qext"` build reports both DRED and QEXT support and
exposes both control families. A `-tags "gopus_unsupported_controls gopus_qext"`
build reports QEXT support, exposes the quarantine DRED/OSCE controls for
parity work, and still reports DRED and OSCE BWE as unsupported.

## Quarantine Build Tag

Experimental wrappers and parity hooks can still be compiled with:

```bash
go test -tags gopus_unsupported_controls ./...
```

That build tag exists to make quarantine work explicit and testable. It may
expose DRED controls/standalone helpers and OSCE controls for libopus-backed
parity sweeps, but it does not, by itself, change `SupportsOptionalExtension(...)`.
DRED release support comes from `gopus_dred`, and OSCE BWE remains unsupported.

In quarantine builds, tag-gated wrappers and low-level helper methods are
available for parity work and explicit experiments. OSCE BWE and LACE/NoLACE
control state is retained, model-bound, and exercised by numerical
libopus-backed forward-pass comparators plus decoder runtime smoke gates; it
still does not report public support from `SupportsOptionalExtension(...)`.

## Release Contract

For `v0.1.0`, rely on `SupportsOptionalExtension(...)` in the current build as
the source of truth. If a control is quarantined or reports `false`, treat it as
unsupported.
