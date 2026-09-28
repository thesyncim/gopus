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
