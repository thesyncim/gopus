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

## Feature Matrix

| Extension | Support status | Probe | Notes |
| --- | --- | --- | --- |
| DNN blob loading | Supported by default | `OptionalExtensionDNNBlob` | Available through `SetDNNBlob` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`; decoder-side support currently covers loader-derived validation and retained control state, not full model-backed PLC/OSCE runtime behavior. Tagged DRED/quarantine builds may bind DRED-capable model families on this control path, but normal encode/decode runtime work remains dormant until a DRED duration, payload, or recovery path is explicitly armed; model-only public caller-buffer encode/decode paths stay zero-allocation and skip unarmed DRED helper work |
| QEXT | Supported by default | `OptionalExtensionQEXT` | Available through `SetQEXT` / `QEXT` on `Encoder` and `MultistreamEncoder` |
| DRED | Tagged control/standalone support | `OptionalExtensionDRED` | Build with `-tags gopus_dred` to support `SetDREDDuration(...)` / `DREDDuration()` on `Encoder` and `MultistreamEncoder`, plus standalone `DREDDecoder` / `DRED`; quarantine builds may expose the same controls/helpers under `gopus_unsupported_controls` for parity work without reporting DRED support; this does not claim broad DRED audio-path parity, and default builds keep DRED absent with runtime hooks dormant |
| OSCE BWE | Unsupported and quarantined | `OptionalExtensionOSCEBWE` | `SetOSCEBWE(...)` / `OSCEBWE()` are absent from the default public API surface, and low-level OSCE model helpers stay quarantine-gated |

## Supported Feature Tags

Build tag-gated DRED control/standalone support explicitly when you need the
verified DRED control and standalone surfaces:

```bash
go test -tags gopus_dred ./...
```

`SupportsOptionalExtension(gopus.OptionalExtensionDRED)` reports `true` only in
that tagged DRED build. Current release support is scoped to exposed controls,
the standalone DRED wrapper, and the selected green non-decoder-audio parity
seams. `make test-dred-tag` exercises standalone DRED wrapper lifecycle,
zero-allocation, libopus parse/decode/process metadata coverage, and real-packet
standalone process state/feature parity, standalone recovery scheduling parity,
and decoder cached recovery bookkeeping parity plus the supported-tag SILK
wideband 20/40/60 ms mono and 20 ms stereo encoder carried-payload/primary-frame
seams, the Hybrid fullband 20 ms payload-only seam, and the SILK 20 ms
primary-budget seam, while
`make test-unsupported-controls-parity` mirrors those encoder seams, the
real-model PitchDNN and RDOVAE encoder oracles, the
conceal-analysis oracle, plus bootstrap and
bookkeeping coverage. In
default builds, DRED controls are absent and
encode/decode hot paths do not enter DRED runtime hooks. The internal encoder
DRED runtime, top-level decoder DRED internals, and multistream decoder DRED
cache/runtime helpers are build-tag split, so default `./encoder`, `.`, and
`./multistream` builds use no-op stubs instead of importing the DRED/RDOVAE or
LPCNet runtime packages. Tagged DRED builds also pin the inactive encoder case:
`SetDNNBlob(...)` may retain DRED-capable model families, but `Encode` remains
zero-allocation and leaves the encoder DRED runtime dormant while
`SetDREDDuration(...)` is unset. The public caller-buffer `Encoder` and
`Decoder` paths also keep DRED model-only control state from arming the encoder
latent path, decoder payload scan, or decoder good-packet marker work. Decoder audio-path parity, Hybrid packet-length
parity, and Hybrid primary-frame byte exactness remain seam-specific and
experimental unless covered by green libopus-backed parity tests.

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
available for parity work and explicit experiments. Some OSCE control state is
retained and observable, but full model-backed OSCE BWE runtime behavior remains
incomplete.

## Release Contract

For `v0.1.0`, rely on `SupportsOptionalExtension(...)` in the current build as
the source of truth. If a control is quarantined or reports `false`, treat it as
unsupported.
