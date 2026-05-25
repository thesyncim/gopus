package lace

import (
	"reflect"
	"testing"
)

func TestLACEControlFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	int32ArrayType := reflect.TypeOf([SubframesPerFrame]int32{})
	for _, tc := range []struct {
		owner reflect.Type
		want  reflect.Type
		names []string
	}{
		{
			owner: reflect.TypeOf(FeatureControl{}),
			want:  int32ArrayType,
			names: []string{"PitchL"},
		},
		{
			owner: reflect.TypeOf(FeatureControl{}),
			want:  int32Type,
			names: []string{"SignalType"},
		},
		{
			owner: reflect.TypeOf(FeatureState{}),
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
