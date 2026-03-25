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
// extensions disabled, so this currently returns false for every known value.
// Unknown values also return false.
func SupportsOptionalExtension(ext OptionalExtension) bool {
	switch ext {
	case OptionalExtensionDRED,
		OptionalExtensionDNNBlob,
		OptionalExtensionQEXT,
		OptionalExtensionOSCEBWE:
		return false
	default:
		return false
	}
}
