//go:build !gopus_unsupported_controls && !gopus_dred && !gopus_qext
// +build !gopus_unsupported_controls,!gopus_dred,!gopus_qext

package multistream

import (
	"reflect"
	"testing"
)

func exportedMethodNames(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	names := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names[t.Method(i).Name] = struct{}{}
	}
	return names
}

func TestDefaultBuildQuarantinesUnsupportedControls(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}

	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "QEXT", "SetDREDDuration", "SetQEXT"} {
		if _, ok := exportedMethodNames(enc)[name]; ok {
			t.Fatalf("default build unexpectedly exposes encoder %s", name)
		}
	}
	for _, name := range []string{"DREDModelLoaded", "OSCEBWEModelLoaded", "OSCEModelsLoaded"} {
		if _, ok := exportedMethodNames(dec)[name]; ok {
			t.Fatalf("default build unexpectedly exposes decoder %s", name)
		}
	}
}

func TestNewDecoderLeavesDREDSidecarDormant(t *testing.T) {
	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	if dec.dred != nil {
		t.Fatalf("default build allocated multistream DRED sidecar: %+v", dec.dred)
	}

	dec.SetDNNBlob(nil)
	if dec.dred != nil {
		t.Fatalf("default SetDNNBlob allocated multistream DRED sidecar: %+v", dec.dred)
	}
}
