//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package extsupport

// DREDRuntime reports whether DRED runtime hooks are compiled in. The
// extra-controls tag enables runtime hooks for parity without changing
// SupportsOptionalExtension.
const DREDRuntime = true
