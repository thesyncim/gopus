//go:build gopus_dred || gopus_osce

package main

import (
	"fmt"

	"github.com/thesyncim/gopus"
)

func dredControlsAvailable() bool {
	return true
}

func dredBuildStatus() string {
	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionDRED) {
		return "DRED controls: gopus_dred"
	}
	return "DRED controls: extra-controls parity build"
}

func setEncoderDRED(enc *gopus.Encoder, enabled bool, duration int) error {
	if enc == nil {
		return fmt.Errorf("encoder not ready")
	}
	if !enabled {
		duration = 0
	}
	return enc.SetDREDDuration(duration)
}
