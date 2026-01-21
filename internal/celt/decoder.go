package celt

import (
	"errors"

	"gopus/internal/plc"
	"gopus/internal/rangecoding"
)

// Decoding errors
var (
	// ErrInvalidFrame indicates the frame data is invalid or corrupted.
	ErrInvalidFrame = errors.New("celt: invalid frame data")

	// ErrInvalidFrameSize indicates an unsupported frame size.
	ErrInvalidFrameSize = errors.New("celt: invalid frame size")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("celt: nil range decoder")
)

// celtPLCState tracks packet loss concealment state for CELT decoding.
var celtPLCState = plc.NewState()

// Decoder decodes CELT frames from an Opus packet.
// It maintains state across frames for proper audio continuity via overlap-add
// synthesis and energy prediction.
//
// CELT is the transform-based layer of Opus, using the Modified Discrete Cosine
// Transform (MDCT) for music and general audio. The decoder reconstructs audio by:
// 1. Decoding energy envelope (coarse + fine quantization)
// 2. Decoding normalized band shapes via PVQ
// 3. Applying denormalization (scaling by energy)
// 4. Performing IMDCT synthesis with overlap-add
// 5. Applying de-emphasis filter
//
// Reference: RFC 6716 Section 4.3
type Decoder struct {
	// Configuration
	channels   int // 1 or 2
	sampleRate int // Output sample rate (typically 48000)

	// Range decoder (set per frame)
	rangeDecoder *rangecoding.Decoder

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []float64 // Two frames ago energies (for anti-collapse)

	// Synthesis state (persists for overlap-add)
	overlapBuffer []float64 // Previous frame overlap tail [Overlap * channels]
	preemphState  []float64 // De-emphasis filter state [channels]

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int     // Pitch period for comb filter
	postfilterGain   float64 // Comb filter gain
	postfilterTapset int     // Filter tap configuration (0, 1, or 2)

	// Error recovery / deterministic randomness
	rng uint32 // RNG state for PLC and folding

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)
}

// NewDecoder creates a new CELT decoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The decoder is ready to process CELT frames after creation.
func NewDecoder(channels int) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	d := &Decoder{
		channels:   channels,
		sampleRate: 48000, // CELT always operates at 48kHz internally

		// Allocate energy arrays for all bands and channels
		prevEnergy:  make([]float64, MaxBands*channels),
		prevEnergy2: make([]float64, MaxBands*channels),

		// Overlap buffer for IMDCT overlap-add
		// Size is Overlap (120) samples per channel
		overlapBuffer: make([]float64, Overlap*channels),

		// De-emphasis filter state, one per channel
		preemphState: make([]float64, channels),

		// Initialize RNG with non-zero seed
		rng: 22222,
	}

	// Initialize energy arrays to reasonable defaults
	// Using negative infinity would cause issues; use small energy instead
	for i := range d.prevEnergy {
		d.prevEnergy[i] = -28.0 // Low but finite starting energy
		d.prevEnergy2[i] = -28.0
	}

	return d
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	// Clear energy arrays
	for i := range d.prevEnergy {
		d.prevEnergy[i] = -28.0
		d.prevEnergy2[i] = -28.0
	}

	// Clear overlap buffer
	for i := range d.overlapBuffer {
		d.overlapBuffer[i] = 0
	}

	// Clear de-emphasis state
	for i := range d.preemphState {
		d.preemphState[i] = 0
	}

	// Reset postfilter
	d.postfilterPeriod = 0
	d.postfilterGain = 0
	d.postfilterTapset = 0

	// Reset RNG
	d.rng = 22222

	// Clear range decoder reference
	d.rangeDecoder = nil
}

// SetRangeDecoder sets the range decoder for the current frame.
// This must be called before decoding each frame.
func (d *Decoder) SetRangeDecoder(rd *rangecoding.Decoder) {
	d.rangeDecoder = rd
}

// RangeDecoder returns the current range decoder.
func (d *Decoder) RangeDecoder() *rangecoding.Decoder {
	return d.rangeDecoder
}

// Channels returns the number of audio channels (1 or 2).
func (d *Decoder) Channels() int {
	return d.channels
}

// SampleRate returns the output sample rate (always 48000 for CELT).
func (d *Decoder) SampleRate() int {
	return d.sampleRate
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy decoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (d *Decoder) PrevEnergy() []float64 {
	return d.prevEnergy
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (d *Decoder) PrevEnergy2() []float64 {
	return d.prevEnergy2
}

// SetPrevEnergy copies the given energies to the previous energy buffer.
// Also shifts current prev to prev2.
func (d *Decoder) SetPrevEnergy(energies []float64) {
	// Shift: current prev becomes prev2
	copy(d.prevEnergy2, d.prevEnergy)
	// Copy new energies to prev
	copy(d.prevEnergy, energies)
}

// OverlapBuffer returns the overlap buffer for IMDCT overlap-add.
// Size is Overlap * channels samples.
func (d *Decoder) OverlapBuffer() []float64 {
	return d.overlapBuffer
}

// SetOverlapBuffer copies the given samples to the overlap buffer.
func (d *Decoder) SetOverlapBuffer(samples []float64) {
	copy(d.overlapBuffer, samples)
}

// PreemphState returns the de-emphasis filter state.
// One value per channel.
func (d *Decoder) PreemphState() []float64 {
	return d.preemphState
}

// PostfilterPeriod returns the pitch period for the postfilter.
func (d *Decoder) PostfilterPeriod() int {
	return d.postfilterPeriod
}

// PostfilterGain returns the comb filter gain.
func (d *Decoder) PostfilterGain() float64 {
	return d.postfilterGain
}

// PostfilterTapset returns the filter tap configuration.
func (d *Decoder) PostfilterTapset() int {
	return d.postfilterTapset
}

// SetPostfilter sets the postfilter parameters.
func (d *Decoder) SetPostfilter(period int, gain float64, tapset int) {
	d.postfilterPeriod = period
	d.postfilterGain = gain
	d.postfilterTapset = tapset
}

// RNG returns the current RNG state.
func (d *Decoder) RNG() uint32 {
	return d.rng
}

// SetRNG sets the RNG state.
func (d *Decoder) SetRNG(seed uint32) {
	d.rng = seed
}

// NextRNG advances the RNG and returns the new value.
// Uses the same LCG as libopus for deterministic behavior.
func (d *Decoder) NextRNG() uint32 {
	d.rng = d.rng*1664525 + 1013904223
	return d.rng
}

// GetEnergy returns the energy for a specific band and channel from prevEnergy.
func (d *Decoder) GetEnergy(band, channel int) float64 {
	if band < 0 || band >= MaxBands || channel < 0 || channel >= d.channels {
		return 0
	}
	return d.prevEnergy[channel*MaxBands+band]
}

// SetEnergy sets the energy for a specific band and channel.
func (d *Decoder) SetEnergy(band, channel int, energy float64) {
	if band < 0 || band >= MaxBands || channel < 0 || channel >= d.channels {
		return
	}
	d.prevEnergy[channel*MaxBands+band] = energy
}

// DecodeFrame decodes a complete CELT frame from raw bytes.
// If data is nil, performs Packet Loss Concealment (PLC) instead of decoding.
// data: raw CELT frame bytes (without Opus framing), or nil for PLC
// frameSize: expected output samples (120, 240, 480, or 960)
// Returns: PCM samples as float64 slice, interleaved if stereo
//
// The decoding pipeline:
// 1. Initialize range decoder
// 2. Decode frame header flags (silence, transient, intra)
// 3. Decode energy envelope (coarse + fine)
// 4. Compute bit allocation
// 5. Decode bands via PVQ
// 6. Synthesis: IMDCT + windowing + overlap-add
// 7. Apply de-emphasis filter
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_decoder.c celt_decode_with_ec()
func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float64, error) {
	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(frameSize)
	}

	if len(data) == 0 {
		return nil, ErrInvalidFrame
	}

	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Decode frame header flags
	silence := d.decodeSilenceFlag()
	if silence {
		// Silence frame: return zeros
		return d.decodeSilenceFrame(frameSize), nil
	}

	// Decode transient and intra flags
	transient := d.decodeTransientFlag(lm)
	intra := d.decodeIntraFlag()

	// Decode spread (for folding)
	// spread := d.decodeSpread()
	// _ = spread // Used later for folding

	// Determine short blocks for transient mode
	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 1: Decode coarse energy
	energies := d.DecodeCoarseEnergy(nbBands, intra, lm)

	// Step 2: Compute bit allocation
	// Estimate total bits remaining
	totalBits := len(data)*8 - rd.Tell()
	if totalBits < 0 {
		totalBits = 0
	}

	// Use default allocation parameters
	allocResult := ComputeAllocation(
		totalBits,
		nbBands,
		nil,  // caps
		nil,  // dynalloc
		0,    // trim (neutral)
		-1,   // intensity (disabled)
		false, // dual stereo
		lm,
	)

	// Step 3: Decode fine energy
	d.DecodeFineEnergy(energies, nbBands, allocResult.FineBits)

	// Step 4: Decode bands (PVQ)
	var coeffs []float64
	var coeffsL, coeffsR []float64

	if d.channels == 2 {
		// Stereo decoding
		// Split energies for L/R channels
		energiesL := energies[:nbBands]
		energiesR := make([]float64, nbBands)
		if len(energies) > nbBands {
			energiesR = energies[nbBands:]
		} else {
			copy(energiesR, energiesL) // Fallback: copy left
		}

		coeffsL, coeffsR = d.DecodeBandsStereo(
			energiesL, energiesR,
			allocResult.BandBits,
			nbBands,
			frameSize,
			-1, // intensity band (disabled)
		)
	} else {
		// Mono decoding
		coeffs = d.DecodeBands(energies, allocResult.BandBits, nbBands, false, frameSize)
	}

	// Step 5: Decode energy remainder (leftover bits)
	d.DecodeEnergyRemainder(energies, nbBands, allocResult.RemainderBits)

	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64

	if d.channels == 2 {
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		samples = d.Synthesize(coeffs, transient, shortBlocks)
	}

	// Step 7: Apply de-emphasis filter
	d.applyDeemphasis(samples)

	// Update energy state for next frame
	d.SetPrevEnergy(energies)

	// Reset PLC state after successful decode
	celtPLCState.Reset()
	celtPLCState.SetLastFrameParams(plc.ModeCELT, frameSize, d.channels)

	return samples, nil
}

// decodeSilenceFlag decodes the silence flag from the bitstream.
// Returns true if this is a silence frame.
func (d *Decoder) decodeSilenceFlag() bool {
	if d.rangeDecoder == nil {
		return false
	}
	// Silence is indicated by first bit = 1
	return d.rangeDecoder.DecodeBit(15) == 1
}

// decodeTransientFlag decodes the transient flag.
// Returns true if this frame uses short blocks (transient mode).
func (d *Decoder) decodeTransientFlag(lm int) bool {
	if d.rangeDecoder == nil {
		return false
	}
	// Transient flag is only present for frames with LM >= 1
	if lm < 1 {
		return false
	}
	// Probability depends on frame size
	logp := uint(3) // P(transient) = 1/8
	return d.rangeDecoder.DecodeBit(logp) == 1
}

// decodeIntraFlag decodes the intra flag.
// Returns true if this is an intra frame (no inter-frame prediction).
func (d *Decoder) decodeIntraFlag() bool {
	if d.rangeDecoder == nil {
		return false
	}
	// Intra flag
	logp := uint(3) // P(intra) = 1/8
	return d.rangeDecoder.DecodeBit(logp) == 1
}

// decodeSpread decodes the spread value for folding.
// Returns spread decision (0-3).
func (d *Decoder) decodeSpread() int {
	if d.rangeDecoder == nil {
		return 0
	}
	// Spread is decoded as 2 bits
	// 0 = aggressive, 1 = normal, 2 = light, 3 = none
	bit1 := d.rangeDecoder.DecodeBit(5)
	if bit1 == 0 {
		return 2 // Light spread (default)
	}
	bit2 := d.rangeDecoder.DecodeBit(1)
	if bit2 == 0 {
		return 1 // Normal spread
	}
	bit3 := d.rangeDecoder.DecodeBit(1)
	if bit3 == 0 {
		return 0 // Aggressive spread
	}
	return 3 // No spread
}

// decodeSilenceFrame returns zeros for a silence frame.
func (d *Decoder) decodeSilenceFrame(frameSize int) []float64 {
	n := frameSize * d.channels
	samples := make([]float64, n)

	// Apply de-emphasis to zeros to maintain filter state
	d.applyDeemphasis(samples)

	return samples
}

// applyDeemphasis applies the de-emphasis filter for natural sound.
// CELT uses pre-emphasis during encoding; this reverses it.
// The filter is: y[n] = x[n] + PreemphCoef * y[n-1]
//
// This is a first-order IIR filter that boosts low frequencies,
// countering the high-frequency boost from pre-emphasis.
func (d *Decoder) applyDeemphasis(samples []float64) {
	if len(samples) == 0 {
		return
	}

	if d.channels == 1 {
		// Mono de-emphasis
		state := d.preemphState[0]
		for i := range samples {
			samples[i] = samples[i] + PreemphCoef*state
			state = samples[i]
		}
		d.preemphState[0] = state
	} else {
		// Stereo de-emphasis (interleaved samples)
		stateL := d.preemphState[0]
		stateR := d.preemphState[1]

		for i := 0; i < len(samples)-1; i += 2 {
			// Left channel
			samples[i] = samples[i] + PreemphCoef*stateL
			stateL = samples[i]

			// Right channel
			samples[i+1] = samples[i+1] + PreemphCoef*stateR
			stateR = samples[i+1]
		}

		d.preemphState[0] = stateL
		d.preemphState[1] = stateR
	}
}

// DecodeFrameWithDecoder decodes a frame using a pre-initialized range decoder.
// This is useful when the range decoder is shared with other layers (e.g., SILK in hybrid mode).
func (d *Decoder) DecodeFrameWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Decode frame header flags
	silence := d.decodeSilenceFlag()
	if silence {
		return d.decodeSilenceFrame(frameSize), nil
	}

	transient := d.decodeTransientFlag(lm)
	intra := d.decodeIntraFlag()

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Decode energy
	energies := d.DecodeCoarseEnergy(nbBands, intra, lm)

	// Simple allocation for remaining bits
	totalBits := 256 - rd.Tell() // Approximate
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := ComputeAllocation(totalBits, nbBands, nil, nil, 0, -1, false, lm)

	d.DecodeFineEnergy(energies, nbBands, allocResult.FineBits)

	// Decode bands
	var coeffs []float64
	if d.channels == 1 {
		coeffs = d.DecodeBands(energies, allocResult.BandBits, nbBands, false, frameSize)
	} else {
		energiesL := energies[:nbBands]
		energiesR := energies[:nbBands]
		if len(energies) > nbBands {
			energiesR = energies[nbBands:]
		}
		coeffsL, coeffsR := d.DecodeBandsStereo(energiesL, energiesR, allocResult.BandBits, nbBands, frameSize, -1)
		_ = coeffsL
		_ = coeffsR
		// For simplicity, use mono path
		coeffs = d.DecodeBands(energies[:nbBands], allocResult.BandBits, nbBands, false, frameSize)
	}

	// Synthesis
	samples := d.Synthesize(coeffs, transient, shortBlocks)

	// De-emphasis
	d.applyDeemphasis(samples)

	// Update energy
	d.SetPrevEnergy(energies)

	return samples, nil
}

// HybridCELTStartBand is the first CELT band decoded in hybrid mode.
// Bands 0-16 are covered by SILK; CELT only decodes bands 17-21.
const HybridCELTStartBand = 17

// DecodeFrameHybrid decodes a CELT frame for hybrid mode.
// In hybrid mode, CELT only decodes bands 17-21 (frequencies above ~8kHz).
// The range decoder should already have been partially consumed by SILK.
//
// Parameters:
//   - rd: Range decoder (SILK has already consumed its portion)
//   - frameSize: Expected output samples (480 or 960 for hybrid 10ms/20ms)
//
// Returns: PCM samples as float64 slice at 48kHz
//
// Implementation approach:
// - Decode all bands as usual but zero out bands 0-16 before synthesis
// - This ensures correct operation with the existing synthesis pipeline
// - Only bands 17-21 contribute to the output (high frequencies for hybrid)
//
// Reference: RFC 6716 Section 3.2 (Hybrid mode), libopus celt/celt_decoder.c
func (d *Decoder) DecodeFrameHybrid(rd *rangecoding.Decoder, frameSize int) ([]float64, error) {
	if rd == nil {
		return nil, ErrNilDecoder
	}

	// Hybrid only supports 10ms (480) and 20ms (960) frames
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	nbBands := mode.EffBands // Full band count for this frame size
	lm := mode.LM

	// Decode frame header flags
	silence := d.decodeSilenceFlag()
	if silence {
		return d.decodeSilenceFrame(frameSize), nil
	}

	transient := d.decodeTransientFlag(lm)
	intra := d.decodeIntraFlag()

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Decode coarse energy for all bands
	// In hybrid mode, we decode all bands but only use bands >= HybridCELTStartBand
	energies := d.DecodeCoarseEnergy(nbBands, intra, lm)

	// Compute bit allocation
	totalBits := 256 - rd.Tell()
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := ComputeAllocation(totalBits, nbBands, nil, nil, 0, -1, false, lm)

	// Decode fine energy
	d.DecodeFineEnergy(energies, nbBands, allocResult.FineBits)

	// Decode bands using PVQ
	var coeffs []float64
	var coeffsL, coeffsR []float64

	if d.channels == 2 {
		energiesL := energies[:nbBands]
		energiesR := make([]float64, nbBands)
		if len(energies) > nbBands {
			copy(energiesR, energies[nbBands:])
		} else {
			copy(energiesR, energiesL)
		}
		coeffsL, coeffsR = d.DecodeBandsStereo(energiesL, energiesR, allocResult.BandBits, nbBands, frameSize, -1)
	} else {
		coeffs = d.DecodeBands(energies, allocResult.BandBits, nbBands, false, frameSize)
	}

	// Zero out bands 0-16 (SILK handles low frequencies)
	// Calculate the bin offset where band 17 starts
	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)

	if d.channels == 2 {
		// Zero lower bands for both channels
		for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
			coeffsL[i] = 0
		}
		for i := 0; i < hybridBinStart && i < len(coeffsR); i++ {
			coeffsR[i] = 0
		}
	} else {
		// Zero lower bands for mono
		for i := 0; i < hybridBinStart && i < len(coeffs); i++ {
			coeffs[i] = 0
		}
	}

	// Decode energy remainder
	d.DecodeEnergyRemainder(energies, nbBands, allocResult.RemainderBits)

	// Synthesis: IMDCT + window + overlap-add
	var samples []float64
	if d.channels == 2 {
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		samples = d.Synthesize(coeffs, transient, shortBlocks)
	}

	// Apply de-emphasis filter
	d.applyDeemphasis(samples)

	// Update energy state for next frame
	d.SetPrevEnergy(energies)

	return samples, nil
}

// decodePLC generates concealment audio for a lost CELT packet.
func (d *Decoder) decodePLC(frameSize int) ([]float64, error) {
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Get fade factor for this loss
	fadeFactor := celtPLCState.RecordLoss()

	// Generate concealment using PLC module
	// Pass decoder as both state and synthesizer (it implements both interfaces)
	samples := plc.ConcealCELT(d, d, frameSize, fadeFactor)

	return samples, nil
}

// decodePLCHybrid generates concealment for CELT in hybrid mode.
func (d *Decoder) decodePLCHybrid(frameSize int) ([]float64, error) {
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidFrameSize
	}

	// Get fade factor for this loss
	fadeFactor := celtPLCState.RecordLoss()

	// Generate concealment for hybrid bands only (17-21)
	samples := plc.ConcealCELTHybrid(d, d, frameSize, fadeFactor)

	return samples, nil
}

// CELTPLCState returns the PLC state for external access (e.g., hybrid mode).
func CELTPLCState() *plc.State {
	return celtPLCState
}
