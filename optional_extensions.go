package gopus

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
//
// The default pure-Go build intentionally leaves these optional libopus
// extensions disabled except for DNN blob loading. Unknown values return false.
func SupportsOptionalExtension(ext OptionalExtension) bool {
	switch ext {
	case OptionalExtensionDNNBlob:
		return true
	case OptionalExtensionDRED,
		OptionalExtensionQEXT,
		OptionalExtensionOSCEBWE:
		return false
	default:
		return false
	}
}
