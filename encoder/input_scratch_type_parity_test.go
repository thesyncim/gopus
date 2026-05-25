package encoder

import (
	"reflect"
	"testing"
)

func TestOpusInputScratchFieldWidthsMatchFloatBuild(t *testing.T) {
	opusResSliceType := reflect.TypeOf([]opusRes(nil))
	float32SliceType := reflect.TypeOf([]float32(nil))
	int32Type := reflect.TypeOf(int32(0))

	checkFieldsHaveType(t, reflect.TypeOf(Encoder{}), opusResSliceType,
		"inputBuffer",
		"delayBuffer",
		"scratchDCPCM",
		"scratchInputPCM",
		"scratchDelayedPCM",
		"scratchDelayState",
		"scratchTransitionPrefill",
		"scratchSilkPrefill",
		"scratchCELTPrefill",
		"scratchQuantPCM",
	)
	checkFieldsHaveType(t, reflect.TypeOf(Encoder{}), float32SliceType,
		"scratchPCM32",
		"scratchLeft",
		"scratchRight",
		"scratchMono",
		"silkResampled",
		"silkResampledR",
		"silkResampledBuffer",
		"scratchSilkAligned",
		"floatInputFrame",
	)
	checkFieldsHaveType(t, reflect.TypeOf(HybridState{}), opusResSliceType,
		"scratchTransitionPCM",
	)
	checkFieldsHaveType(t, reflect.TypeOf(HybridState{}), float32SliceType,
		"scratchLookahead32",
		"scratchSilkLookahead",
		"scratchLaLeft",
		"scratchLaRight",
		"scratchLaOutLeft",
		"scratchLaOutRight",
	)
	checkFieldsHaveType(t, reflect.TypeOf(Encoder{}), int32Type,
		"bitrate",
		"packetLoss",
		"complexity",
		"forceChannels",
		"lsbDepth",
		"voiceRatio",
		"streamChannels",
		"prevChannels",
		"toMono",
		"fecConfig",
	)
	checkFieldsHaveType(t, reflect.TypeOf(dtxState{}), int32Type,
		"noActivityMsQ1",
		"frameDurationMs",
	)
	checkFieldsHaveType(t, reflect.TypeOf(fecState{}), int32Type,
		"frameCount",
	)
}

func checkFieldsHaveType(t *testing.T, owner reflect.Type, want reflect.Type, names ...string) {
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
