package celt

// ExportedOpPVQSearch exposes opPVQSearch for testing.
func ExportedOpPVQSearch(x []float64, k int) ([]int, float64) {
	return opPVQSearch(x, k)
}

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
