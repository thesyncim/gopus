//go:build gopus_dred || gopus_extra_controls

package encoder

import (
	"reflect"
	"testing"
)

func TestDREDEncoderFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	checkFieldsHaveType(t, reflect.TypeOf(dredEmissionPlan{}), int32Type,
		"q0",
		"dQ",
		"qmax",
		"targetChunks",
		"bitrate",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderRuntime{}), int32Type,
		"latentsFill",
		"dredOffset",
		"latentOffset",
		"lastExtraDREDOffset",
		"emitted",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderPacketSnapshot{}), int32Type,
		"latentsFill",
		"dredOffset",
		"latentOffset",
		"lastExtraDREDOffset",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dredEncoderExtras{}), int32Type,
		"duration",
	)
}
