package plc

import "github.com/thesyncim/gopus/internal/opusmath"

// EnergyDecayPerFrame is the energy decay factor per lost frame.
// Applied to band energies to gradually fade concealment.
const EnergyDecayPerFrame = 0.85

func pow10F32(x float32) float32 {
	return opusmath.Pow10F32(x)
}

func sqrtF32(x float32) float32 {
	return opusmath.SqrtF32(x)
}

// CELTDecoderState provides access to CELT decoder state needed for PLC.
// This interface allows PLC to access decoder state without importing the celt package.
type CELTDecoderState interface {
	// Channels returns the number of channels (1 or 2).
	Channels() int
	// PrevEnergy returns the previous frame's band energies.
	PrevEnergy() []float32
	// SetPrevEnergy updates the previous energy state.
	SetPrevEnergy(energies []float32)
	// RNG returns the current RNG state.
	RNG() uint32
	// SetRNG sets the RNG state.
	SetRNG(seed uint32)
	// PreemphState returns the de-emphasis filter state.
	PreemphState() []float32
	// OverlapBuffer returns the overlap buffer for synthesis.
	OverlapBuffer() []float32
	// SetOverlapBuffer sets the overlap buffer.
	SetOverlapBuffer(samples []float32)
}

type celtPreemphSetter interface {
	SetPreemphState(samples []float32)
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
	SynthesizeFloat32(coeffs []float32, transient bool, shortBlocks int) []float32
	// SynthesizeStereo performs IMDCT synthesis for stereo.
	SynthesizeStereoFloat32(coeffsL, coeffsR []float32, transient bool, shortBlocks int) []float32
}

type celtConcealmentEnergyMode uint8

const (
	celtConcealmentEnergyDecay celtConcealmentEnergyMode = iota
	celtConcealmentEnergyDBDecay
	celtConcealmentEnergyProvided
)

type celtConcealmentConfig struct {
	hybrid     bool
	energyMode celtConcealmentEnergyMode
	decayDB    float32
	energies   []float32
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
func ConcealCELT(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) []float32 {
	if dec == nil {
		return make([]float32, frameSize)
	}

	channels := dec.Channels()

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float32, frameSize*channels)
	}

	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)

	// Get previous frame energy (will be decayed)
	prevEnergy := dec.PrevEnergy()

	// Create decayed energy for concealment
	concealEnergy := make([]float32, len(prevEnergy))
	for i := range prevEnergy {
		// Apply energy decay
		concealEnergy[i] = prevEnergy[i] * float32(EnergyDecayPerFrame)
	}

	// Generate noise-filled MDCT coefficients at the decayed energy levels
	var coeffs []float32
	var coeffsL, coeffsR []float32

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
	var samples []float32
	if synth != nil {
		if channels == 2 {
			samples = synth.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1)
		} else {
			samples = synth.SynthesizeFloat32(coeffs, false, 1)
		}
	} else {
		// No synthesizer available - return zeros
		samples = make([]float32, frameSize*channels)
	}

	// Apply de-emphasis to maintain filter state continuity
	applyDeemphasisPLCToDecoderFloat32(samples, dec, channels)

	// Update decoder energy state for next concealment
	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)

	return samples
}

// generateNoiseBands creates noise-filled MDCT coefficients scaled by band energies.
// Each band gets random noise normalized and scaled to the target energy level.
func generateNoiseBands(energies []float32, nbBands, frameSize int, rng *uint32, fadeFactor float32, bandInfo *CELTBandInfo) []float32 {
	// Number of MDCT bins = frameSize (CELT convention)
	coeffs := make([]float32, frameSize)

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
		energyLin := pow10F32(energies[band] * 0.1)

		// Apply fade factor to energy
		energyLin *= fadeFactor * fadeFactor // Square for energy (amplitude squared)

		// Generate random unit-norm vector for the band
		noise := generateNoiseBand(rng, bandWidth)

		// Normalize noise vector
		normalizeVector(noise)

		// Scale by energy (sqrt because coefficients are amplitude)
		scale := sqrtF32(energyLin)

		// Fill band with scaled noise
		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}

// generateNoiseBand creates a random vector for a band.
// Uses LCG from CELT decoder for deterministic noise.
func generateNoiseBand(rng *uint32, bandWidth int) []float32 {
	noise := make([]float32, bandWidth)

	for i := range noise {
		// LCG: same as libopus for reproducibility
		*rng = *rng*1664525 + 1013904223

		// Convert to [-1, 1] range
		// Use top bits for better randomness
		noise[i] = float32(int32(*rng)) * (1.0 / float32(1<<31))
	}

	return noise
}

// normalizeVector normalizes a vector to unit L2 norm.
func normalizeVector(v []float32) {
	// Compute L2 norm
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	norm = sqrtF32(norm)

	if norm < 1e-10 {
		// Avoid division by zero - leave as is
		return
	}

	// Normalize
	invNorm := float32(1.0) / norm
	for i := range v {
		v[i] *= invNorm
	}
}

func applyDeemphasisPLCToDecoderFloat32(samples []float32, dec CELTDecoderState, channels int) {
	if dec == nil || len(samples) == 0 {
		return
	}
	state := dec.PreemphState()
	if len(state) < channels {
		return
	}
	const preemphCoef = float32(0.85)
	if channels == 1 {
		s := state[0]
		for i := range samples {
			tmp := samples[i] + preemphCoef*s
			samples[i] = tmp
			s = tmp
		}
		state[0] = s
	} else {
		stateL := state[0]
		stateR := state[1]
		for i := 0; i < len(samples)-1; i += 2 {
			left := samples[i] + preemphCoef*stateL
			samples[i] = left
			stateL = left
			right := samples[i+1] + preemphCoef*stateR
			samples[i+1] = right
			stateR = right
		}
		state[0] = stateL
		state[1] = stateR
	}
	if setter, ok := dec.(celtPreemphSetter); ok {
		setter.SetPreemphState(state)
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
func ConcealCELTHybrid(dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) []float32 {
	if dec == nil {
		return make([]float32, frameSize)
	}
	channels := dec.Channels()
	outLen := frameSize * channels
	out := make([]float32, outLen)
	ConcealCELTHybridRawInto(out, dec, synth, frameSize, fadeFactor)
	if outLen > len(out) {
		outLen = len(out)
	}
	if outLen > 0 {
		applyDeemphasisPLCToDecoderFloat32(out[:outLen], dec, channels)
	}
	return out
}

// ConcealCELTHybridRawInto generates hybrid concealment into a pre-allocated
// buffer without applying de-emphasis. This lets decoder-owned paths apply
// postfilter/de-emphasis in libopus order.
func ConcealCELTHybridRawInto(dst []float32, dec CELTDecoderState, synth CELTSynthesizer, frameSize int, fadeFactor float32) {
	writeCELTConcealment(dst, dec, synth, frameSize, fadeFactor, celtConcealmentConfig{hybrid: true})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// writeCELTConcealment centralizes the raw PLC path used by the fullband and
// hybrid entry points.
func writeCELTConcealment(
	dst []float32,
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float32,
	cfg celtConcealmentConfig,
) {
	if dec == nil {
		zeroCELTConcealment(dst, frameSize)
		return
	}

	channels := dec.Channels()
	outLen := minInt(frameSize*channels, len(dst))
	if fadeFactor < 0.001 {
		zeroCELTConcealment(dst, outLen)
		return
	}

	concealEnergy := buildCELTConcealmentEnergies(dec.PrevEnergy(), cfg)
	samples, rng := synthesizeCELTConcealment(dec, synth, frameSize, fadeFactor, concealEnergy, cfg.hybrid)
	copyCELTConcealment(dst, samples, outLen)

	dec.SetPrevEnergy(concealEnergy)
	dec.SetRNG(rng)
}

func buildCELTConcealmentEnergies(prevEnergy []float32, cfg celtConcealmentConfig) []float32 {
	switch cfg.energyMode {
	case celtConcealmentEnergyDBDecay:
		concealEnergy := make([]float32, len(prevEnergy))
		for i := range prevEnergy {
			concealEnergy[i] = prevEnergy[i] - cfg.decayDB
		}
		return concealEnergy
	case celtConcealmentEnergyProvided:
		if len(cfg.energies) >= len(prevEnergy) {
			return cfg.energies[:len(prevEnergy)]
		}
		return prevEnergy
	default:
		concealEnergy := make([]float32, len(prevEnergy))
		for i := range prevEnergy {
			concealEnergy[i] = prevEnergy[i] * float32(EnergyDecayPerFrame)
		}
		return concealEnergy
	}
}

func synthesizeCELTConcealment(
	dec CELTDecoderState,
	synth CELTSynthesizer,
	frameSize int,
	fadeFactor float32,
	concealEnergy []float32,
	hybrid bool,
) ([]float32, uint32) {
	bandInfo := defaultCELTBandInfo()
	nbBands := bandInfo.EffBands(frameSize)
	channels := dec.Channels()
	rng := dec.RNG()

	var coeffs []float32
	var coeffsL, coeffsR []float32
	if channels == 2 {
		if hybrid {
			coeffsL = generateNoiseHybridBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
			coeffsR = generateNoiseHybridBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		} else {
			coeffsL = generateNoiseBands(concealEnergy[:bandInfo.MaxBands], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
			coeffsR = generateNoiseBands(concealEnergy[bandInfo.MaxBands:], nbBands, frameSize, &rng, fadeFactor, &bandInfo)
		}
	} else if hybrid {
		coeffs = generateNoiseHybridBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	} else {
		coeffs = generateNoiseBands(concealEnergy, nbBands, frameSize, &rng, fadeFactor, &bandInfo)
	}

	if synth == nil {
		return nil, rng
	}
	if channels == 2 {
		return synth.SynthesizeStereoFloat32(coeffsL, coeffsR, false, 1), rng
	}
	return synth.SynthesizeFloat32(coeffs, false, 1), rng
}

func copyCELTConcealment(dst, samples []float32, outLen int) {
	if len(samples) == 0 {
		zeroCELTConcealment(dst, outLen)
		return
	}

	n := minInt(outLen, len(samples))
	copy(dst[:outLen], samples[:n])
	for i := n; i < outLen; i++ {
		dst[i] = 0
	}
}

func zeroCELTConcealment(dst []float32, limit int) {
	if limit > len(dst) {
		limit = len(dst)
	}
	for i := 0; i < limit; i++ {
		dst[i] = 0
	}
}

// generateNoiseHybridBands generates noise for hybrid mode (bands 17-21 only).
func generateNoiseHybridBands(energies []float32, nbBands, frameSize int, rng *uint32, fadeFactor float32, bandInfo *CELTBandInfo) []float32 {
	coeffs := make([]float32, frameSize)

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
		energyLin := pow10F32(energies[band] * 0.1)
		energyLin *= fadeFactor * fadeFactor

		// Generate and scale noise
		noise := generateNoiseBand(rng, bandWidth)
		normalizeVector(noise)
		scale := sqrtF32(energyLin)

		for i := 0; i < bandWidth && startBin+i < frameSize; i++ {
			coeffs[startBin+i] = noise[i] * scale
		}
	}

	return coeffs
}
