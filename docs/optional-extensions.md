# Optional Extensions

`gopus` tracks a small set of libopus build-time extension surfaces. Optional
features have separate support and parity gates: a feature tag makes a surface
supported, while `gopus_unsupported_controls` only compiles quarantine helpers
for parity work.

Use `SupportsOptionalExtension(...)` before relying on an extension-backed control:

```go
if gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT) {
	_ = enc.SetQEXT(true)
}
```

## Feature Matrix

| Extension | Support status | Probe | Notes |
| --- | --- | --- | --- |
| DNN blob loading | Supported by default | `OptionalExtensionDNNBlob` | Available through `SetDNNBlob` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`; decoder-side support currently covers loader-derived validation and retained control state, not full model-backed PLC/OSCE runtime behavior |
| QEXT | Supported by default | `OptionalExtensionQEXT` | Available through `SetQEXT` / `QEXT` on `Encoder` and `MultistreamEncoder` |
| DRED | Tagged control/standalone support | `OptionalExtensionDRED` | Build with `-tags gopus_dred` to expose `SetDREDDuration(...)` / `DREDDuration()` on `Encoder` and `MultistreamEncoder`, plus standalone `DREDDecoder` / `DRED`; this does not claim broad DRED audio-path parity, and default builds keep DRED absent with runtime hooks dormant |
| OSCE BWE | Unsupported and quarantined | `OptionalExtensionOSCEBWE` | `SetOSCEBWE(...)` / `OSCEBWE()` are absent from the default public API surface, and low-level OSCE model helpers stay quarantine-gated |

## Supported Feature Tags

Build DRED support explicitly when you need the verified DRED control and
standalone surfaces:

```bash
go test -tags gopus_dred ./...
```

`SupportsOptionalExtension(gopus.OptionalExtensionDRED)` reports `true` only in
that tagged DRED build. Current release support is scoped to exposed controls,
the standalone DRED wrapper, and the selected green non-decoder-audio parity
seams. In default builds, DRED controls are absent and encode/decode hot paths
do not enter DRED runtime hooks.

## Quarantine Build Tag

Experimental wrappers and parity hooks can still be compiled with:

```bash
go test -tags gopus_unsupported_controls ./...
```

That build tag exists to make quarantine work explicit and testable. It does not, by itself, change `SupportsOptionalExtension(...)`, and it does not turn OSCE BWE into a supported release feature. DRED parity helpers may also compile there for libopus-backed parity sweeps; release support comes from `gopus_dred`.

In quarantine builds, tag-gated wrappers and low-level helper methods are
available for parity work and explicit experiments. Some OSCE control state is
retained and observable, but full model-backed OSCE BWE runtime behavior remains
incomplete.

## Release Contract

For `v0.1.0`, rely on `SupportsOptionalExtension(...)` in the current build as
the source of truth. If a control is quarantined or reports `false`, treat it as
unsupported.
