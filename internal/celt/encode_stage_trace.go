//go:build gopus_celt_trace && !gopus_fixed_point

package celt

// EncodeStageTrace holds bounded CELT frame intermediates for the opt-in
// first-divergence oracle test. Values use the codec's float32 storage width.
type EncodeStageTrace struct {
	BandStages     []EncodeBandStageTrace
	Normalizations []EncodeNormalizationTrace
	CoarseEnergy   []EncodeCoarseEnergyTrace
	BandQuantize   []EncodeBandQuantizeTrace
}

type EncodeBandStageTrace struct {
	FrameCoeffs   int
	Bands         int
	Channels      int
	LM            int
	HasAmplitudes bool
	Spectrum      []float32
	Amplitudes    []float32
	LogEnergy     []float32
}

type EncodeNormalizationTrace struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Normalized   []float32
}

type EncodeCoarseEnergyTrace struct {
	Bands       int
	Channels    int
	BudgetBytes int
	Input       []float32
	Quantized   []float32
	Error       []float32
}

type EncodeBandQuantizeTrace struct {
	ActiveCoeffs int
	Bands        int
	Channels     int
	BandEnergy   []float32
	Input        []float32
	Output       []float32
}

type encodeStageTraceState struct {
	enabled bool
	trace   EncodeStageTrace
}

// EnableEncodeStageTraceForTesting resets and enables frame-stage captures.
// It is available only with the gopus_celt_trace build tag.
func (e *Encoder) EnableEncodeStageTraceForTesting() {
	e.encodeStageTrace.reset()
}

// EncodeStageTraceForTesting returns the captured CELT frame-stage values.
// Slices remain valid until the next encode or trace reset.
func (e *Encoder) EncodeStageTraceForTesting() EncodeStageTrace {
	return e.encodeStageTrace.trace
}

func (s *encodeStageTraceState) reset() {
	s.enabled = true
	s.trace = EncodeStageTrace{
		BandStages:     make([]EncodeBandStageTrace, 0, 4),
		Normalizations: make([]EncodeNormalizationTrace, 0, 2),
		CoarseEnergy:   make([]EncodeCoarseEnergyTrace, 0, 2),
		BandQuantize:   make([]EncodeBandQuantizeTrace, 0, 2),
	}
}

func (s *encodeStageTraceState) recordBandStage(spectrum []float32, amplitudes []CeltEner, logEnergy []CeltGLog, frameCoeffs, channels, bands, lm int) {
	if !s.enabled {
		return
	}
	s.trace.BandStages = append(s.trace.BandStages, EncodeBandStageTrace{
		FrameCoeffs:   frameCoeffs,
		Bands:         bands,
		Channels:      channels,
		LM:            lm,
		HasAmplitudes: amplitudes != nil,
		Spectrum:      copyStageFloat32(spectrum),
		Amplitudes:    copyStageFloat32(amplitudes),
		LogEnergy:     copyStageFloat32(logEnergy),
	})
}

func (s *encodeStageTraceState) recordNormalization(normL, normR []CeltNorm, bandEnergy []CeltEner, activeCoeffs, bands, channels int) {
	if !s.enabled {
		return
	}
	normalized := make([]float32, activeCoeffs*channels)
	copyStagePrefix(normalized, normL, activeCoeffs)
	if channels > 1 {
		copyStagePrefix(normalized[activeCoeffs:], normR, activeCoeffs)
	}
	s.trace.Normalizations = append(s.trace.Normalizations, EncodeNormalizationTrace{
		ActiveCoeffs: activeCoeffs,
		Bands:        bands,
		Channels:     channels,
		BandEnergy:   copyStageFloat32(bandEnergy),
		Normalized:   normalized,
	})
}

func (e *Encoder) recordEncodeNormalizationTrace(normL, normR []CeltNorm, bandEnergy []CeltEner, bands, lm, channels int) {
	activeCoeffs := e.modeEdges()[bands] * (1 << lm)
	e.encodeStageTrace.recordNormalization(normL, normR, bandEnergy, activeCoeffs, bands, channels)
}

func (s *encodeStageTraceState) recordCoarseInput(input []CeltGLog, bands, channels int, budgetBytes int32) {
	if !s.enabled {
		return
	}
	s.trace.CoarseEnergy = append(s.trace.CoarseEnergy, EncodeCoarseEnergyTrace{
		Bands:       bands,
		Channels:    channels,
		BudgetBytes: int(budgetBytes),
		Input:       copyStageFloat32(input[:bands*channels]),
	})
}

func (s *encodeStageTraceState) recordCoarseOutput(quantized, errorValues []CeltGLog) {
	if !s.enabled || len(s.trace.CoarseEnergy) == 0 {
		return
	}
	stage := &s.trace.CoarseEnergy[len(s.trace.CoarseEnergy)-1]
	stage.Quantized = copyStageFloat32(quantized)
	stage.Error = copyStageFloat32(errorValues[:len(quantized)])
}

func (s *encodeStageTraceState) recordQuantInput(normL, normR []CeltNorm, bandEnergy []CeltEner, activeCoeffs, bands, channels int) {
	if !s.enabled {
		return
	}
	input := make([]float32, activeCoeffs*channels)
	copyStagePrefix(input, normL, activeCoeffs)
	if channels > 1 {
		copyStagePrefix(input[activeCoeffs:], normR, activeCoeffs)
	}
	s.trace.BandQuantize = append(s.trace.BandQuantize, EncodeBandQuantizeTrace{
		ActiveCoeffs: activeCoeffs,
		Bands:        bands,
		Channels:     channels,
		BandEnergy:   copyStageFloat32(bandEnergy),
		Input:        input,
	})
}

func (e *Encoder) recordEncodeQuantInputTrace(normL, normR []CeltNorm, bandEnergy []CeltEner, bands, lm, channels int) {
	activeCoeffs := e.modeEdges()[bands] * (1 << lm)
	e.encodeStageTrace.recordQuantInput(normL, normR, bandEnergy, activeCoeffs, bands, channels)
}

func (s *encodeStageTraceState) recordQuantOutput(normL, normR []CeltNorm) {
	if !s.enabled || len(s.trace.BandQuantize) == 0 {
		return
	}
	stage := &s.trace.BandQuantize[len(s.trace.BandQuantize)-1]
	stage.Output = make([]float32, stage.ActiveCoeffs*stage.Channels)
	copyStagePrefix(stage.Output, normL, stage.ActiveCoeffs)
	if stage.Channels > 1 {
		copyStagePrefix(stage.Output[stage.ActiveCoeffs:], normR, stage.ActiveCoeffs)
	}
}

func copyStageFloat32[T ~float32](src []T) []float32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float32, len(src))
	copyStagePrefix(dst, src, len(src))
	return dst
}

func copyStagePrefix[T ~float32](dst []float32, src []T, n int) {
	if n > len(src) {
		n = len(src)
	}
	for i := 0; i < n; i++ {
		dst[i] = float32(src[i])
	}
}
