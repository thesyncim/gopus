//go:build gopus_dred && !gopus_unsupported_controls
// +build gopus_dred,!gopus_unsupported_controls

package multistream

import (
	"reflect"
	"testing"
)

func dredBuildTagMethodNames(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	names := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names[t.Method(i).Name] = struct{}{}
	}
	return names
}

func TestDREDBuildTagExposesEncoderControlsOnly(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "SetDREDDuration"} {
		if _, ok := dredBuildTagMethodNames(enc)[name]; !ok {
			t.Fatalf("gopus_dred build does not expose encoder %s", name)
		}
	}

	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	if _, ok := dredBuildTagMethodNames(dec)["DREDModelLoaded"]; !ok {
		t.Fatal("gopus_dred build does not expose decoder DREDModelLoaded")
	}
	for _, name := range []string{"OSCEBWEModelLoaded", "OSCEModelsLoaded", "OSCEBWE", "SetOSCEBWE"} {
		if _, ok := dredBuildTagMethodNames(dec)[name]; ok {
			t.Fatalf("gopus_dred build unexpectedly exposes decoder %s", name)
		}
	}
}
