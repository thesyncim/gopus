package plc

import (
	"math"
)

// EnergyDecayPerFrame is the energy decay factor per lost frame.
// Applied to band energies to gradually fade concealment.
const EnergyDecayPerFrame = 0.85

// CELTDecoderState provides access to CELT decoder state needed for PLC.
// This interface allows PLC to access decoder state without importing the celt package.
type CELTDecoderState interface {
	// Channels returns the number of channels (1 or 2).
	Channels() int
	// PrevEnergy returns the previous frame's band energies.
	PrevEnergy() []float64
	// SetPrevEnergy updates the previous energy state.
	SetPrevEnergy(energies []float64)
	// RNG returns the current RNG state.
	RNG() uint32
	// SetRNG sets the RNG state.
	SetRNG(seed uint32)
	// PreemphState returns the de-emphasis filter state.
	PreemphState() []float64
	// OverlapBuffer returns the overlap buffer for synthesis.
	OverlapBuffer() []float64
	// SetOverlapBuffer sets the overlap buffer.
	SetOverlapBuffer(samples []float64)
}

// CELTBandInfo provides band configuration for CELT PLC.
type CELTBandInfo struct {
	// MaxBands is the maximum number of frequency bands.
	MaxBands int
	// HybridStartBand is the first band for hybrid mode (bands 17-21).
	HybridStartBand int
	// EffBands returns effective bands for a frame size.
	EffBands func(frameSize int) int
	// BandStart returns the starting bin for a band at given frame size.
	BandStart func(band, frameSize int) int
	// BandEnd returns the ending bin for a band at given frame size.
	BandEnd func(band, frameSize int) int
	// ValidFrameSize checks if frame size is valid.
	ValidFrameSize func(frameSize int) bool
	// Overlap is the overlap size for synthesis.
	Overlap int
}

func defaultCELTEffBands(frameSize int) int {
	switch frameSize {
	case 120:
		return 13
	case 240:
		return 17
	case 480:
		return 19
	case 960:
		return 21
	default:
		return 21
	}
}

func defaultCELTBandStart(band, frameSize int) int {
	switch band {
	case 0:
		return 0
	case 1:
		return frameSize / 960
	case 2:
		return (2 * frameSize) / 960
	case 3:
		return (3 * frameSize) / 960
	case 4:
		return (4 * frameSize) / 960
	case 5:
		return (5 * frameSize) / 960
	case 6:
		return (6 * frameSize) / 960
	case 7:
		return (7 * frameSize) / 960
	case 8:
		return (8 * frameSize) / 960
	case 9:
		return (10 * frameSize) / 960
	case 10:
		return (12 * frameSize) / 960
	case 11:
		return (14 * frameSize) / 960
	case 12:
		return (16 * frameSize) / 960
	case 13:
		return (20 * frameSize) / 960
	case 14:
		return (24 * frameSize) / 960
	case 15:
		return (28 * frameSize) / 960
	case 16:
		return (34 * frameSize) / 960
	case 17:
		return (40 * frameSize) / 960
	case 18:
		return (48 * frameSize) / 960
	case 19:
		return (60 * frameSize) / 960
	case 20:
		return (78 * frameSize) / 960
	case 21:
		return (100 * frameSize) / 960
	default:
		return 0
	}
}

func defaultCELTBandEnd(band, frameSize int) int {
	if band < 0 || band >= 21 {
		return frameSize
	}
	return defaultCELTBandStart(band+1, frameSize)
}

func defaultCELTValidFrameSize(frameSize int) bool {
	return frameSize == 120 || frameSize == 240 || frameSize == 480 || frameSize == 960
}

func defaultCELTBandInfo() CELTBandInfo {
	return CELTBandInfo{
		MaxBands:        21,
		HybridStartBand: 17,
		EffBands:        defaultCELTEffBands,
		BandStart:       defaultCELTBandStart,
		BandEnd:         defaultCELTBandEnd,
		ValidFrameSize:  defaultCELTValidFrameSize,
		Overlap:         120,
	}
}

// CELTSynthesizer provides synthesis functionality for CELT PLC.
type CELTSynthesizer interface {
	// Synthesize performs IMDCT synthesis for mono.
	Synthesize(coeffs []float64, transient bool, shortBlocks int) []float64
	// SynthesizeStereo performs IMDCT synthesis for stereo.
	SynthesizeStereo(coeffsL, coeffsR []float64, transient bool, shortBlocks int) []float64
}

// ConcealCELT generates concealment audio for a lost CELT frame.
//
// CELT PLC strategy:
//  1. Copy energy from previous frame with decay
//  2. Fill bands with noise at decayed energy levels
//  3. Apply normal IMDCT synthesis
//  4. Apply fade factor to output
//
// This maintains the spectral shape of the last frame while fading out.
//
// Parameters:
//   - dec: CELT decoder state from last good frame
//   - synth: CELT synthesizer for IMDCT
//   - frameSize: samples to generate at 48kHz (120, 240, 480, or 960)
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: concealed samples at 48kHz
func ConcealCELT(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float64) []float64 {
	if dec == nil {
		return make([]float64, frameSize)
	}

	channels := dec.Channels()

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float64, frameSize*channels)
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	// Get previous frame energy (will be decayed)
	prevEnergy := dec.PrevEnergy()

	// Create decayed energy for concealment
	concealEnergy := make([]float64, len(prevEnergy))
	for i := range prevEnergy {
		// Apply energy decay
		concealEnergy[i] = prevEnergy[i] * EnergyDecayPerFrame
	}

	// Generate noise-filled MDCT coefficients at the decayed energy levels
	var coeffs []float64
	var coeffsL, coeffsR []float64

	rng := dec.RNG()

	if channels == 2 {
		// Stereo: generate coefficients for both channels
		coeffsL = generateNoiseBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		// Mono: single set of coefficients
		coeffs = generateNoiseBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	// Synthesize using IMDCT + window + overlap-add
	var samples []float64
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.Synthesize(coeffs, false, 1)
		}
	} else {
		// No synthesizer available - return zeros
		samples = make([]float64, frameSize*channels)
	}

	// Apply de-emphasis to maintain filter state continuity
	applyDeemphasisPLC(samples, dec.PreemphState(), channels)

	// Update decoder energy state for next concealment
	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)

	return samples
}

// ConcealCELTInto generates concealment audio into a pre-allocated buffer.
// This is the zero-allocation version of ConcealCELT.
//
// Parameters:
//   - dst: destination buffer (must be at least frameSize*channels)
//   - dec: CELT decoder state from last good frame
//   - synth: CELT synthesizer for IMDCT
//   - frameSize: samples to generate at 48kHz (120, 240, 480, or 960)
//   - fadeFactor: gain multiplier (0.0 to 1.0)
func ConcealCELTInto(dst []float64, dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float64) {
	ConcealCELTRawInto(dst, dec, synth, frameSize, fadeFactor)
	if dec == nil {
		return
	}
	channels := dec.Channels()
	outLen := frameSize * channels
	if outLen > len(dst) {
		outLen = len(dst)
	}
	if outLen <= 0 {
		return
	}
	// Apply de-emphasis to maintain filter state continuity.
	applyDeemphasisPLC(dst[:outLen], dec.PreemphState(), channels)
}

// ConcealCELTRawInto generates concealment audio into a pre-allocated buffer
// without applying de-emphasis. Decoder-owned paths can use this to apply
// postfilter/de-emphasis in libopus order.
func ConcealCELTRawInto(dst []float64, dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float64) {
	if dec == nil {
		// Zero the output buffer
		for i := 0; i < frameSize && i < len(dst); i++ {
			dst[i] = 0
		}
		return
	}

	channels := dec.Channels()
	outLen := frameSize * channels

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		for i := 0; i < outLen && i < len(dst); i++ {
			dst[i] = 0
		}
		return
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	// Get previous frame energy (will be decayed)
	prevEnergy := dec.PrevEnergy()

	// Create decayed energy for concealment
	// Re-use prevEnergy slice in-place by modifying decoder state directly
	concealEnergy := make([]float64, len(prevEnergy))
	for i := range prevEnergy {
		// Apply energy decay
		concealEnergy[i] = prevEnergy[i] * EnergyDecayPerFrame
	}

	// Generate noise-filled MDCT coefficients at the decayed energy levels
	var coeffs []float64
	var coeffsL, coeffsR []float64

	rng := dec.RNG()

	if channels == 2 {
		// Stereo: generate coefficients for both channels
		coeffsL = generateNoiseBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		// Mono: single set of coefficients
		coeffs = generateNoiseBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	// Synthesize using IMDCT + window + overlap-add
	var samples []float64
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.Synthesize(coeffs, false, 1)
		}
	} else {
		// No synthesizer available - output zeros
		for i := 0; i < outLen && i < len(dst); i++ {
			dst[i] = 0
		}
		dec.SetPrevEnergy(concealEnergy)
		dec.SetRNG(rng)
		return
	}

	// Copy synthesized samples to destination
	copy(dst[:outLen], samples)

	// Update decoder energy state for next concealment
	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

// generateNoiseBands creates noise-filled MDCT coefficients scaled by band energies.
// Each band gets random noise normalized and scaled to the target energy level.
func generateNoiseBands(energies []float64, nbBands, frameSize int, rng *uint32, fadeFactor float64, bandInfo *CELTBandInfo) []float64 {
	// Number of MDCT bins = frameSize (CELT convention)
	coeffs := make([]float64, frameSize)

	for band := 0; band < nbBands && band < len(energies); band++ {
		// Get band boundaries
		startBin := bandInfo.BandStart(band, frameSize)
		endBin := bandInfo.BandEnd(band, frameSize)

		if startBin >= frameSize {
			break
		}
		if endBin > frameSize {
			endBin = frameSize
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			continue
		}

		// Get target energy for this band (linear scale from dB)
		// prevEnergy is stored in dB, convert to linear
		energyDB := energies[band]
		energyLin := math.Pow(10.0, energyDB/10.0)

		// Apply fade factor to energy
		energyLin *= fadeFactor * fadeFactor // Square for energy (amplitude squared)

		// Generate random unit-norm vector for the band
		noise := generateNoiseBand(rng, bandWidth)

		// Normalize noise vector
		normalizeVector(noise)

		// Scale by energy (sqrt because coefficients are amplitude)
		scale := math.Sqrt(energyLin)

		// Fill band with scaled noise
		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}

// generateNoiseBand creates a random vector for a band.
// Uses LCG from CELT decoder for deterministic noise.
func generateNoiseBand(rng *uint32, bandWidth int) []float64 {
	noise := make([]float64, bandWidth)

	for i := range noise {
		// LCG: same as libopus for reproducibility
		*rng = *rng*1664525 + 1013904223

		// Convert to [-1, 1] range
		// Use top bits for better randomness
		noise[i] = float64(int32(*rng)) / float64(1<<31)
	}

	return noise
}

// normalizeVector normalizes a vector to unit L2 norm.
func normalizeVector(v []float64) {
	// Compute L2 norm
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)

	if norm < 1e-10 {
		// Avoid division by zero - leave as is
		return
	}

	// Normalize
	invNorm := 1.0 / norm
	for i := range v {
		v[i] *= invNorm
	}
}

// applyDeemphasisPLC applies de-emphasis filter during PLC.
// This maintains the filter state for seamless transition to next good frame.
func applyDeemphasisPLC(samples []float64, state []float64, channels int) {
	if len(samples) == 0 || len(state) < channels {
		return
	}

	// De-emphasis coefficient (same as in decoder)
	const preemphCoef = 0.85

	if channels == 1 {
		// Mono de-emphasis
		s := state[0]
		for i := range samples {
			samples[i] = samples[i] + preemphCoef*s
			s = samples[i]
		}
		state[0] = s
	} else {
		// Stereo de-emphasis (interleaved samples)
		stateL := state[0]
		stateR := state[1]

		for i := 0; i < len(samples)-1; i += 2 {
			// Left channel
			samples[i] = samples[i] + preemphCoef*stateL
			stateL = samples[i]

			// Right channel
			samples[i+1] = samples[i+1] + preemphCoef*stateR
			stateR = samples[i+1]
		}

		state[0] = stateL
		state[1] = stateR
	}
}

// ConcealCELTHybrid generates concealment for CELT in hybrid mode.
// Only bands 17-21 are filled with noise (bands 0-16 are handled by SILK).
//
// Parameters:
//   - dec: CELT decoder state from last good frame
//   - synth: CELT synthesizer for IMDCT
//   - frameSize: samples to generate at 48kHz (480 or 960 for hybrid)
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: concealed high-frequency samples at 48kHz
func ConcealCELTHybrid(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float64) []float64 {
	if dec == nil {
		return make([]float64, frameSize)
	}
	channels := dec.Channels()
	outLen := frameSize * channels
	out := make([]float64, outLen)
	ConcealCELTHybridRawInto(out, dec, synth, frameSize, fadeFactor)
	if outLen > len(out) {
		outLen = len(out)
	}
	if outLen > 0 {
		applyDeemphasisPLC(out[:outLen], dec.PreemphState(), channels)
	}
	return out
}

// ConcealCELTHybridRawInto generates hybrid concealment into a pre-allocated
// buffer without applying de-emphasis. This lets decoder-owned paths apply
// postfilter/de-emphasis in libopus order.
func ConcealCELTHybridRawInto(dst []float64, dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float64) {
	if dec == nil {
		limit := frameSize
		if limit > len(dst) {
			limit = len(dst)
		}
		for i := 0; i < limit; i++ {
			dst[i] = 0
		}
		return
	}

	channels := dec.Channels()
	outLen := frameSize * channels
	if outLen > len(dst) {
		outLen = len(dst)
	}

	if fadeFactor < 0.001 {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
		return
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	prevEnergy := dec.PrevEnergy()
	concealEnergy := make([]float64, len(prevEnergy))
	for i := range prevEnergy {
		concealEnergy[i] = prevEnergy[i] * EnergyDecayPerFrame
	}

	rng := dec.RNG()
	var coeffs []float64
	var coeffsL, coeffsR []float64
	if channels == 2 {
		coeffsL = generateNoiseHybridBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseHybridBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		coeffs = generateNoiseHybridBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	var samples []float64
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.Synthesize(coeffs, false, 1)
		}
	}
	if len(samples) > 0 {
		copy(dst[:outLen], samples[:minInt(outLen, len(samples))])
		if len(samples) < outLen {
			for i := len(samples); i < outLen; i++ {
				dst[i] = 0
			}
		}
	} else {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
	}

	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

// ConcealCELTHybridRawIntoWithDBDecay is a hybrid raw concealment variant
// where per-band energy decay is applied in log-energy (dB-like) units.
func ConcealCELTHybridRawIntoWithDBDecay(
	dst []float64,
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float64,
	decayDB float64,
) {
	if dec == nil {
		limit := frameSize
		if limit > len(dst) {
			limit = len(dst)
		}
		for i := 0; i < limit; i++ {
			dst[i] = 0
		}
		return
	}

	channels := dec.Channels()
	outLen := frameSize * channels
	if outLen > len(dst) {
		outLen = len(dst)
	}
	if fadeFactor < 0.001 {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
		return
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	prevEnergy := dec.PrevEnergy()
	concealEnergy := make([]float64, len(prevEnergy))
	for i := range prevEnergy {
		concealEnergy[i] = prevEnergy[i] - decayDB
	}

	rng := dec.RNG()
	var coeffs []float64
	var coeffsL, coeffsR []float64
	if channels == 2 {
		coeffsL = generateNoiseHybridBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseHybridBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		coeffs = generateNoiseHybridBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	var samples []float64
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.Synthesize(coeffs, false, 1)
		}
	}
	if len(samples) > 0 {
		copy(dst[:outLen], samples[:minInt(outLen, len(samples))])
		if len(samples) < outLen {
			for i := len(samples); i < outLen; i++ {
				dst[i] = 0
			}
		}
	} else {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
	}

	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

// ConcealCELTHybridRawIntoFromEnergies generates hybrid raw concealment using
// caller-supplied per-band log energies.
func ConcealCELTHybridRawIntoFromEnergies(
	dst []float64,
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float64,
	energies []float64,
) {
	if dec == nil {
		limit := frameSize
		if limit > len(dst) {
			limit = len(dst)
		}
		for i := 0; i < limit; i++ {
			dst[i] = 0
		}
		return
	}

	channels := dec.Channels()
	outLen := frameSize * channels
	if outLen > len(dst) {
		outLen = len(dst)
	}
	if fadeFactor < 0.001 {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
		return
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	prevEnergy := dec.PrevEnergy()
	concealEnergy := prevEnergy
	if len(energies) >= len(prevEnergy) {
		concealEnergy = energies[:len(prevEnergy)]
	}

	rng := dec.RNG()
	var coeffs []float64
	var coeffsL, coeffsR []float64
	if channels == 2 {
		coeffsL = generateNoiseHybridBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		coeffsR = generateNoiseHybridBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		coeffs = generateNoiseHybridBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	var samples []float64
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereo(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.Synthesize(coeffs, false, 1)
		}
	}
	if len(samples) > 0 {
		copy(dst[:outLen], samples[:minInt(outLen, len(samples))])
		if len(samples) < outLen {
			for i := len(samples); i < outLen; i++ {
				dst[i] = 0
			}
		}
	} else {
		for i := 0; i < outLen; i++ {
			dst[i] = 0
		}
	}

	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// generateNoiseHybridBands generates noise for hybrid mode (bands 17-21 only).
func generateNoiseHybridBands(energies []float64, nbBands, frameSize int, rng *uint32, fadeFactor float64, bandInfo *CELTBandInfo) []float64 {
	coeffs := make([]float64, frameSize)

	// Start at hybrid start band (17)
	startBand := bandInfo.HybridStartBand

	for band := startBand; band < nbBands && band < len(energies); band++ {
		startBin := bandInfo.BandStart(band, frameSize)
		endBin := bandInfo.BandEnd(band, frameSize)

		if startBin >= frameSize {
			break
		}
		if endBin > frameSize {
			endBin = frameSize
		}

		bandWidth := endBin - startBin
		if bandWidth <= 0 {
			continue
		}

		// Get target energy
		energyDB := energies[band]
		energyLin := math.Pow(10.0, energyDB/10.0)
		energyLin *= fadeFactor * fadeFactor

		// Generate and scale noise
		noise := generateNoiseBand(rng, bandWidth)
		normalizeVector(noise)
		scale := math.Sqrt(energyLin)

		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}
