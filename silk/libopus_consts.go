package silk

const (
	maxNbSubfr                   = 4
	subFrameLengthMs             = 5
	ltpMemLengthMs               = 20
	maxFsKHz                     = 16
	maxSubFrameLength            = subFrameLengthMs * maxFsKHz
	maxFrameLength               = maxSubFrameLength * maxNbSubfr
	maxLPCOrder                  = 16
	minLPCOrder                  = 10
	ltpOrder                     = 5
	shellCodecFrameLength        = 16
	log2ShellCodecFrameLength    = 4
	nRateLevels                  = 10
	silkMaxPulses                = 16
	maxFramesPerPacket           = 3
	maxLPCStabilizeIterations    = 16
	maxPredictionPowerGainInvQ30 = 107374

	nLevelsQGain        = 64
	maxDeltaGainQuant   = 36
	minDeltaGainQuant   = -4
	minQGainDb          = 2
	maxQGainDb          = 88
	quantLevelAdjustQ10 = 80

	typeNoVoiceActivity = 0
	typeUnvoiced        = 1
	typeVoiced          = 2

	codeIndependently             = 0
	codeIndependentlyNoLtpScaling = 1
	codeConditionally             = 2

	nlsfQuantMaxAmplitude = 4
	bweAfterLossQ16       = 63570
	nlsfQuantLevelAdjQ10  = 102
	lsfCosTabSizeFix      = 128
	stereoQuantTabSize    = 16
	stereoQuantSubSteps   = 5
	stereoInterpLenMs     = 8
)

const (
	peMaxNbSubfr        = 4
	peMinLagMs          = 2
	peMaxLagMs          = 18
	peNbCbksStage2Ext   = 11
	peNbCbksStage2_10ms = 3
	peNbCbksStage3Max   = 34
	peNbCbksStage3_10ms = 12
)

var silk_Quantization_Offsets_Q10 = [][]int16{
	{100, 240},
	{32, 100},
}
