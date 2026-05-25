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
