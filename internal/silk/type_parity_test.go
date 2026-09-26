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

	// silk_encoder_state (silk/structs.h): opus_int fields are int32.
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int32Type,
		"inputBufIx",
		"noSpeechCounter",
		"packetSizeMs",
		"nbSubfr",
		"frameLength",
		"ecPrevSignalType",
		"frameCounter",
		"lpcOrder",
		"speechActivityQ8",
		"inputTiltQ15",
		"targetRateBps",
		"snrDBQ7",
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
		"lastNumSamples",
		"fsKHz",
		"apiFsHz",
		"prevAPIFsHz",
		"maxInternalFsHz",
		"minInternalFsHz",
		"desiredInternalFsHz",
	)
	// silk_LP_state (silk/structs.h).
	checkSILKFieldsHaveType(t, reflect.TypeFor[LPState](), int32Type,
		"TransitionFrameNo",
		"Mode",
		"SavedFsKHz",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), int16Type,
		"ecPrevLagIndex",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[Encoder](), reflect.TypeFor[[maxFrameLength + 2]int16](),
		"inputBuf",
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
	// silk_encoder (silk/float/structs_FLP.h).
	checkSILKFieldsHaveType(t, reflect.TypeFor[PacketEncoder](), int32Type,
		"nBitsUsedLBRR",
		"nBitsExceeded",
		"nChannelsAPI",
		"nChannelsInternal",
		"nPrevChannelsInternal",
		"timeSinceSwitchAllowedMs",
	)
	// silk_EncControlStruct (silk/control.h).
	checkSILKFieldsHaveType(t, reflect.TypeFor[EncControl](), int32Type,
		"NChannelsAPI",
		"NChannelsInternal",
		"APISampleRate",
		"PayloadSizeMs",
		"BitRate",
		"PacketLossPercentage",
		"Complexity",
		"MaxBits",
		"InternalSampleRate",
		"StereoWidthQ14",
		"SignalType",
		"Offset",
	)
	// stereo_enc_state (silk/structs.h).
	checkSILKFieldsHaveType(t, reflect.TypeFor[stereoEncState](), reflect.TypeFor[[maxFramesPerPacket][2][3]int8](),
		"predIx",
	)
	checkSILKFieldsHaveType(t, reflect.TypeFor[stereoEncState](), reflect.TypeFor[[maxFramesPerPacket]int8](),
		"midOnlyFlags",
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
