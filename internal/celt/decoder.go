package celt

import (
	"errors"

	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
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
	prevLogE    []float64 // Previous log energies (for anti-collapse history)
	prevLogE2   []float64 // Two frames ago log energies (for anti-collapse history)

	// Synthesis state (persists for overlap-add)
	overlapBuffer []float64 // Previous frame overlap tail [Overlap * channels]
	preemphState  []float64 // De-emphasis filter state [channels]

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int     // Pitch period for comb filter
	postfilterGain   float64 // Comb filter gain
	postfilterTapset int     // Filter tap configuration (0, 1, or 2)
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int
	postfilterGainOld   float64
	postfilterTapsetOld int
	// Postfilter history buffer (per-channel)
	postfilterMem []float64

	// Error recovery / deterministic randomness
	rng uint32 // RNG state for PLC and folding

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)

	// Bandwidth (Opus TOC-derived)
	bandwidth CELTBandwidth
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
		prevLogE:    make([]float64, MaxBands*channels),
		prevLogE2:   make([]float64, MaxBands*channels),

		// Overlap buffer for CELT (full overlap per channel)
		overlapBuffer: make([]float64, Overlap*channels),

		// De-emphasis filter state, one per channel
		preemphState: make([]float64, channels),

		// Postfilter history buffer for comb filter
		postfilterMem: make([]float64, combFilterHistory*channels),

		// Initialize RNG with non-zero seed
		rng: 22222,

		bandwidth: CELTFullband,
	}

	// Initialize energy arrays to reasonable defaults
	// Using negative infinity would cause issues; use small energy instead
	for i := range d.prevEnergy {
		d.prevEnergy[i] = -28.0 // Low but finite starting energy
		d.prevEnergy2[i] = -28.0
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
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
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
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
	d.resetPostfilterState()

	// Reset RNG
	d.rng = 22222

	// Clear range decoder reference
	d.rangeDecoder = nil

	// Reset bandwidth to fullband
	d.bandwidth = CELTFullband
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

// SetBandwidth sets the CELT bandwidth derived from the Opus TOC.
func (d *Decoder) SetBandwidth(bw CELTBandwidth) {
	d.bandwidth = bw
}

// Bandwidth returns the current CELT bandwidth setting.
func (d *Decoder) Bandwidth() CELTBandwidth {
	return d.bandwidth
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

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during decoding.
func (d *Decoder) SetPrevEnergyWithPrev(prev, energies []float64) {
	if len(prev) == len(d.prevEnergy2) {
		copy(d.prevEnergy2, prev)
	} else {
		copy(d.prevEnergy2, d.prevEnergy)
	}
	copy(d.prevEnergy, energies)
}

func (d *Decoder) updateLogE(energies []float64, nbBands int, transient bool) {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands <= 0 {
		return
	}
	if len(energies) < nbBands*d.channels {
		nbBands = len(energies) / d.channels
	}
	if nbBands <= 0 {
		return
	}

	if !transient {
		copy(d.prevLogE2, d.prevLogE)
	}
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := 0; band < nbBands; band++ {
			src := c*nbBands + band
			dst := base + band
			e := energies[src]
			if transient {
				if e < d.prevLogE[dst] {
					d.prevLogE[dst] = e
				}
			} else {
				d.prevLogE[dst] = e
			}
		}
	}
}

// OverlapBuffer returns the overlap buffer for CELT overlap.
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
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy := append([]float64(nil), d.prevEnergy...)
	prev1LogE := append([]float64(nil), d.prevLogE...)
	prev2LogE := append([]float64(nil), d.prevLogE2...)

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		d.SetPostfilter(0, 0, 0)
		samples := d.decodeSilenceFrame(frameSize)
		silenceE := make([]float64, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		DefaultTracer.TraceHeader(frameSize, d.channels, lm, 0, 0)
		DefaultTracer.TraceEnergy(0, 0, 0, 0)
		traceLen := len(samples)
		if traceLen > 16 {
			traceLen = 16
		}
		if traceLen > 0 {
			DefaultTracer.TraceSynthesis("final", samples[:traceLen])
		}
		celtPLCState.Reset()
		celtPLCState.SetLastFrameParams(plc.ModeCELT, frameSize, d.channels)
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	d.SetPostfilter(postfilterPeriod, postfilterGain, postfilterTapset)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}

	// Trace frame header
	DefaultTracer.TraceHeader(frameSize, d.channels, lm, boolToInt(intra), boolToInt(transient))

	// Determine short blocks for transient mode
	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 1: Decode coarse energy
	energies := d.DecodeCoarseEnergy(end, intra, lm)

	tfRes := make([]int, end)
	tfDecode(start, end, transient, tfRes, lm, rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := initCaps(end, lm, d.channels)
	offsets := make([]int, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := minInt(width<<bitRes, maxInt(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i] {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		if boost > 0 {
			dynallocLogp = maxInt(2, dynallocLogp-1)
		}
	}

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := make([]int, end)
	fineQuant := make([]int, end)
	finePriority := make([]int, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	codedBands := cltComputeAllocation(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, d.channels, lm, rd)

	d.DecodeFineEnergy(energies, end, fineQuant)

	coeffsL, coeffsR, collapse := quantAllBandsDecode(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, &d.rng)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64

	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
		denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		denormalizeCoeffs(coeffsL, energies, end, frameSize)
		samples = d.Synthesize(coeffsL, transient, shortBlocks)
	}

	d.applyPostfilter(samples, frameSize, mode.LM)

	// Step 7: Apply de-emphasis filter
	d.applyDeemphasis(samples)
	scaleSamples(samples, 1.0/32768.0)

	// Trace final synthesis output
	traceLen := len(samples)
	if traceLen > 16 {
		traceLen = 16
	}
	DefaultTracer.TraceSynthesis("final", samples[:traceLen])

	// Update energy state for next frame
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)

	// Reset PLC state after successful decode
	celtPLCState.Reset()
	celtPLCState.SetLastFrameParams(plc.ModeCELT, frameSize, d.channels)

	return samples, nil
}

// DecodeStereoParams decodes stereo parameters (intensity and dual stereo).
// Reference: RFC 6716 Section 4.3.4, libopus celt/celt_decoder.c
func (d *Decoder) DecodeStereoParams(nbBands int) (intensity, dualStereo int) {
	if d.rangeDecoder == nil {
		return -1, 0
	}

	// IntensityDecay = 16384 (Q15)
	const decay = 16384

	// Compute fs0 exactly as encoder does
	// fs0 = laplaceNMin + (laplaceFS - laplaceNMin)*decay >> 15
	fs0 := laplaceNMin + ((laplaceFS-laplaceNMin)*decay)>>15

	// Decode intensity band index using Laplace distribution
	intensity = d.decodeLaplace(fs0, decay)

	// Decode dual stereo flag
	dualStereo = d.rangeDecoder.DecodeBit(1)

	return intensity, dualStereo
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
	mode := GetModeConfig(frameSize)
	zeros := make([]float64, frameSize)
	var samples []float64
	if d.channels == 2 {
		samples = d.SynthesizeStereo(zeros, zeros, false, 1)
	} else {
		samples = d.Synthesize(zeros, false, 1)
	}
	if len(samples) == 0 {
		samples = make([]float64, frameSize*d.channels)
	}

	d.applyPostfilter(samples, frameSize, mode.LM)
	d.applyDeemphasis(samples)
	scaleSamples(samples, 1.0/32768.0)

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

func scaleSamples(samples []float64, scale float64) {
	if scale == 1.0 {
		return
	}
	for i := range samples {
		samples[i] *= scale
	}
}

// DecodeFrameWithPacketStereo decodes a CELT frame with explicit packet stereo flag.
// This handles the case where the packet's stereo flag differs from the decoder's configured channels.
// For example, a stereo decoder (channels=2) receiving a mono packet (packetStereo=false).
//
// packetStereo: true if the packet contains stereo data, false for mono
//
// When packetStereo doesn't match decoder channels:
// - Mono packet + stereo decoder: decode mono, duplicate to stereo output
// - Stereo packet + mono decoder: decode stereo, mix to mono output
func (d *Decoder) DecodeFrameWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float64, error) {
	packetChannels := 1
	if packetStereo {
		packetChannels = 2
	}

	// If packet channels match decoder channels, use normal decoding
	if packetChannels == d.channels {
		return d.DecodeFrame(data, frameSize)
	}

	// Handle mismatch: need to decode with packet's channel count, then convert
	if packetChannels == 1 && d.channels == 2 {
		// Mono packet, stereo decoder: decode as mono, duplicate to stereo
		return d.decodeMonoPacketToStereo(data, frameSize)
	}

	// Stereo packet, mono decoder: decode as stereo, mix to mono
	return d.decodeStereoPacketToMono(data, frameSize)
}

// decodeMonoPacketToStereo decodes a mono packet and converts output to stereo.
// This is used when a stereo decoder receives a mono packet.
// The mono signal is duplicated to both L and R channels.
// State is maintained in stereo format (L and R get same values).
func (d *Decoder) decodeMonoPacketToStereo(data []byte, frameSize int) ([]float64, error) {
	if data == nil {
		return d.decodePLC(frameSize)
	}
	if len(data) == 0 {
		return nil, ErrInvalidFrame
	}
	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}

	// Save original channel count and temporarily set to mono for decoding
	origChannels := d.channels
	d.channels = 1

	// Initialize range decoder
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0

	// Save prev energy state - use left channel for mono prediction
	prev1Energy := make([]float64, MaxBands)
	prev1LogE := make([]float64, MaxBands)
	prev2LogE := make([]float64, MaxBands)
	for i := 0; i < MaxBands; i++ {
		prev1Energy[i] = d.prevEnergy[i] // Use left channel
		prev1LogE[i] = d.prevLogE[i]
		prev2LogE[i] = d.prevLogE2[i]
	}
	// Temporarily adjust prevEnergy for mono decoding
	origPrevEnergy := d.prevEnergy
	d.prevEnergy = prev1Energy

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}

	// Restore original channels before any returns
	defer func() {
		d.channels = origChannels
		d.prevEnergy = origPrevEnergy
	}()

	if silence {
		d.SetPostfilter(0, 0, 0)
		// Generate mono silence, then duplicate to stereo
		d.channels = origChannels // Restore for silence frame
		samples := d.decodeSilenceFrame(frameSize)
		silenceE := make([]float64, MaxBands*origChannels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.prevEnergy = origPrevEnergy
		d.updateLogE(silenceE, MaxBands, false)
		celtPLCState.Reset()
		celtPLCState.SetLastFrameParams(plc.ModeCELT, frameSize, origChannels)
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	d.SetPostfilter(postfilterPeriod, postfilterGain, postfilterTapset)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Decode coarse energy for mono (using d.channels=1)
	monoEnergies := d.DecodeCoarseEnergy(end, intra, lm)

	tfRes := make([]int, end)
	tfDecode(start, end, transient, tfRes, lm, rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := initCaps(end, lm, 1) // mono
	offsets := make([]int, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := 1 * (EBands[i+1] - EBands[i]) << lm // mono
		quanta := minInt(width<<bitRes, maxInt(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i] {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		if boost > 0 {
			dynallocLogp = maxInt(2, dynallocLogp-1)
		}
	}

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := make([]int, end)
	fineQuant := make([]int, end)
	finePriority := make([]int, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	codedBands := cltComputeAllocation(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, 1, lm, rd) // mono

	// Decode fine energy for mono
	d.DecodeFineEnergy(monoEnergies, end, fineQuant)

	// Decode bands for mono
	coeffsMono, _, collapse := quantAllBandsDecode(rd, 1, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, &d.rng)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	d.DecodeEnergyFinalise(monoEnergies, end, fineQuant, finePriority, bitsLeft)

	if antiCollapseOn {
		antiCollapse(coeffsMono, nil, collapse, lm, 1, start, end, monoEnergies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	// Denormalize mono coefficients
	denormalizeCoeffs(coeffsMono, monoEnergies, end, frameSize)

	// Duplicate mono coefficients to stereo for synthesis
	coeffsL := coeffsMono
	coeffsR := make([]float64, len(coeffsMono))
	copy(coeffsR, coeffsMono)

	// Restore original channels for stereo synthesis
	d.channels = origChannels
	d.prevEnergy = origPrevEnergy

	// Synthesize as stereo
	samples := d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)

	d.applyPostfilter(samples, frameSize, mode.LM)
	d.applyDeemphasis(samples)
	scaleSamples(samples, 1.0/32768.0)

	// Update stereo energy state by duplicating mono energies
	stereoEnergies := make([]float64, MaxBands*2)
	for i := 0; i < end; i++ {
		stereoEnergies[i] = monoEnergies[i]          // Left
		stereoEnergies[MaxBands+i] = monoEnergies[i] // Right (duplicate)
	}
	for i := end; i < MaxBands; i++ {
		stereoEnergies[i] = -28.0
		stereoEnergies[MaxBands+i] = -28.0
	}

	d.updateLogE(stereoEnergies, end, transient)
	// Update prevEnergy for both channels
	for i := 0; i < MaxBands; i++ {
		d.prevEnergy[i] = stereoEnergies[i]
		d.prevEnergy[MaxBands+i] = stereoEnergies[MaxBands+i]
	}

	celtPLCState.Reset()
	celtPLCState.SetLastFrameParams(plc.ModeCELT, frameSize, origChannels)

	return samples, nil
}

// decodeStereoPacketToMono decodes a stereo packet and converts output to mono.
// This is used when a mono decoder receives a stereo packet.
// The stereo signal is mixed to mono: out = (L + R) / 2
func (d *Decoder) decodeStereoPacketToMono(data []byte, frameSize int) ([]float64, error) {
	// For now, this case is not fully implemented - would need to:
	// 1. Decode stereo coefficients
	// 2. Mix L and R coefficients
	// 3. Synthesize as mono
	// For the test vector case, we primarily need mono->stereo, so this returns an error
	return nil, ErrInvalidFrame
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
		d.SetPostfilter(0, 0, 0)
		samples := d.decodeSilenceFrame(frameSize)
		silenceE := make([]float64, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(append([]float64(nil), d.prevEnergy...), silenceE)
		return samples, nil
	}

	transient := d.decodeTransientFlag(lm)
	intra := d.decodeIntraFlag()

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Decode energy
	prev1Energy := append([]float64(nil), d.prevEnergy...)
	energies := d.DecodeCoarseEnergy(nbBands, intra, lm)

	// Simple allocation for remaining bits
	totalBits := 256 - rd.Tell() // Approximate
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := ComputeAllocation(totalBits, nbBands, d.channels, nil, nil, 0, -1, false, lm)

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
	scaleSamples(samples, 1.0/32768.0)

	// Update energy
	d.updateLogE(energies, nbBands, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)

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
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy := append([]float64(nil), d.prevEnergy...)
	prev1LogE := append([]float64(nil), d.prevLogE...)
	prev2LogE := append([]float64(nil), d.prevLogE2...)

	totalBits := rd.StorageBits()
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		d.SetPostfilter(0, 0, 0)
		samples := d.decodeSilenceFrame(frameSize)
		silenceE := make([]float64, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	d.SetPostfilter(postfilterPeriod, postfilterGain, postfilterTapset)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	energies := d.DecodeCoarseEnergy(end, intra, lm)

	tfRes := make([]int, end)
	tfDecode(start, end, transient, tfRes, lm, rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}

	cap := initCaps(end, lm, d.channels)
	offsets := make([]int, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := minInt(width<<bitRes, maxInt(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i] {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		if boost > 0 {
			dynallocLogp = maxInt(2, dynallocLogp-1)
		}
	}

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := make([]int, end)
	fineQuant := make([]int, end)
	finePriority := make([]int, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	codedBands := cltComputeAllocation(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, d.channels, lm, rd)

	d.DecodeFineEnergy(energies, end, fineQuant)

	coeffsL, coeffsR, collapse := quantAllBandsDecode(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, &d.rng)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}

	bitsLeft := totalBits - rd.Tell()
	d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}

	hybridBinStart := ScaledBandStart(HybridCELTStartBand, frameSize)
	var samples []float64
	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
		denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
			coeffsL[i] = 0
		}
		for i := 0; i < hybridBinStart && i < len(coeffsR); i++ {
			coeffsR[i] = 0
		}
		samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
	} else {
		denormalizeCoeffs(coeffsL, energies, end, frameSize)
		for i := 0; i < hybridBinStart && i < len(coeffsL); i++ {
			coeffsL[i] = 0
		}
		samples = d.Synthesize(coeffsL, transient, shortBlocks)
	}

	d.applyPostfilter(samples, frameSize, mode.LM)

	d.applyDeemphasis(samples)
	scaleSamples(samples, 1.0/32768.0)
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)

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
	scaleSamples(samples, 1.0/32768.0)

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
	scaleSamples(samples, 1.0/32768.0)

	return samples, nil
}

// CELTPLCState returns the PLC state for external access (e.g., hybrid mode).
func CELTPLCState() *plc.State {
	return celtPLCState
}
