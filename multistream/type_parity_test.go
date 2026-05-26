package multistream

import (
	"reflect"
	"testing"
)

func TestStreamDecoderControlFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, name := range []string{
		"sampleRate",
		"channels",
		"decodeGainQ8",
		"complexity",
		"lastMode",
		"lastBandwidth",
		"lastFrameSize",
		"lastPacketDuration",
		"lastDataLen",
	} {
		field, ok := reflect.TypeOf(streamState{}).FieldByName(name)
		if !ok {
			t.Fatalf("streamState.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("streamState.%s type=%s want %s", name, field.Type, int32Type)
		}
	}
}

func TestMultistreamWrapperStateFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, tc := range []struct {
		owner reflect.Type
		field string
	}{
		{owner: reflect.TypeOf(Encoder{}), field: "sampleRate"},
		{owner: reflect.TypeOf(Decoder{}), field: "sampleRate"},
	} {
		field, ok := tc.owner.FieldByName(tc.field)
		if !ok {
			t.Fatalf("%s.%s missing", tc.owner.Name(), tc.field)
		}
		if field.Type != int32Type {
			t.Fatalf("%s.%s type=%s want %s", tc.owner.Name(), tc.field, field.Type, int32Type)
		}
	}
}
