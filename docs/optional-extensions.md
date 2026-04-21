# Optional Extensions

`gopus` tracks a small set of libopus build-time extension surfaces. Before `v0.1.0`, the default build is the supported public contract.

Use `SupportsOptionalExtension(...)` before relying on an extension-backed control:

```go
if gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT) {
	_ = enc.SetQEXT(true)
}
```

## Default-Build Matrix

| Extension | Default build | Probe | Notes |
| --- | --- | --- | --- |
| DNN blob loading | Supported | `OptionalExtensionDNNBlob` | Available through `SetDNNBlob` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`; decoder-side support currently covers loader-derived validation and retained control state, not full model-backed PLC/OSCE runtime behavior |
| QEXT | Supported | `OptionalExtensionQEXT` | Available through `SetQEXT` / `QEXT` on `Encoder` and `MultistreamEncoder` |
| DRED | Unsupported and quarantined | `OptionalExtensionDRED` | `SetDREDDuration(...)` / `DREDDuration()` are absent from the default public API surface, including the low-level `encoder` and `multistream` packages |
| OSCE BWE | Unsupported and quarantined | `OptionalExtensionOSCEBWE` | `SetOSCEBWE(...)` / `OSCEBWE()` are absent from the default public API surface, and low-level OSCE model helpers stay tag-gated |

## Quarantine Build Tag

The unsupported DRED and OSCE BWE wrappers are only compiled when you build with:

```bash
go test -tags gopus_unsupported_controls ./...
```

That build tag exists to make the quarantine explicit and testable. It does not change `SupportsOptionalExtension(...)`, and it does not turn DRED or OSCE BWE into supported release features.

In quarantine builds, the tag-gated wrappers and low-level helper methods are available for parity work and explicit experiments. Some control state is retained and observable, but full model-backed DRED encode/decode and OSCE BWE runtime behavior remain incomplete.

## Release Contract

For `v0.1.0`, rely on the default build plus `SupportsOptionalExtension(...)` as the source of truth. If a control is quarantined or reports `false`, treat it as unsupported.
