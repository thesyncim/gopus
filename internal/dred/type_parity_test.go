package dred

import (
	"reflect"
	"testing"
)

func TestDREDIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeFor[int32]()
	for _, tc := range []struct {
		owner reflect.Type
		names []string
	}{
		{
			owner: reflect.TypeFor[Header](),
			names: []string{
				"Q0",
				"DQ",
				"QMax",
				"ExtraOffset",
				"DredOffset",
				"DredFrameOffset",
			},
		},
		{
			owner: reflect.TypeFor[EncoderBuffer](),
			names: []string{
				"inputBufferFill",
				"dredOffset",
				"latentOffset",
			},
		},
	} {
		for _, name := range tc.names {
			field, ok := tc.owner.FieldByName(name)
			if !ok {
				t.Fatalf("%s.%s missing", tc.owner.Name(), name)
			}
			if field.Type != int32Type {
				t.Fatalf("%s.%s type=%s want %s", tc.owner.Name(), name, field.Type, int32Type)
			}
		}
	}
}
