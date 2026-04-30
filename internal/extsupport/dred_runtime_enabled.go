//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package extsupport

// DREDRuntime reports whether DRED runtime hooks are compiled in. The
// unsupported-controls tag enables runtime hooks for parity without changing
// SupportsOptionalExtension.
const DREDRuntime = true
