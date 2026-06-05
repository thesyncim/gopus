package gopus_test

import (
	"github.com/thesyncim/gopus"
	"reflect"
	"testing"
)

func TestWrapperRuntimeStateFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeFor[int32]()

	checkWrapperFieldsHaveType(t, reflect.TypeFor[gopus.Encoder](), int32Type,
		"sampleRate",
		"channels",
		"frameSize",
	)
	checkWrapperFieldsHaveType(t, reflect.TypeFor[gopus.Decoder](), int32Type,
		"sampleRate",
		"channels",
		"lastFrameSize",
		"lastPacketDuration",
		"lastDataLen",
		"complexity",
	)
	checkWrapperFieldsHaveType(t, reflect.TypeFor[gopus.MultistreamEncoder](), int32Type,
		"sampleRate",
		"channels",
		"frameSize",
	)
	checkWrapperFieldsHaveType(t, reflect.TypeFor[gopus.MultistreamDecoder](), int32Type,
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
