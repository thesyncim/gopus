package libopustest

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func validateDREDReferenceBuildPairing() error {
	if !fixedDREDReferenceUnsupported {
		return nil
	}
	return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf(
		"pinned libopus %s rejects FIXED_POINT with ENABLE_DRED; no matching DRED reference archive exists",
		libopustooling.DefaultVersion,
	)}
}

func resolveDREDQEXTReferenceVariantForCurrentBuild() (libopustooling.LibopusReferenceVariant, error) {
	if err := validateDREDReferenceBuildPairing(); err != nil {
		return "", err
	}
	return libopustooling.ResolveLibopusDREDQEXTReferenceVariant()
}
