package libopustest

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestCurrentPublicAPIHelperConfigPairsReference(t *testing.T) {
	cfg, dnn, err := currentPublicAPIHelperConfig(CHelperConfig{
		Label:      "test public helper",
		OutputBase: "test_public_helper",
		SourceFile: "test_public_helper.c",
		Libs:       []string{"-lm", "-ldl"},
	})
	if err != nil {
		if decodeSequenceFixedRef && (extsupport.DREDRuntime || osceDNNFeatureEnabled) {
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("unsupported feature combination error=%T %v, want LibopusReferenceConfigError", err, err)
			}
			return
		}
		t.Fatal(err)
	}
	if dnn != (extsupport.DREDRuntime || osceDNNFeatureEnabled) {
		t.Fatalf("DNN helper=%t for DRED=%t OSCE=%t", dnn, extsupport.DREDRuntime, osceDNNFeatureEnabled)
	}
	if dnn {
		if cfg.CustomRef || cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef {
			t.Fatalf("DNN helper has a custom-only selector: %+v", cfg)
		}
		if len(cfg.Libs) != 2 || cfg.Libs[0] != "-lm" || cfg.Libs[1] != "-ldl" {
			t.Fatalf("DNN helper link inputs=%v, want caller inputs preserved", cfg.Libs)
		}
		return
	}

	var wantArchive string
	switch {
	case customModesReferenceEnabled && decodeSequenceFixedRef && extsupport.QEXT:
		if !cfg.CustomFixedQEXTRef || cfg.CustomRef || cfg.CustomQEXTRef || cfg.CustomFixedRef {
			t.Fatalf("custom fixed-QEXT selectors=%+v", cfg)
		}
		wantArchive = CustomFixedQEXTRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled && decodeSequenceFixedRef:
		if !cfg.CustomFixedRef || cfg.CustomRef || cfg.CustomQEXTRef || cfg.CustomFixedQEXTRef {
			t.Fatalf("custom fixed selectors=%+v", cfg)
		}
		wantArchive = CustomFixedRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled && extsupport.QEXT:
		if !cfg.CustomQEXTRef || cfg.CustomRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef {
			t.Fatalf("custom QEXT selectors=%+v", cfg)
		}
		wantArchive = CustomQEXTRefPath(".libs", "libopus.a")
	case customModesReferenceEnabled:
		if !cfg.CustomRef || cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef || cfg.CustomQEXTRef || cfg.CustomFixedRef || cfg.CustomFixedQEXTRef {
			t.Fatalf("custom reference selectors custom=%t fixed=%t qext=%t fixed-qext=%t", cfg.CustomRef, cfg.FixedRef, cfg.QEXTRef, cfg.FixedQEXTRef)
		}
		wantArchive = CustomRefPath(".libs", "libopus.a")
	case decodeSequenceFixedRef && extsupport.QEXT:
		if !cfg.FixedQEXTRef || cfg.QEXTRef || cfg.FixedRef {
			t.Fatalf("reference selectors fixed=%t qext=%t fixed-qext=%t", cfg.FixedRef, cfg.QEXTRef, cfg.FixedQEXTRef)
		}
		wantArchive = FixedQEXTRefPath(".libs", "libopus.a")
	case decodeSequenceFixedRef:
		if !cfg.FixedRef || cfg.QEXTRef || cfg.FixedQEXTRef {
			t.Fatalf("reference selectors fixed=%t qext=%t fixed-qext=%t", cfg.FixedRef, cfg.QEXTRef, cfg.FixedQEXTRef)
		}
		wantArchive = FixedRefPath(".libs", "libopus.a")
	case extsupport.QEXT:
		if !cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef {
			t.Fatalf("reference selectors fixed=%t qext=%t fixed-qext=%t", cfg.FixedRef, cfg.QEXTRef, cfg.FixedQEXTRef)
		}
		wantArchive = QEXTRefPath(".libs", "libopus.a")
	default:
		if cfg.QEXTRef || cfg.FixedRef || cfg.FixedQEXTRef {
			t.Fatalf("unexpected reference selectors fixed=%t qext=%t fixed-qext=%t", cfg.FixedRef, cfg.QEXTRef, cfg.FixedQEXTRef)
		}
		wantArchive = RefPath(".libs", "libopus.a")
	}
	wantLibs := []string{wantArchive, "-lm", "-ldl"}
	if !reflect.DeepEqual(cfg.Libs, wantLibs) {
		t.Fatalf("helper libraries=%v want %v", cfg.Libs, wantLibs)
	}
	if got := filepath.Base(cfg.Libs[0]); got != "libopus.a" {
		t.Fatalf("selected archive basename=%q", got)
	}
}

func TestCurrentPublicAPIHelperConfigRejectsReferenceOverrides(t *testing.T) {
	tests := []CHelperConfig{
		{QEXTRef: true},
		{FixedRef: true},
		{FixedQEXTRef: true},
		{DREDQEXTRef: true},
		{CustomRef: true},
		{CustomQEXTRef: true},
		{CustomFixedRef: true},
		{CustomFixedQEXTRef: true},
		{SIMDRef: true},
		{ForceScalarRef: true},
		{Libs: []string{QEXTRefPath(".libs", "libopus.a")}},
		{Libs: []string{"-lopus"}},
		{LDFlags: []string{"-Wl,-l,opus"}},
		{CFlags: []string{"-l", "opus"}},
	}
	for i, cfg := range tests {
		if _, _, err := currentPublicAPIHelperConfig(cfg); err == nil {
			t.Errorf("case %d accepted caller-selected reference", i)
		} else {
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Errorf("case %d returned %T, want LibopusReferenceConfigError", i, err)
			}
		}
	}
}

func TestValidatePublicAPIAMD64TargetFeatures(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		fixed   bool
		qext    bool
		custom  bool
		dnn     bool
		wantErr bool
	}{
		{name: "untargeted feature reference", fixed: true, qext: true, custom: true, dnn: true},
		{name: "targeted default float core", target: "v2"},
		{name: "targeted fixed point", target: "v2", fixed: true, wantErr: true},
		{name: "targeted QEXT", target: "v2", qext: true, wantErr: true},
		{name: "targeted custom modes", target: "v2", custom: true, wantErr: true},
		{name: "targeted DNN", target: "v2", dnn: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePublicAPIAMD64TargetFeatures(tc.target, tc.fixed, tc.qext, tc.custom, tc.dnn)
			if tc.wantErr {
				var configErr *libopustooling.LibopusReferenceConfigError
				if !errors.As(err, &configErr) {
					t.Fatalf("error=%T %v, want LibopusReferenceConfigError", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildCHelperRejectsCustomRefWithAMD64Target(t *testing.T) {
	cfg := CHelperConfig{Label: "target custom helper", OutputBase: "target_custom", SourceFile: "unused.c", CustomRef: true}
	err := validateCHelperAMD64TargetReference("v2", cfg, libopustooling.LibopusReferenceScalar)
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("direct CustomRef selection error=%T %v, want LibopusReferenceConfigError", err, err)
	}

	t.Setenv(libopustooling.LibopusAMD64TargetEnv, "v2")
	_, err = BuildCHelper(cfg)
	if !errors.As(err, &configErr) {
		t.Fatalf("BuildCHelper error=%T %v, want LibopusReferenceConfigError", err, err)
	}
}
