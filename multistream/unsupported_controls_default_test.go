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

// childDREDNil reports whether the unexported `dred` field of the given
// *encoder.Encoder is nil. Reflection is required because `dred` is
// unexported in the encoder package and lives outside the multistream
// package's accessible surface.
func childDREDNil(v any) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	field := rv.FieldByName("dred")
	if !field.IsValid() {
		return true
	}
	return field.IsNil()
}

func TestNewEncoderLeavesDREDDormant(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	if got, want := len(enc.encoders), 2; got != want {
		t.Fatalf("expected %d child stream encoders for 3-channel fanout, got %d", want, got)
	}
	for i, child := range enc.encoders {
		if !childDREDNil(child) {
			t.Fatalf("default build allocated child DRED runtime for stream %d before SetDNNBlob", i)
		}
		if child.DNNBlobLoaded() {
			t.Fatalf("default build child stream %d unexpectedly reports DNN blob loaded before SetDNNBlob", i)
		}
	}

	// Nil-blob smoke: must not panic and must not wake any child DRED runtime.
	enc.SetDNNBlob(nil)
	if enc.DNNBlobLoaded() {
		t.Fatal("SetDNNBlob(nil) left multistream encoder reporting DNN blob loaded")
	}
	for i, child := range enc.encoders {
		if !childDREDNil(child) {
			t.Fatalf("SetDNNBlob(nil) woke child DRED runtime for stream %d", i)
		}
		if child.DNNBlobLoaded() {
			t.Fatalf("SetDNNBlob(nil) left child stream %d reporting DNN blob loaded", i)
		}
	}
}
