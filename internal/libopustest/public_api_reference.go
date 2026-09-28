package libopustest

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// BuildPublicAPIHelper builds an oracle helper against the public codec
// configuration selected by the current Go build, including its optional
// feature and instruction lane.
//
// The caller supplies ordinary helper sources, flags, and non-archive link
// inputs. Reference selectors and libopus archives are selected here so a
// public API comparison cannot silently pair QEXT or fixed-point Go code with
// a different C build.
func BuildPublicAPIHelper(cfg CHelperConfig) (string, error) {
	cfg, dnn, err := currentPublicAPIHelperConfig(cfg)
	if err != nil {
		return "", err
	}
	if dnn {
		return BuildDNNCHelper(repoRoot(), cfg)
	}
	return BuildCHelper(cfg)
}

func currentPublicAPIHelperConfig(cfg CHelperConfig) (CHelperConfig, bool, error) {
	if cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef || cfg.DREDQEXTRef || cfg.CustomRef || cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef || cfg.SIMDRef || cfg.ForceScalarRef {
		return CHelperConfig{}, false, &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("public API helper cannot override the current Go reference configuration")}
	}
	linkInputs := make([]string, 0, len(cfg.Libs)+len(cfg.LDFlags)+len(cfg.CFlags))
	linkInputs = append(linkInputs, cfg.Libs...)
	linkInputs = append(linkInputs, cfg.LDFlags...)
	linkInputs = append(linkInputs, cfg.CFlags...)
	if err := validateNoLibopusLibraryOverride(linkInputs); err != nil {
		return CHelperConfig{}, false, err
	}

	if extsupport.DREDRuntime || osceDNNFeatureEnabled {
		if decodeSequenceFixedRef {
			return CHelperConfig{}, false, &libopustooling.LibopusReferenceConfigError{Err: fmt.Errorf("public API helper cannot pair fixed-point and DNN feature references")}
		}
		return cfg, true, nil
	}

	var archive string
	switch {
	case customModesReferenceEnabled && decodeSequenceFixedRef && extsupport.QEXT:
		cfg.CustomFixedQEXTRef = true
		archive = CustomFixedQEXTRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled && decodeSequenceFixedRef:
		cfg.CustomFixedRef = true
		archive = CustomFixedRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled && extsupport.QEXT:
		cfg.CustomQEXTRef = true
		archive = CustomQEXTRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled:
		cfg.CustomRef = true
		archive = CustomRefPath(".libs", "libopus.a")
	case decodeSequenceFixedRef && extsupport.QEXT:
		cfg.FixedQEXTRef = true
		archive = FixedQEXTRefPath(".libs", "libopus.a")
	case decodeSequenceFixedRef:
		cfg.FixedRef = true
		archive = FixedRefPath(".libs", "libopus.a")
	case extsupport.QEXT:
		cfg.QEXTRef = true
		archive = QEXTRefPath(".libs", "libopus.a")
	default:
		archive = RefPath(".libs", "libopus.a")
	}

	cfg.Libs = append([]string{archive}, cfg.Libs...)
	if len(cfg.Libs) == 1 {
		cfg.Libs = append(cfg.Libs, "-lm")
	}
	return cfg, false, nil
}
