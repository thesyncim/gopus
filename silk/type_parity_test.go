package silk

import (
	"reflect"
	"testing"
)

func TestSILKDecoderStateIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	int16Type := reflect.TypeOf(int16(0))
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
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderState{}), int16Type,
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderState{}), int32FlagsType,
		"VADFlags",
		"LBRRFlags",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(decoderControl{}), int32Type,
		"NumBits",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(Decoder{}), reflect.TypeOf([2]int32{}),
		"lastFrameCtrlSignal",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(Decoder{}), int32Type,
		"lpcOrder",
		"prevDecodeOnlyMiddle",
		"lastNativeMonoLen",
		"lastNativeMonoFsKHz",
		"lastNativeStereoLen",
		"lastNativeStereoFsKHz",
		"lastNativeMidLen",
		"lastNativeMidFsKHz",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(LatestDecoderControl{}), int32Type,
		"LPCOrder",
		"NbSubfr",
		"SignalType",
		"FsKHz",
		"NumBits",
	)
}

func TestSILKEncoderStateIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeOf(int32(0))
	int16Type := reflect.TypeOf(int16(0))
	int8Type := reflect.TypeOf(int8(0))
	int32FlagsType := reflect.TypeOf([maxFramesPerPacket]int32{})

	checkSILKFieldsHaveType(t, reflect.TypeOf(Encoder{}), int32Type,
		"ecPrevSignalType",
		"frameCounter",
		"lpcOrder",
		"snrDBQ7",
		"targetRateBps",
		"lastControlTargetRateBps",
		"preAdjustedTargetRateBps",
		"complexity",
		"nStatesDelayedDecision",
		"pitchEstimationComplexity",
		"pitchEstimationLPCOrder",
		"shapingLPCOrder",
		"laShape",
		"shapeWinLength",
		"warpingQ16",
		"nlsfSurvivors",
		"lbrrGainIncreases",
		"packetLossPercent",
		"nFramesEncoded",
		"nFramesPerPacket",
		"stereoCondMidFramesEncoded",
		"stereoChannelIdx",
		"stereoPrevDecodeOnlyMiddle",
		"nBitsExceeded",
		"nBitsUsedLBRR",
		"maxBits",
		"timeSinceSwitchAllowedMS",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(Encoder{}), int16Type,
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(Encoder{}), int8Type,
		"lbrrFlag",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(Encoder{}), int32FlagsType,
		"lbrrFlags",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(stereoEncState{}), int32Type,
		"prevDecodeOnlyMiddle",
	)
	checkSILKFieldsHaveType(t, reflect.TypeOf(stereoEncState{}), int32FlagsType,
		"lbrrMidOnly",
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
