package encoder

import (
	"reflect"
	"testing"
)

func TestOpusInputScratchFieldWidthsMatchFloatBuild(t *testing.T) {
	opusResSliceType := reflect.TypeFor[[]opusRes]()
	float32SliceType := reflect.TypeFor[[]float32]()
	int32Type := reflect.TypeFor[int32]()

	checkFieldsHaveType(t, reflect.TypeFor[Encoder](), opusResSliceType,
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
	checkFieldsHaveType(t, reflect.TypeFor[Encoder](), float32SliceType,
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
	checkFieldsHaveType(t, reflect.TypeFor[HybridState](), opusResSliceType,
		"scratchTransitionPCM",
	)
	checkFieldsHaveType(t, reflect.TypeFor[HybridState](), float32SliceType,
		"scratchLookahead32",
		"scratchSilkLookahead",
		"scratchLaLeft",
		"scratchLaRight",
		"scratchLaOutLeft",
		"scratchLaOutRight",
	)
	checkFieldsHaveType(t, reflect.TypeFor[Encoder](), int32Type,
		"sampleRate",
		"channels",
		"frameSize",
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
		"silkResamplerRate",
	)
	checkFieldsHaveType(t, reflect.TypeFor[dtxState](), int32Type,
		"noActivityMsQ1",
		"frameDurationMs",
	)
	checkFieldsHaveType(t, reflect.TypeFor[fecState](), int32Type,
		"frameCount",
	)
	checkFieldsHaveType(t, reflect.TypeFor[TonalityAnalysisState](), int32Type,
		"LSBDepth",
		"MemFill",
		"PrevBandwidth",
		"ECount",
		"Count",
		"AnalysisOffset",
		"WritePos",
		"ReadPos",
		"ReadSubframe",
	)
	checkFieldsHaveType(t, reflect.TypeFor[AnalysisInfo](), int32Type,
		"BandwidthIndex",
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
