//go:build gopus_dred
// +build gopus_dred

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
	// dnn_blob: true
	// dred: true
	// osce_bwe: false
	// qext: true
}
