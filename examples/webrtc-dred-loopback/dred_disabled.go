//go:build !gopus_dred && !gopus_extra_controls

package main

import (
	"fmt"

	"github.com/thesyncim/gopus"
)

func dredControlsAvailable() bool {
	return false
}

func dredBuildStatus() string {
	return "DRED controls: rebuild with -tags gopus_dred"
}

func setEncoderDRED(_ *gopus.Encoder, enabled bool, _ int) error {
	if enabled {
		return fmt.Errorf("DRED controls are not compiled in")
	}
	return nil
}
