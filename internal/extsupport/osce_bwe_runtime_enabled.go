//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package extsupport

// OSCEBWERuntime reports whether OSCE BWE controls/helpers are compiled in.
// The unsupported-controls tag enables runtime hooks for parity without
// changing SupportsOptionalExtension.
const OSCEBWERuntime = true
