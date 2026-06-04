package hybrid

import (
	"reflect"
	"testing"
)

func TestDecoderControlFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, name := range []string{
		"channels",
		"apiSampleRate",
	} {
		field, ok := reflect.TypeOf(Decoder{}).FieldByName(name)
		if !ok {
			t.Fatalf("Decoder.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("Decoder.%s type=%s want %s", name, field.Type, int32Type)
		}
	}
}
