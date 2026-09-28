package libopustest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// PublicAPIReferenceIdentity records the archive selected for the current
// public Go build. DNN archives use their feature stamp and feature fields;
// other archives use the standard libopustooling variant stamp.
type PublicAPIReferenceIdentity struct {
	ArchivePath string
	BuildDir    string
	SourceDir   string
	Variant     libopustooling.LibopusReferenceVariant
	DNN         bool
	DRED        bool
	OSCE        bool
	QEXT        bool
	Custom      bool
}

// ResolvePublicAPIReferenceIdentity selects the same archive as
// BuildPublicAPIHelper, ensures DNN archives exist, and validates the selected
// archive's source, feature, and instruction identity before returning it.
func ResolvePublicAPIReferenceIdentity() (PublicAPIReferenceIdentity, error) {
	cfg, dnn, err := currentPublicAPIHelperConfig(CHelperConfig{})
	if err != nil {
		return PublicAPIReferenceIdentity{}, err
	}
	if dnn {
		variant, err := libopustooling.ResolveLibopusReferenceVariant()
		if err != nil {
			return PublicAPIReferenceIdentity{}, err
		}
		if dredQEXTReferenceEnabled && !customModesReferenceEnabled {
			dredVariant, err := libopustooling.ResolveLibopusDREDQEXTReferenceVariant()
			if err != nil {
				return PublicAPIReferenceIdentity{}, err
			}
			archive := DREDQEXTRefPath(".libs", "libopus.a")
			identity := PublicAPIReferenceIdentity{
				ArchivePath: archive,
				BuildDir:    filepath.Dir(filepath.Dir(archive)),
				Variant:     dredVariant,
				DRED:        true,
				QEXT:        true,
			}
			return identity, identity.Validate()
		}
		buildConfig := featureDNNBuildConfig(extsupport.DRED, osceDNNFeatureEnabled, extsupport.QEXT, customModesReferenceEnabled, variant)
		ensure := EnsureDREDBuild
		if osceDNNFeatureEnabled {
			ensure = EnsureOSCEBuild
		}
		sourceDir, buildDir, err := ensure(repoRoot())
		if err != nil {
			return PublicAPIReferenceIdentity{}, err
		}
		identity := PublicAPIReferenceIdentity{
			ArchivePath: filepath.Join(buildDir, ".libs", "libopus.a"),
			BuildDir:    buildDir,
			SourceDir:   sourceDir,
			Variant:     variant,
			DNN:         true,
			DRED:        buildConfig.dred,
			OSCE:        buildConfig.osce,
			QEXT:        buildConfig.qext,
			Custom:      buildConfig.custom,
		}
		return identity, identity.Validate()
	}
	if len(cfg.Libs) == 0 {
		return PublicAPIReferenceIdentity{}, &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("public helper resolver did not select a libopus archive")}
	}
	variant, err := publicAPIReferenceVariant(cfg)
	if err != nil {
		return PublicAPIReferenceIdentity{}, err
	}
	archive := cfg.Libs[0]
	identity := PublicAPIReferenceIdentity{
		ArchivePath: archive,
		BuildDir:    filepath.Dir(filepath.Dir(archive)),
		Variant:     variant,
		QEXT:        extsupport.QEXT,
		Custom:      customModesReferenceEnabled,
	}
	return identity, identity.Validate()
}

// Validate checks the selected archive's stamped source, feature, and ISA
// identity before a caller uses it as parity evidence.
func (identity PublicAPIReferenceIdentity) Validate() error {
	if identity.ArchivePath == "" || identity.BuildDir == "" {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("public reference identity lacks an archive path or build directory")}
	}
	if _, err := os.Stat(identity.ArchivePath); err != nil {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("selected public libopus archive is unavailable: %w", err)}
	}
	if !identity.DNN {
		if identity.Variant == "" {
			return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("selected public archive lacks a libopus variant")}
		}
		return libopustooling.ValidateLibopusReferenceBuild(identity.BuildDir, identity.Variant, libopustooling.DefaultVersion)
	}
	if identity.Variant == "" {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("selected DNN archive lacks an instruction variant")}
	}
	if err := validateDNNSource(identity.SourceDir); err != nil {
		return err
	}
	if identity.DRED {
		if err := libopustooling.ValidateDREDModelSources(identity.SourceDir); err != nil {
			return err
		}
	}
	config := featureDNNBuildConfig(identity.DRED, identity.OSCE, identity.QEXT, identity.Custom, identity.Variant)
	if !config.buildCurrent(identity.BuildDir) {
		return &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("selected DNN archive has a stale or missing feature build stamp")}
	}
	return validateDREDInstructionBuild(identity.BuildDir, config)
}

func publicAPIReferenceVariant(cfg CHelperConfig) (libopustooling.LibopusReferenceVariant, error) {
	switch {
	case cfg.CustomFixedQEXTRef:
		return libopustooling.ResolveLibopusCustomFixedQEXTReferenceVariant()
	case cfg.CustomQEXTRef:
		return libopustooling.ResolveLibopusCustomQEXTReferenceVariant()
	case cfg.CustomFixedRef:
		return libopustooling.ResolveLibopusCustomFixedReferenceVariant()
	case cfg.FixedQEXTRef:
		return libopustooling.ResolveLibopusFixedQEXTReferenceVariant()
	case cfg.FixedRef:
		return libopustooling.ResolveLibopusFixedReferenceVariant()
	case cfg.QEXTRef:
		return libopustooling.ResolveLibopusQEXTReferenceVariant()
	case cfg.CustomRef:
		variant, err := libopustooling.ResolveLibopusReferenceVariant()
		if err != nil {
			return "", err
		}
		if variant == libopustooling.LibopusReferenceSIMD {
			return libopustooling.LibopusReferenceCustomSIMD, nil
		}
		return libopustooling.LibopusReferenceCustomScalar, nil
	default:
		return libopustooling.ResolveLibopusReferenceVariant()
	}
}
