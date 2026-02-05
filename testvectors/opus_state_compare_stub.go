//go:build !cgo_libopus

package testvectors

type libopusOpusStateSnapshot struct {
	SignalType           int
	LagIndex             int
	ContourIndex         int
	PrevLag              int
	PrevSignalType       int
	LTPCorr              float32
	FirstFrameAfterReset int

	NSQLagPrev       int
	NSQSLTPBufIdx    int
	NSQSLTPShpBufIdx int
	NSQPrevGainQ16   int32
	NSQRandSeed      int32
	NSQRewhiteFlag   int

	ECPrevLagIndex    int
	ECPrevSignalType  int
	SilkModeSignal    int
	SilkInternalHz    int
	SilkPayloadSizeMs int
	SilkModeUseCBR    int
	SilkModeMaxBits   int
	SilkModeBitRate   int
	NFramesPerPacket  int
	NFramesEncoded    int
	SpeechActivityQ8  int
	InputTiltQ15      int
	PitchEstThresQ16  int32
	NStatesDelayedDec int
	WarpingQ16        int
	SumLogGainQ7      int32
	TargetRateBps     int
	SNRDBQ7           int
	NBitsExceeded     int
	GainIndices       [4]int8
	LastGainIndex     int
	NSQXQHash         uint64
	NSQSLTPShpHash    uint64
	NSQSLPCHash       uint64
	NSQSAR2Hash       uint64
	PitchXBufHash     uint64
	PitchBufLen       int
	PitchWinHash      uint64
	PitchWinLen       int
}

func captureLibopusOpusSilkState(_ []float32, _, _, _, _, _ int) (libopusOpusStateSnapshot, bool) {
	return libopusOpusStateSnapshot{}, false
}

func captureLibopusOpusSilkStateBeforeFrame(_ []float32, _, _, _, _, _ int) (libopusOpusStateSnapshot, bool) {
	return libopusOpusStateSnapshot{}, false
}

func captureLibopusOpusPitchXBufBeforeFrame(_ []float32, _, _, _, _, _ int) ([]float32, bool) {
	return nil, false
}
