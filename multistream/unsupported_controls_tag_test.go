//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package multistream

import (
	"reflect"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

func exportedMethodNames(v any) map[string]struct{} {
	t := reflect.TypeOf(v)
	names := make(map[string]struct{}, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names[t.Method(i).Name] = struct{}{}
	}
	return names
}

func TestUnsupportedControlsBuildExposesQuarantinedControls(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}

	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "SetDREDDuration"} {
		if _, ok := exportedMethodNames(enc)[name]; !ok {
			t.Fatalf("unsupported-controls build does not expose encoder %s", name)
		}
	}
	for _, name := range []string{"DREDModelLoaded", "OSCEBWEModelLoaded", "OSCEModelsLoaded"} {
		if _, ok := exportedMethodNames(dec)[name]; !ok {
			t.Fatalf("unsupported-controls build does not expose decoder %s", name)
		}
	}
}

func TestEncoderDREDDuration(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration(4) error: %v", err)
	}
	if got := enc.DREDDuration(); got != 4 {
		t.Fatalf("DREDDuration()=%d want 4", got)
	}

	for i, streamEnc := range enc.encoders {
		if got := streamEnc.DREDDuration(); got != 4 {
			t.Fatalf("stream %d DREDDuration()=%d want 4", i, got)
		}
	}

	if err := enc.SetDREDDuration(internaldred.MaxFrames + 1); err != encpkg.ErrInvalidDREDDuration {
		t.Fatalf("SetDREDDuration(max+1) error=%v want=%v", err, encpkg.ErrInvalidDREDDuration)
	}

	enc.Reset()
	if got := enc.DREDDuration(); got != 0 {
		t.Fatalf("DREDDuration() after Reset=%d want 0", got)
	}
}
