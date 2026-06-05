package lace

import (
	"reflect"
	"testing"
)

func TestLACEControlFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeFor[int32]()
	int32ArrayType := reflect.TypeFor[[4]int32]()
	for _, tc := range []struct {
		owner reflect.Type
		want  reflect.Type
		names []string
	}{
		{
			owner: reflect.TypeFor[FeatureControl](),
			want:  int32ArrayType,
			names: []string{"PitchL"},
		},
		{
			owner: reflect.TypeFor[FeatureControl](),
			want:  int32Type,
			names: []string{"LPCOrder", "SignalType"},
		},
		{
			owner: reflect.TypeFor[FeatureState](),
			want:  int32Type,
			names: []string{"pitchHangoverCount", "lastLag", "lastType"},
		},
	} {
		for _, name := range tc.names {
			field, ok := tc.owner.FieldByName(name)
			if !ok {
				t.Fatalf("%s.%s missing", tc.owner.Name(), name)
			}
			if field.Type != tc.want {
				t.Fatalf("%s.%s type=%s want %s", tc.owner.Name(), name, field.Type, tc.want)
			}
		}
	}
}
