package gopus

import "github.com/thesyncim/gopus/internal/extsupport"

// OptionalExtension identifies a libopus build-time extension surface.
type OptionalExtension string

const (
	// OptionalExtensionDRED gates encoder-side deep redundancy control.
	OptionalExtensionDRED OptionalExtension = "dred"

	// OptionalExtensionDNNBlob gates weights-file model blob loading.
	OptionalExtensionDNNBlob OptionalExtension = "dnn_blob"

	// OptionalExtensionQEXT gates the optional extended-precision theta path.
	OptionalExtensionQEXT OptionalExtension = "qext"

	// OptionalExtensionOSCEBWE gates decoder-side OSCE bandwidth extension control.
	OptionalExtensionOSCEBWE OptionalExtension = "osce_bwe"
)

// SupportsOptionalExtension reports whether the current gopus build enables ext.
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
