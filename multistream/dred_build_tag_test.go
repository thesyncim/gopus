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

func TestDREDBuildTagExposesEncoderControlsOnlyReadyRequiresEveryStream(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	if len(enc.encoders) < 2 {
		t.Fatalf("test requires multiple stream encoders, got %d", len(enc.encoders))
	}
	blob := makeLoadableDREDEncoderTestBlob(t)
	enc.SetDNNBlob(blob)
	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	if !enc.DREDModelLoaded() || !enc.DREDReady() {
		t.Fatal("multistream encoder did not report ready after propagating model and duration")
	}

	enc.encoders[1].SetDNNBlob(nil)
	if enc.DREDModelLoaded() {
		t.Fatal("DREDModelLoaded()=true with one stream missing a DRED model")
	}
	if enc.DREDReady() {
		t.Fatal("DREDReady()=true with one stream missing a DRED model")
	}

	enc.encoders[1].SetDNNBlob(blob)
	if err := enc.encoders[1].SetDREDDuration(0); err != nil {
		t.Fatalf("child SetDREDDuration(0) error: %v", err)
	}
	if !enc.DREDModelLoaded() {
		t.Fatal("DREDModelLoaded()=false after restoring every stream model")
	}
	if enc.DREDReady() {
		t.Fatal("DREDReady()=true with one stream duration cleared")
	}
}
