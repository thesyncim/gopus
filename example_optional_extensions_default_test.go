//go:build !gopus_dred && !gopus_qext && !gopus_extra_controls

package gopus_test

import (
	"fmt"

	"github.com/thesyncim/gopus"
)

func ExampleSupportsOptionalExtension() {
	fmt.Printf("dnn_blob: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob))
	fmt.Printf("dred: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDRED))
	fmt.Printf("osce_bwe: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionOSCEBWE))
	fmt.Printf("qext: %v\n", gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT))
	// Output:
	// dnn_blob: false
	// dred: false
	// osce_bwe: false
	// qext: false
}
