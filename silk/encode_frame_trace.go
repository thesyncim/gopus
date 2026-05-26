package silk

type encodeFrameTraceStage uint8

const (
	encodeFrameTraceAfterIndices encodeFrameTraceStage = iota + 1
	encodeFrameTraceAfterPulses
)

type encodeFrameIndexTracePoint uint8

const (
	encodeFrameIndexTraceAfterType encodeFrameIndexTracePoint = iota
	encodeFrameIndexTraceAfterGains
	encodeFrameIndexTraceAfterNLSF
	encodeFrameIndexTraceAfterPitch
	encodeFrameIndexTraceAfterLTP
	encodeFrameIndexTraceAfterSeed
	encodeFrameIndexTracePointCount
)

type encodeFrameRangeTrace struct {
	tell int
	rng  uint32
}

type encodeFrameTrace struct {
	stage              encodeFrameTraceStage
	iter               int
	tell               int
	rng                uint32
	indexTrace         [encodeFrameIndexTracePointCount]encodeFrameRangeTrace
	indices            sideInfoIndices
	predGainBits       int32
	pitchAutoCorr0Bits int32
	pitchResNrgBits    int32
	gainsPreQ16        [maxNbSubfr]int32
	resNrgBits         [maxNbSubfr]int32
	gainsUnqQ16        [maxNbSubfr]int32
	gainsQuantQ16      [maxNbSubfr]int32
	pulses             []int8
}
