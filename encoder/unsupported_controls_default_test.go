//go:build !gopus_unsupported_controls
// +build !gopus_unsupported_controls

package encoder_test

import (
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/encoder"
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
	methods := exportedMethodNames(encoder.NewEncoder(48000, 1))
	for _, name := range []string{"DREDDuration", "DREDModelLoaded", "DREDReady", "SetDREDDuration"} {
		if _, ok := methods[name]; ok {
			t.Fatalf("default build unexpectedly exposes %s", name)
		}
	}
}
