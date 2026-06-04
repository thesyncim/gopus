package plc

import (
	"reflect"
	"testing"
)

func TestPLCStateFieldWidthsMatchLibopusFloatBuild(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	modeType := reflect.TypeOf(Mode(0))

	for _, name := range []string{
		"lostCount",
		"lastFrameSize",
		"lastChannels",
	} {
		field, ok := reflect.TypeOf(State{}).FieldByName(name)
		if !ok {
			t.Fatalf("State.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("State.%s type=%s want %s", name, field.Type, int32Type)
		}
	}

	field, ok := reflect.TypeOf(State{}).FieldByName("mode")
	if !ok {
		t.Fatalf("State.mode missing")
	}
	if field.Type != modeType {
		t.Fatalf("State.mode type=%s want %s", field.Type, modeType)
	}
	if modeType.Kind() != reflect.Int32 {
		t.Fatalf("Mode kind=%s want %s", modeType.Kind(), reflect.Int32)
	}
}

func TestSILKPLCStateFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	for _, name := range []string{
		"FsKHz",
		"SubfrLength",
		"NbSubfr",
		"LPCOrder",
		"ConcEnergyShift",
	} {
		field, ok := reflect.TypeOf(SILKPLCState{}).FieldByName(name)
		if !ok {
			t.Fatalf("SILKPLCState.%s missing", name)
		}
		if field.Type != int32Type {
			t.Fatalf("SILKPLCState.%s type=%s want %s", name, field.Type, int32Type)
		}
	}
}
