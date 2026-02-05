package silk

import "math"

// EncoderTrace captures intermediate values for parity debugging.
// When non-nil, the encoder will populate it during EncodeFrame.
type EncoderTrace struct {
	Pitch *PitchTrace
	NLSF  *NLSFTrace
	LTP   *LTPTrace
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

func hashFloat32Slice(vals []float32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(math.Float32bits(v))
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
