//go:build gopus_extra_controls

package extsupport

// OSCEBWERuntime reports whether OSCE BWE controls/helpers are compiled in.
// The extra-controls tag enables runtime hooks for parity without
// changing SupportsOptionalExtension.
const (
	OSCEBWERuntime = true
	OSCERuntime    = true
)
