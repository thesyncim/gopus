package gopus

import "github.com/thesyncim/gopus/internal/extsupport"

// OptionalExtension identifies a recognized libopus build-time extension surface.
type OptionalExtension string

const (
	// OptionalExtensionDRED identifies encoder-side DRED controls, which remain
	// unsupported in the default build and are absent unless built with
	// -tags gopus_unsupported_controls.
	OptionalExtensionDRED OptionalExtension = "dred"

	// OptionalExtensionDNNBlob identifies supported weights-file model blob loading.
	OptionalExtensionDNNBlob OptionalExtension = "dnn_blob"

	// OptionalExtensionQEXT identifies the supported optional extended-precision theta path.
	OptionalExtensionQEXT OptionalExtension = "qext"

	// OptionalExtensionOSCEBWE identifies decoder-side OSCE BWE controls, which
	// remain unsupported in the default build and are absent unless built with
	// -tags gopus_unsupported_controls.
	OptionalExtensionOSCEBWE OptionalExtension = "osce_bwe"
)

// SupportsOptionalExtension reports whether the current build makes ext part of
// the supported default-build surface. Unsupported controls may be completely
// absent from the default public API even though the extension name is
// recognized here.
func SupportsOptionalExtension(ext OptionalExtension) bool {
	switch ext {
	case OptionalExtensionDRED:
		return extsupport.DRED
	case OptionalExtensionDNNBlob:
		return extsupport.DNNBlob
	case OptionalExtensionQEXT:
		return extsupport.QEXT
	case OptionalExtensionOSCEBWE:
		return extsupport.OSCEBWE
	default:
		return false
	}
}
