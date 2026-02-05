//go:build !cgo_libopus

package testvectors

type libopusProcessGainsSnapshot struct {
	GainsIndices     []int8
	GainsUnqQ16      []int32
	QuantOffsetType  int
	LastGainIndexOut int8
	Lambda           float32
}

type libopusProcessGainsFrameSnapshot struct {
	EncodeFrame            int
	CallsInFrame           int
	CondCoding             int
	SignalType             int
	QuantOffsetBefore      int
	QuantOffsetAfter       int
	NumSubframes           int
	SubframeLength         int
	NStatesDelayedDecision int
	InputTiltQ15           int
	SNRDBQ7                int
	SpeechActivityQ8       int
	LTPPredCodGain         float32
	Lambda                 float32
	LastGainIndexPrev      int
	LastGainIndexOut       int
	GainsIndices           [4]int8
	GainsUnqQ16            [4]int32
	GainsBefore            [4]float32
	GainsAfter             [4]float32
	ResNrgBefore           [4]float32
}

func libopusProcessGainsFromTraceInputs(
	_ []float32,
	_ []float32,
	_ int,
	_ int,
	_ int,
	_ int32,
	_ int,
	_ int,
	_ int,
	_ int,
	_ int8,
	_ bool,
) (libopusProcessGainsSnapshot, bool) {
	return libopusProcessGainsSnapshot{}, false
}

func captureLibopusProcessGainsAtFrame(_ []float32, _, _, _, _, _ int) (libopusProcessGainsFrameSnapshot, bool) {
	return libopusProcessGainsFrameSnapshot{}, false
}
