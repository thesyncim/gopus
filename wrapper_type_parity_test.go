package gopus

import (
	"reflect"
	"testing"
)

func TestWrapperRuntimeStateFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))

	checkWrapperFieldsHaveType(t, reflect.TypeOf(Encoder{}), int32Type,
		"sampleRate",
		"channels",
		"frameSize",
	)
	checkWrapperFieldsHaveType(t, reflect.TypeOf(MultistreamEncoder{}), int32Type,
		"sampleRate",
		"channels",
		"frameSize",
	)
	checkWrapperFieldsHaveType(t, reflect.TypeOf(MultistreamDecoder{}), int32Type,
		"sampleRate",
		"channels",
		"lastFrameSize",
	)
}

func checkWrapperFieldsHaveType(t *testing.T, owner reflect.Type, want reflect.Type, names ...string) {
	t.Helper()
	for _, name := range names {
		field, ok := owner.FieldByName(name)
		if !ok {
			t.Fatalf("%s.%s missing", owner.Name(), name)
		}
		if field.Type != want {
			t.Fatalf("%s.%s type=%s want %s", owner.Name(), name, field.Type, want)
		}
	}
}
