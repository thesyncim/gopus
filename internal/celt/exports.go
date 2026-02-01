package celt

// ExportedOpPVQSearch exposes opPVQSearch for testing.
func ExportedOpPVQSearch(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}

// BitsToPulsesExport exposes bitsToPulses for testing.
func BitsToPulsesExport(band, lm, bitsQ3 int) int {
	return bitsToPulses(band, lm, bitsQ3)
}

// GetPulsesExport exposes getPulses for testing.
func GetPulsesExport(q int) int {
	return getPulses(q)
}

// PulsesToBitsExport exposes pulsesToBits for testing.
func PulsesToBitsExport(band, lm, pulses int) int {
	return pulsesToBits(band, lm, pulses)
}

// ExpRotationExport exposes expRotation for testing.
func ExpRotationExport(x []float64, length, dir, stride, k, spread int) {
	expRotation(x, length, dir, stride, k, spread)
}

// OpPVQSearchExport exposes opPVQSearch for testing (same as ExportedOpPVQSearch).
func OpPVQSearchExport(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}

// PVQDebugInfo captures encoder-side PVQ inputs/outputs for testing.
type PVQDebugInfo struct {
	Band   int
	N      int
	K      int
	Spread int
	B      int
	X      []float64
	Pulses []int
	Index  uint32
	VSize  uint32
}

// PVQDebugHook, if set, is called by the encoder with PVQ inputs/outputs.
// It is nil in production and only intended for tests.
var PVQDebugHook func(info PVQDebugInfo)

// QuantDebugInfo captures quantization decisions for debugging encoder/decoder alignment.
type QuantDebugInfo struct {
	Band          int
	Encode        bool
	N             int
	B             int
	Bits          int
	Q             int
	K             int
	CurrBits      int
	RemainingBits int
	Split         bool
}

// QuantDebugHook, if set, is called during quantization with per-band debug info.
// It is nil in production and only intended for tests.
var QuantDebugHook func(info QuantDebugInfo)

// BandDebugInfo captures per-band allocation values inside quant_all_bands.
type BandDebugInfo struct {
	Band          int
	Encode        bool
	TellFrac      int
	Balance       int
	RemainingBits int
	Bits          int
	CurrBalance   int
	Pulses        int
	CodedBands    int
}

// BandDebugHook, if set, is called with per-band allocation state.
// It is nil in production and only intended for tests.
var BandDebugHook func(info BandDebugInfo)

// DynallocDebugInfo captures dynalloc offsets for debugging.
type DynallocDebugInfo struct {
	Encode  bool
	Offsets []int
}

// DynallocDebugHook, if set, is called with dynalloc offsets from encoder/decoder.
// It is nil in production and only intended for tests.
var DynallocDebugHook func(info DynallocDebugInfo)

// TrimDebugInfo captures allocation trim decisions for debugging.
type TrimDebugInfo struct {
	Encode  bool
	Trim    int
	Encoded bool
}

// TrimDebugHook, if set, is called with allocation trim decisions.
// It is nil in production and only intended for tests.
var TrimDebugHook func(info TrimDebugInfo)

// AllocTrimDebugInfo contains debug information for allocation trim computation.
type AllocTrimDebugInfo struct {
	TfEstimate     float64
	EquivRate      int
	EffectiveBytes int
	TargetBits     int
	AllocTrim      int
}

// EncodeFrameWithDebug encodes a frame and returns debug info.
func (e *Encoder) EncodeFrameWithDebug(pcm []float64, frameSize int) ([]byte, *AllocTrimDebugInfo, error) {
	// Set debug mode
	e.debugAllocTrim = true
	defer func() { e.debugAllocTrim = false }()

	packet, err := e.EncodeFrame(pcm, frameSize)
	if err != nil {
		return nil, nil, err
	}

	return packet, e.lastAllocTrimDebug, nil
}
