package silk

import (
	"reflect"
	"testing"
)

func TestSILKDecoderStateIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeFor[int32]()
	int16Type := reflect.TypeFor[int16]()
	int32FlagsType := reflect.TypeFor[[3]int32]()

	checkSILKFieldsHaveType(t, reflect.TypeFor[decoderState](), int32Type,
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
	checkSILKFieldsHaveType(t, reflect.TypeFor[decoderState](), int16Type,
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[decoderState](), int32FlagsType,
		"VADFlags",
		"LBRRFlags",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[decoderControl](), int32Type,
		"NumBits",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Decoder](), reflect.TypeFor[[2]int32](),
		"lastFrameCtrlSignal",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Decoder](), int32Type,
		"lpcOrder",
		"prevDecodeOnlyMiddle",
		"lastNativeMonoLen",
		"lastNativeMonoFsKHz",
		"lastNativeStereoLen",
		"lastNativeStereoFsKHz",
		"lastNativeMidLen",
		"lastNativeMidFsKHz",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[LatestDecoderControl](), int32Type,
		"LPCOrder",
		"NbSubfr",
		"SignalType",
		"FsKHz",
		"NumBits",
	)
}

func TestSILKEncoderStateIntegerFieldWidthsMatchLibopus(t *testing.T) {
	int32Type := reflect.TypeFor[int32]()
	int16Type := reflect.TypeFor[int16]()
	int8Type := reflect.TypeFor[int8]()
	int32FlagsType := reflect.TypeFor[[3]int32]()
	int32FramesType := reflect.TypeFor[[3]int32]()

	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int32Type,
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
		"lastNumSamples",
		"sampleRate",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int16Type,
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int8Type,
		"lbrrFlag",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int32FlagsType,
		"lbrrFlags",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int32FramesType,
		"lbrrFrameLength",
		"lbrrNbSubfr",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[stereoEncState](), int32Type,
		"prevDecodeOnlyMiddle",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[stereoEncState](), int32FlagsType,
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
