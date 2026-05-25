package silk

import (
	"reflect"
	"testing"
)

func TestSILKDecoderStateIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	int32FlagsType := reflect.TypeOf([maxFramesPerPacket]int32{})

	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderState{}), int32Type,
		"nFramesDecoded",
		"nFramesPerPacket",
		"LBRRFlag",
		"fsKHz",
		"nbSubfr",
		"frameLength",
		"subfrLength",
		"ltpMemLength",
		"lpcOrder",
		"lossCnt",
		"prevSignalType",
		"ecPrevSignalType",
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderState{}), int32FlagsType,
		"VADFlags",
		"LBRRFlags",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderControl{}), int32Type,
		"NumBits",
	)
}

func checkSILKFieldsHaveType(t *testing.T, owner reflect.Type, want reflect.Type, names ...string) {
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
