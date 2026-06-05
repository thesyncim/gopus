//go:build gopus_osce && !gopus_dred && !gopus_qext

package gopus_test

import (
	"fmt"

	"github.com/thesyncim/gopus"
)

// The extra-controls build enables USE_WEIGHTS_FILE model loading (DNN blob)
// alongside the DRED/OSCE runtime hooks, but DRED is not part of the supported
// release surface, so SupportsOptionalExtension(DRED) stays false.
func ExampleSupportsOptionalExtension() {
	fmt.Printf("dnn_blob: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob))
	fmt.Printf("dred: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDRED))
	fmt.Printf("osce_bwe: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionOSCEBWE))
	fmt.Printf("qext: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT))
	// Output:
	// dnn_blob: true
	// dred: false
	// osce_bwe: false
	// qext: false
}
