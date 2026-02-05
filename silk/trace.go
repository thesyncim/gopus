package silk

import "math"

// EncoderTrace captures intermediate values for parity debugging.
// When non-nil, the encoder will populate it during EncodeFrame.
type EncoderTrace struct {
	Pitch    *PitchTrace
	NLSF     *NLSFTrace
	LTP      *LTPTrace
	GainLoop *GainLoopTrace
	NSQ      *NSQTrace
	FramePre *FrameStateTrace
	Frame    *FrameStateTrace
}

// PitchTrace captures pitch residual and search inputs.
type PitchTrace struct {
	CaptureResidual  bool
	CapturePitchLags bool
	CaptureXBuf      bool

	XBufHash   uint64
	XBufLen    int
	XBuf       []float32 // Captured input buffer (scaled) when CaptureXBuf is true
	XFrameHash uint64
	XFrameLen  int

	ResidualHash uint64
	ResidualLen  int
	Residual     []float32

	PitchLags []int
	LagIndex  int
	Contour   int
	LTPCorr   float32

	ResStart     int
	FrameSamples int
	BufLen       int
	LtpMemLen    int
	LaPitch      int
	PitchWinLen  int
	NbSubfr      int
	SubfrLen     int
	FsKHz        int

	SearchThres1         float64
	Thrhld               float64
	ThrhldClamped        float64
	PitchEstThresholdQ16 int32
	PrevLag              int
	PrevSignal           int
	SignalType           int
	SpeechQ8             int
	InputTiltQ15         int
	PredGain             float64
	LPCOrder             int
	Complexity           int
	FirstFrameAfterReset bool
}

// NLSFTrace captures NLSF analysis and quantization inputs/outputs.
type NLSFTrace struct {
	CaptureLTPRes bool

	LTPResHash uint64
	LTPResLen  int
	LTPRes     []float32

	MinInvGain        float64
	LPCOrder          int
	NbSubfr           int
	SubfrLen          int
	SubfrLenWithOrder int
	UseInterp         bool
	InterpIdx         int

	RawNLSFQ15       []int16
	PrevNLSFQ15      []int16
	Stage1Idx        int
	Residuals        []int
	QuantizedNLSFQ15 []int16

	SignalType           int
	SpeechQ8             int
	Bandwidth            Bandwidth
	NLSFSurvivors        int
	FirstFrameAfterReset bool
}

// LTPTrace captures LTP analysis and quantization inputs/outputs.
type LTPTrace struct {
	CaptureResidual bool
	CaptureXX       bool

	ResidualHash uint64
	ResidualLen  int
	Residual     []float32
	ResStart     int

	NbSubfr  int
	SubfrLen int

	PitchLags []int

	SumLogGainQ7In  int32
	SumLogGainQ7Out int32

	PERIndex   int
	PredGainQ7 int32
	LTPIndex   []int8
	BQ14       []int16

	XXHash uint64
	XXLen  int
	XX     []float32

	XxHash uint64
	XxLen  int
	Xx     []float32
}

// GainLoopTrace captures per-iteration gain search loop state.
type GainLoopTrace struct {
	Iterations []GainLoopIter

	SeedIn                 int
	SeedOut                int
	UsedDelayedDecision    bool
	WarpingQ16             int
	NStatesDelayedDecision int
	MaxBits                int
	UseCBR                 bool
	ConditionalCoding      bool
	NumSubframes           int
	LastGainIndexPrev      int8
	GainsUnqQ16            [maxNbSubfr]int32
	SignalType             int
	SpeechActivityQ8       int
	InputTiltQ15           int
	SNRDBQ7                int
	PredGainQ7             int32
	SubframeSamples        int
	QuantOffsetBefore      int
	QuantOffsetAfter       int
	GainsBefore            [maxNbSubfr]float32
	ResNrgBefore           [maxNbSubfr]float32
	GainsAfter             [maxNbSubfr]float32
}

// GainLoopIter captures one iteration of the gain/bit-budget search loop.
type GainLoopIter struct {
	Iter              int
	GainMultQ8        int16
	GainsID           int32
	QuantOffset       int
	Bits              int
	BitsBeforeIndices int
	BitsAfterIndices  int
	BitsAfterPulses   int
	FoundLower        bool
	FoundUpper        bool
	SkippedNSQ        bool
	SeedIn            int
	SeedAfterNSQ      int
	SeedOut           int
}

// FrameStateTrace captures final per-frame encoder state after successful encode.
type FrameStateTrace struct {
	SignalType       int
	LagIndex         int
	Contour          int
	GainIndices      [maxNbSubfr]int8
	PitchL           [maxNbSubfr]int
	LastGainIndex    int32
	SumLogGainQ7     int32
	InputRateBps     int
	TargetRateBps    int
	SNRDBQ7          int
	NBitsExceeded    int
	NFramesPerPacket int
	NFramesEncoded   int

	PrevLag        int
	PrevSignalType int
	LTPCorr        float32

	SpeechActivityQ8       int
	InputTiltQ15           int
	PitchEstThresholdQ16   int32
	NStatesDelayedDecision int
	WarpingQ16             int

	FirstFrameAfterReset bool
	ECPrevLagIndex       int
	ECPrevSignalType     int

	NSQLagPrev       int
	NSQSLTPBufIdx    int
	NSQSLTPShpBufIdx int
	NSQPrevGainQ16   int32
	NSQRandSeed      int32
	NSQRewhiteFlag   int
	NSQXQHash        uint64
	NSQSLTPShpHash   uint64
	NSQSLPCHash      uint64
	NSQSAR2Hash      uint64

	PitchBufHash uint64
	PitchBufLen  int
	PitchWinHash uint64
	PitchWinLen  int
	PitchBuf     []float32
}

// NSQTrace captures inputs/outputs for NSQ parity debugging.
type NSQTrace struct {
	CaptureInputs bool

	InputQ0          []int16
	PredCoefQ12      []int16
	LTPCoefQ14       []int16
	ARShpQ13         []int16
	HarmShapeGainQ14 []int
	TiltQ14          []int
	LFShpQ14         []int32
	GainsQ16         []int32
	PitchL           []int

	LambdaQ10              int
	LTPScaleQ14            int
	FrameLength            int
	SubfrLength            int
	NbSubfr                int
	LTPMemLength           int
	PredLPCOrder           int
	ShapeLPCOrder          int
	WarpingQ16             int
	NStatesDelayedDecision int
	SignalType             int
	QuantOffsetType        int
	NLSFInterpCoefQ2       int

	SeedIn         int
	SeedOut        int
	PulsesLen      int
	PulsesHash     uint64
	XqHash         uint64
	SLTPQ15Hash    uint64
	XScSubfrHash   []uint64
	XScQ10         []int32
	SLTPQ15        []int32
	SLTPRaw        []int16
	DelayedGainQ10 []int32

	// NSQ state snapshot before quantization.
	NSQXQ            []int16
	NSQSLTPShpQ14    []int32
	NSQLPCQ14        []int32
	NSQAR2Q14        []int32
	NSQLFARQ14       int32
	NSQDiffQ14       int32
	NSQLagPrev       int
	NSQSLTPBufIdx    int
	NSQSLTPShpBufIdx int
	NSQRandSeed      int32
	NSQPrevGainQ16   int32
	NSQRewhiteFlag   int

	// NSQ state snapshot after quantization.
	NSQPostXQ            []int16
	NSQPostSLTPShpQ14    []int32
	NSQPostLPCQ14        []int32
	NSQPostAR2Q14        []int32
	NSQPostLFARQ14       int32
	NSQPostDiffQ14       int32
	NSQPostLagPrev       int
	NSQPostSLTPBufIdx    int
	NSQPostSLTPShpBufIdx int
	NSQPostRandSeed      int32
	NSQPostPrevGainQ16   int32
	NSQPostRewhiteFlag   int
	NSQPostXQHash        uint64
	NSQPostSLTPShpHash   uint64
	NSQPostSLPCHash      uint64
	NSQPostSAR2Hash      uint64
}

func hashFloat32Slice(vals []float32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(math.Float32bits(v))
		h *= prime
	}
	return h
}

func hashScaledFloat32Slice(vals []float32, scale float32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(math.Float32bits(v * scale))
		h *= prime
	}
	return h
}

func hashFloat64AsFloat32(vals []float64) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(math.Float32bits(float32(v)))
		h *= prime
	}
	return h
}

func hashInt8Slice(vals []int8) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint8(v))
		h *= prime
	}
	return h
}

func hashInt16Slice(vals []int16) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint16(v))
		h *= prime
	}
	return h
}

func hashInt32Slice(vals []int32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint32(v))
		h *= prime
	}
	return h
}
