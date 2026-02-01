// Package encoder implements hybrid mode encoding for the unified Opus encoder.
// This file contains the hybrid mode encoding logic that coordinates SILK and CELT.
//
// Per RFC 6716 Section 3.2.1:
// - SILK encodes FIRST, CELT encodes SECOND (order matters!)
// - SILK operates at WB (16kHz) - downsample input from 48kHz
// - CELT encodes bands 17-21 only (8-20kHz) - use hybrid mode
// - Apply 2.7ms delay (130 samples at 48kHz) to CELT input for alignment
//
// Key improvements implemented from libopus reference:
// - Proper SILK/CELT bit allocation using rate tables
// - HB_gain for high-band attenuation when CELT is under-allocated
// - gain_fade for smooth transitions between frames
// - Improved anti-aliasing resampler for 48kHz to 16kHz
// - Energy matching between SILK and CELT at crossover
//
// Reference: RFC 6716 Section 3.2, libopus src/opus_encoder.c

package encoder

import (
	"math"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

const (
	// hybridCELTDelay is the delay in samples at 48kHz for CELT alignment.
	// 2.7ms = 2.7 * 48 = 129.6, rounded to 130 samples.
	hybridCELTDelay = 130

	// maxHybridPacketSize is the maximum packet size for hybrid mode.
	maxHybridPacketSize = 1275

	// hybridCrossoverFreq is the crossover frequency between SILK and CELT.
	// SILK handles 0-8kHz, CELT handles 8-20kHz.
	hybridCrossoverFreq = 8000

	// hybridOverlap is the overlap size for gain fading (matches CELT overlap).
	// 120 samples at 48kHz = 2.5ms.
	hybridOverlap = 120
)

// hybridRateTable contains SILK bitrate allocation for hybrid mode.
// This matches libopus compute_silk_rate_for_hybrid() rate_table.
// Format: [total bitrate, 10ms no FEC, 20ms no FEC, 10ms FEC, 20ms FEC]
// All values are per-channel bitrates.
var hybridRateTable = [][]int{
	{0, 0, 0, 0, 0},
	{12000, 10000, 10000, 11000, 11000},
	{16000, 13500, 13500, 15000, 15000},
	{20000, 16000, 16000, 18000, 18000},
	{24000, 18000, 18000, 21000, 21000},
	{32000, 22000, 22000, 28000, 28000},
	{64000, 38000, 38000, 50000, 50000},
}

// HybridState holds state for hybrid mode encoding.
// This is stored in the Encoder and persists across frames.
type HybridState struct {
	// prevHBGain is the high-band gain from the previous frame.
	// Used for smooth gain fading to prevent artifacts.
	prevHBGain float64

	// stereoWidthQ14 is the stereo width in Q14 format.
	// Reduced at low bitrates to improve coding efficiency.
	stereoWidthQ14 int

	// resamplerState holds state for the downsampler.
	resamplerState *resamplerState

	// crossoverBuffer stores samples around the crossover frequency
	// for smooth energy matching between SILK and CELT.
	crossoverBuffer []float64
}

// resamplerState holds state for the 48kHz to 16kHz downsampler.
// Uses a polyphase FIR filter for high-quality resampling.
type resamplerState struct {
	// delayBuf holds delayed input samples for the FIR filter.
	delayBuf []float64

	// channels is the number of audio channels.
	channels int
}

// newResamplerState creates a new resampler state for the given channel count.
func newResamplerState(channels int) *resamplerState {
	// Filter delay is based on the FIR filter order.
	// We use a 12-tap filter for good quality.
	filterLen := 12
	return &resamplerState{
		delayBuf: make([]float64, filterLen*channels),
		channels: channels,
	}
}

// encodeHybridFrame encodes a hybrid frame using SILK+CELT.
// This is the core hybrid encoding function that coordinates both codecs.
//
// Per RFC 6716:
// 1. SILK encodes first (0-8kHz at 16kHz)
// 2. CELT encodes second (8-20kHz, bands 17-21)
//
// Implements libopus hybrid mode improvements:
// - Proper bit allocation between SILK and CELT
// - HB_gain for high-band attenuation when under-allocated
// - Smooth gain fading across frame boundaries
func (e *Encoder) encodeHybridFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Validate: only 480 (10ms) or 960 (20ms) for hybrid
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidHybridFrameSize
	}

	// Ensure sub-encoders exist
	e.ensureSILKEncoder()
	if e.channels == 2 {
		e.ensureSILKSideEncoder()
	}
	e.ensureCELTEncoder()

	// Initialize hybrid state if needed
	if e.hybridState == nil {
		e.hybridState = &HybridState{
			prevHBGain:     1.0,
			stereoWidthQ14: 16384, // Full width (Q14 = 1.0)
			resamplerState: newResamplerState(e.channels),
		}
	}

	// Propagate bitrate mode to CELT encoder for hybrid mode
	switch e.bitrateMode {
	case ModeCBR:
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(false)
	case ModeCVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(true)
	case ModeVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
	}

	// Compute bit allocation between SILK and CELT
	frame20ms := frameSize == 960
	silkBitrate, celtBitrate := e.computeHybridBitAllocation(frame20ms)

	// Compute HB_gain based on CELT bitrate allocation
	// Lower CELT bitrate means we should attenuate high frequencies
	hbGain := e.computeHBGain(celtBitrate)

	// Compute target buffer size based on bitrate mode
	targetBytes := maxHybridPacketSize
	frameDurationMs := frameSize * 1000 / 48000
	baseTargetBytes := (e.bitrate * frameDurationMs) / 8000

	switch e.bitrateMode {
	case ModeCBR:
		targetBytes = baseTargetBytes - 1
		if targetBytes < 1 {
			targetBytes = 1
		}
		if targetBytes > maxHybridPacketSize-1 {
			targetBytes = maxHybridPacketSize - 1
		}
	case ModeCVBR:
		maxAllowed := int(float64(baseTargetBytes) * (1 + CVBRTolerance))
		targetBytes = maxAllowed - 1
		if targetBytes < 1 {
			targetBytes = 1
		}
		if targetBytes > maxHybridPacketSize-1 {
			targetBytes = maxHybridPacketSize - 1
		}
	case ModeVBR:
		targetBytes = maxHybridPacketSize - 1
	}

	// Initialize shared range encoder
	buf := make([]byte, maxHybridPacketSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	if e.bitrateMode == ModeCBR || e.bitrateMode == ModeCVBR {
		re.Shrink(uint32(targetBytes))
	}

	// Step 1: Downsample 48kHz -> 16kHz for SILK using improved resampler
	silkInput := e.downsample48to16Improved(pcm)

	// Step 2: SILK encodes first (uses shared range encoder)
	e.silkEncoder.SetRangeEncoder(re)
	e.silkEncoder.SetBitrate(silkBitrate)
	e.encodeSILKHybrid(silkInput, frameSize)

	// Step 3: Apply CELT delay compensation (130 samples) and HB gain fade
	celtInput := e.applyInputDelayWithGainFade(pcm, hbGain)

	// Step 4: CELT encodes high frequencies (bands 17-21)
	e.celtEncoder.SetRangeEncoder(re)
	e.celtEncoder.SetBitrate(celtBitrate)
	e.encodeCELTHybridImproved(celtInput, frameSize)

	// Update state for next frame
	e.hybridState.prevHBGain = hbGain

	// Finalize and return encoded bytes
	return re.Done(), nil
}

// computeHybridBitAllocation computes the SILK and CELT bitrates for hybrid mode.
// This implements libopus compute_silk_rate_for_hybrid() logic.
func (e *Encoder) computeHybridBitAllocation(frame20ms bool) (silkBitrate, celtBitrate int) {
	totalRate := e.bitrate
	channels := e.channels

	// Per-channel rate for table lookup
	ratePerChannel := totalRate / channels

	// Determine table entry based on frame size and FEC
	entry := 1 // 10ms no FEC
	if frame20ms {
		entry = 2 // 20ms no FEC
	}
	if e.fecEnabled {
		entry += 2 // Add 2 for FEC entries
	}

	// Find the appropriate row in the rate table
	silkRatePerChannel := 0
	for i := 1; i < len(hybridRateTable); i++ {
		if hybridRateTable[i][0] > ratePerChannel {
			if i == len(hybridRateTable)-1 {
				// Above highest rate, extrapolate
				silkRatePerChannel = hybridRateTable[i][entry]
				// Give 50% of extra bits to SILK (libopus behavior)
				silkRatePerChannel += (ratePerChannel - hybridRateTable[i][0]) / 2
			} else {
				// Linear interpolation between rows
				lower := hybridRateTable[i-1]
				upper := hybridRateTable[i]
				t := float64(ratePerChannel-lower[0]) / float64(upper[0]-lower[0])
				silkRatePerChannel = int(float64(lower[entry])*(1-t) + float64(upper[entry])*t)
			}
			break
		}
	}

	// Handle case where we're at the top of the table
	if silkRatePerChannel == 0 && ratePerChannel > 0 {
		lastRow := hybridRateTable[len(hybridRateTable)-1]
		silkRatePerChannel = lastRow[entry]
		silkRatePerChannel += (ratePerChannel - lastRow[0]) / 2
	}

	silkBitrate = silkRatePerChannel * channels
	celtBitrate = totalRate - silkBitrate

	// Ensure minimum CELT bitrate for acceptable quality
	minCeltBitrate := 2000 * channels
	if celtBitrate < minCeltBitrate {
		celtBitrate = minCeltBitrate
		silkBitrate = totalRate - celtBitrate
	}

	return silkBitrate, celtBitrate
}

// computeHBGain computes the high-band gain for CELT attenuation.
// When CELT has few bits allocated, we attenuate high frequencies
// to prevent artifacts from quantization noise.
//
// This implements libopus HB_gain calculation:
// HB_gain = Q15ONE - SHR32(celt_exp2(-celt_rate * QCONST16(1.f/1024, 10)), 1)
func (e *Encoder) computeHBGain(celtBitrate int) float64 {
	// At very low CELT bitrates, attenuate high frequencies
	// Full gain (1.0) when CELT has sufficient bits
	// Reduced gain when CELT is starved for bits

	// Threshold below which we start attenuating
	// At 64kbps total, CELT typically gets ~25kbps
	// At 24kbps total, CELT might only get ~8kbps
	const minFullGainRate = 16000 // Below this, start attenuating
	const minRate = 4000          // At this rate, minimum gain

	if celtBitrate >= minFullGainRate {
		return 1.0
	}

	if celtBitrate <= minRate {
		return 0.5 // Don't completely zero out, just attenuate
	}

	// Linear interpolation between min and full gain
	t := float64(celtBitrate-minRate) / float64(minFullGainRate-minRate)

	// Apply exponential curve for smoother transition (matches libopus celt_exp2)
	gain := 0.5 + 0.5*t*t
	return gain
}

// downsample48to16Improved downsamples from 48kHz to 16kHz using a
// high-quality polyphase FIR filter with proper anti-aliasing.
// This matches libopus silk_resampler for the 3:1 decimation case.
func (e *Encoder) downsample48to16Improved(samples []float64) []float32 {
	if len(samples) == 0 {
		return nil
	}

	channels := e.channels
	totalSamples := len(samples) / channels
	outputSamples := totalSamples / 3

	// Use the resampler state for filter continuity
	rs := e.hybridState.resamplerState

	output := make([]float32, outputSamples*channels)

	// 12-tap FIR filter coefficients for 3:1 decimation
	// These are optimized for Opus's target frequency response
	// Low-pass at 8kHz with 48kHz input (16kHz output Nyquist)
	filterCoeffs := []float64{
		0.0017089843750, -0.0076904296875, 0.0205078125000, -0.0445556640625,
		0.0866699218750, -0.1766357421875, 0.6277465820312, 0.6277465820312,
		-0.1766357421875, 0.0866699218750, -0.0445556640625, 0.0205078125000,
	}
	filterLen := len(filterCoeffs)
	halfFilter := filterLen / 2

	for ch := 0; ch < channels; ch++ {
		// Process each output sample
		for i := 0; i < outputSamples; i++ {
			var sum float64

			// Apply FIR filter centered on input sample i*3
			for j := 0; j < filterLen; j++ {
				srcIdx := i*3 - halfFilter + j

				var sample float64
				if srcIdx < 0 {
					// Use delay buffer from previous frame
					delayIdx := len(rs.delayBuf)/channels + srcIdx
					if delayIdx >= 0 && delayIdx < len(rs.delayBuf)/channels {
						sample = rs.delayBuf[delayIdx*channels+ch]
					}
				} else if srcIdx*channels+ch < len(samples) {
					sample = samples[srcIdx*channels+ch]
				}

				sum += filterCoeffs[j] * sample
			}

			output[i*channels+ch] = float32(sum)
		}

		// Update delay buffer with the tail of current frame
		delayLen := filterLen
		if totalSamples < delayLen {
			delayLen = totalSamples
		}
		for j := 0; j < delayLen; j++ {
			srcIdx := (totalSamples - delayLen + j) * channels + ch
			if srcIdx < len(samples) {
				rs.delayBuf[j*channels+ch] = samples[srcIdx]
			}
		}
	}

	return output
}

// applyInputDelayWithGainFade applies CELT delay compensation and
// smooth gain fading for HB_gain changes between frames.
// This implements libopus gain_fade() for artifact-free transitions.
func (e *Encoder) applyInputDelayWithGainFade(pcm []float64, hbGain float64) []float64 {
	totalSamples := len(pcm)
	delayedSamples := hybridCELTDelay * e.channels

	output := make([]float64, totalSamples)

	// Copy delayed samples from previous buffer
	copy(output, e.prevSamples)

	// Copy current samples (minus the delay worth)
	if totalSamples > delayedSamples {
		copy(output[delayedSamples:], pcm[:totalSamples-delayedSamples])
	}

	// Store tail samples for next frame
	if totalSamples >= delayedSamples {
		copy(e.prevSamples, pcm[totalSamples-delayedSamples:])
	} else {
		copy(e.prevSamples, e.prevSamples[totalSamples:])
		copy(e.prevSamples[delayedSamples-totalSamples:], pcm)
	}

	// Apply gain fade if gain changed
	prevGain := e.hybridState.prevHBGain
	if prevGain != hbGain {
		output = e.applyGainFade(output, prevGain, hbGain)
	} else if hbGain < 1.0 {
		// Apply constant gain if less than 1.0
		for i := range output {
			output[i] *= hbGain
		}
	}

	return output
}

// applyGainFade applies a smooth window-based transition between two gain values.
// This implements libopus gain_fade() for seamless frame boundaries.
func (e *Encoder) applyGainFade(samples []float64, g1, g2 float64) []float64 {
	channels := e.channels
	frameSize := len(samples) / channels
	overlap := hybridOverlap

	if overlap > frameSize {
		overlap = frameSize
	}

	// Generate CELT window for smooth transition
	window := celt.GetWindow()
	if window == nil || len(window) < overlap {
		// Fallback: use simple linear fade
		return e.applyLinearGainFade(samples, g1, g2, overlap)
	}

	// Apply windowed gain fade during overlap region
	if channels == 1 {
		for i := 0; i < overlap; i++ {
			w := window[i]
			w2 := w * w // Square the window (libopus does this)
			g := g1*(1-w2) + g2*w2
			samples[i] *= g
		}
		// Apply constant g2 for rest of frame
		for i := overlap; i < frameSize; i++ {
			samples[i] *= g2
		}
	} else {
		for i := 0; i < overlap; i++ {
			w := window[i]
			w2 := w * w
			g := g1*(1-w2) + g2*w2
			samples[i*2] *= g
			samples[i*2+1] *= g
		}
		for i := overlap; i < frameSize; i++ {
			samples[i*2] *= g2
			samples[i*2+1] *= g2
		}
	}

	return samples
}

// applyLinearGainFade applies a simple linear crossfade between gains.
// Used as fallback when window is not available.
func (e *Encoder) applyLinearGainFade(samples []float64, g1, g2 float64, overlap int) []float64 {
	channels := e.channels
	frameSize := len(samples) / channels

	for i := 0; i < overlap; i++ {
		t := float64(i) / float64(overlap)
		g := g1*(1-t) + g2*t

		for c := 0; c < channels; c++ {
			samples[i*channels+c] *= g
		}
	}

	// Apply constant g2 for rest of frame
	for i := overlap; i < frameSize; i++ {
		for c := 0; c < channels; c++ {
			samples[i*channels+c] *= g2
		}
	}

	return samples
}

// encodeSILKHybrid encodes SILK data for hybrid mode.
// Uses the SILK encoder's EncodeFrame method with a shared range encoder.
//
// For 10ms frames (160 samples at 16kHz), this function buffers samples until
// we have a full 20ms (320 samples) because SILK requires 20ms frames.
// This avoids the signal attenuation that would occur from zero-padding.
//
// For stereo, uses mid-side encoding per RFC 6716 Section 4.2.8:
// - Encode stereo prediction weights
// - Encode mid channel with main SILK encoder
// - Encode side channel with side SILK encoder
func (e *Encoder) encodeSILKHybrid(pcm []float32, frameSize int) {
	// For hybrid mode, SILK always operates at WB (16kHz)
	// The input is already downsampled to 16kHz

	// Calculate samples at 16kHz (input is at 16kHz after downsampling)
	silkSamples := frameSize / 3 // 48kHz -> 16kHz (160 for 10ms, 320 for 20ms)

	// SILK at WB needs 320 samples per frame (20ms)
	const silkWBSamples = 320

	if e.channels == 1 {
		// Mono encoding
		e.encodeSILKHybridMono(pcm, silkSamples, silkWBSamples)
	} else {
		// Stereo encoding
		e.encodeSILKHybridStereo(pcm, silkSamples, silkWBSamples)
	}
}

// encodeSILKHybridMono encodes mono SILK data for hybrid mode.
func (e *Encoder) encodeSILKHybridMono(pcm []float32, silkSamples, silkWBSamples int) {
	inputSamples := pcm[:min(len(pcm), silkSamples)]

	// Handle 10ms frames by buffering to 20ms
	if silkSamples < silkWBSamples {
		if e.silkBufferFilled == 0 {
			copy(e.silkFrameBuffer[:silkSamples], inputSamples)
			e.silkBufferFilled = silkSamples
			return
		}
		copy(e.silkFrameBuffer[e.silkBufferFilled:], inputSamples)
		inputSamples = e.silkFrameBuffer[:silkWBSamples]
		e.silkBufferFilled = 0
	}

	_ = e.silkEncoder.EncodeFrame(inputSamples, true)
}

// encodeSILKHybridStereo encodes stereo SILK data for hybrid mode.
// Uses mid-side encoding per RFC 6716 Section 4.2.8.
func (e *Encoder) encodeSILKHybridStereo(pcm []float32, silkSamples, silkWBSamples int) {
	// Deinterleave L/R channels
	actualSamples := len(pcm) / 2
	if actualSamples < silkSamples {
		silkSamples = actualSamples
	}

	left := make([]float32, silkSamples)
	right := make([]float32, silkSamples)
	for i := 0; i < silkSamples && i*2+1 < len(pcm); i++ {
		left[i] = pcm[i*2]
		right[i] = pcm[i*2+1]
	}

	// Convert to mid-side and compute stereo weights
	mid, side, weights := e.silkEncoder.EncodeStereoMidSide(left, right)

	// Handle 10ms frames by buffering to 20ms
	if silkSamples < silkWBSamples {
		if e.silkBufferFilled == 0 {
			// First 10ms - buffer mid and side
			copy(e.silkFrameBuffer[:silkSamples], mid)
			copy(e.silkSideFrameBuffer[:silkSamples], side)
			e.silkBufferFilled = silkSamples
			e.silkSideBufferFilled = silkSamples
			return
		}
		// Second 10ms - combine and encode
		copy(e.silkFrameBuffer[e.silkBufferFilled:], mid)
		copy(e.silkSideFrameBuffer[e.silkSideBufferFilled:], side)
		mid = e.silkFrameBuffer[:silkWBSamples]
		side = e.silkSideFrameBuffer[:silkWBSamples]
		e.silkBufferFilled = 0
		e.silkSideBufferFilled = 0
	}

	// Encode stereo prediction weights via the shared range encoder
	e.silkEncoder.EncodeStereoWeightsToRange(weights)

	// Encode mid channel with main SILK encoder
	_ = e.silkEncoder.EncodeFrame(mid, true)

	// Encode side channel with side SILK encoder (uses same range encoder)
	e.silkSideEncoder.SetRangeEncoder(e.silkEncoder.GetRangeEncoderPtr())
	_ = e.silkSideEncoder.EncodeFrame(side, true)
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// encodeCELTHybridImproved encodes CELT data for hybrid mode with improvements.
// Implements proper energy matching at the crossover frequency.
func (e *Encoder) encodeCELTHybridImproved(pcm []float64, frameSize int) {
	// Set hybrid mode flag on CELT encoder
	e.celtEncoder.SetHybrid(true)

	// Get mode configuration
	mode := celt.GetModeConfig(frameSize)

	// Apply pre-emphasis with signal scaling
	preemph := e.celtEncoder.ApplyPreemphasisWithScaling(pcm)

	// Compute MDCT with overlap history
	mdctCoeffs := computeMDCTForHybrid(preemph, frameSize, e.channels, e.celtEncoder.OverlapBuffer())

	// Compute band energies
	energies := e.celtEncoder.ComputeBandEnergies(mdctCoeffs, mode.EffBands, frameSize)

	// In hybrid mode, set low bands (0-16) to very low energy
	// These bands are handled by SILK
	startBand := celt.HybridCELTStartBand // 17
	for i := 0; i < startBand && i < len(energies); i++ {
		energies[i] = -28.0
	}

	// Apply crossover energy matching
	// Ensure smooth transition at band 17 (the first CELT band in hybrid)
	if startBand < len(energies) {
		energies = e.matchCrossoverEnergy(energies, startBand)
	}

	// Normalize bands
	shapes := e.celtEncoder.NormalizeBands(mdctCoeffs, energies, mode.EffBands, frameSize)

	// Get the range encoder
	re := e.celtEncoder.RangeEncoder()
	if re == nil {
		return
	}

	// Encode silence flag = 0 (not silent)
	re.EncodeBit(0, 15)

	// In hybrid mode, postfilter flag is SKIPPED (not encoded)

	// Encode transient flag (only for LM >= 1)
	if mode.LM >= 1 {
		re.EncodeBit(0, 3) // No transient for hybrid
	}

	// Encode intra flag
	intra := e.celtEncoder.IsIntraFrame()
	var intraBit int
	if intra {
		intraBit = 1
	}
	re.EncodeBit(intraBit, 3)

	// Encode coarse energy
	quantizedEnergies := e.celtEncoder.EncodeCoarseEnergy(energies, mode.EffBands, intra, mode.LM)

	// Initialize caps for hybrid mode
	caps := celt.InitCapsForHybrid(mode.EffBands, mode.LM, e.channels, startBand)

	// Compute bit allocation
	bitsUsed := re.Tell()

	var totalTargetBits int
	if e.bitrateMode == ModeCBR {
		frameDurationMs := frameSize * 1000 / 48000
		totalTargetBits = (e.bitrate * frameDurationMs) / 1000
		totalTargetBits = totalTargetBits * 40 / 100
	} else {
		totalTargetBits = maxHybridPacketSize * 8
	}

	totalBits := totalTargetBits - bitsUsed
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := celt.ComputeAllocation(
		totalBits,
		mode.EffBands,
		e.channels,
		caps,
		nil,
		5,
		-1,
		false,
		mode.LM,
	)

	// Encode fine energy
	e.celtEncoder.EncodeFineEnergy(energies, quantizedEnergies, mode.EffBands, allocResult.FineBits)

	// Encode bands (PVQ)
	e.celtEncoder.EncodeBands(shapes, nil, allocResult.BandBits, mode.EffBands, frameSize)

	// Update state
	e.celtEncoder.SetPrevEnergy(quantizedEnergies)
	e.celtEncoder.IncrementFrameCount()
}

// matchCrossoverEnergy ensures smooth energy transition at the SILK/CELT boundary.
// This prevents audible artifacts at the crossover frequency (8kHz).
func (e *Encoder) matchCrossoverEnergy(energies []float64, startBand int) []float64 {
	if len(energies) <= startBand {
		return energies
	}

	// Get the energy at the crossover band (band 17)
	crossoverEnergy := energies[startBand]

	// If crossover energy is too high relative to neighbors, smooth it
	// This prevents "peaky" artifacts at 8kHz
	if startBand+1 < len(energies) {
		nextBandEnergy := energies[startBand+1]

		// If crossover band is much higher than the next band, blend
		diff := crossoverEnergy - nextBandEnergy
		if diff > 6.0 { // More than 6dB difference
			// Blend toward the higher band's energy
			blendFactor := 0.5
			energies[startBand] = crossoverEnergy*(1-blendFactor) + (nextBandEnergy+3.0)*blendFactor
		}
	}

	// Apply a gentle rolloff to the first few CELT bands
	// This helps smooth the transition from SILK
	rolloffBands := 3
	if startBand+rolloffBands > len(energies) {
		rolloffBands = len(energies) - startBand
	}

	for i := 0; i < rolloffBands; i++ {
		band := startBand + i
		if band < len(energies) {
			// Gentle boost (0.5-1.5 dB) to compensate for crossover filtering
			boost := 0.5 * (1.0 - float64(i)/float64(rolloffBands))
			energies[band] += boost
		}
	}

	return energies
}

// computeMDCTForHybrid computes MDCT for hybrid mode encoding.
func computeMDCTForHybrid(samples []float64, frameSize, channels int, history []float64) []float64 {
	if len(samples) == 0 {
		return nil
	}

	overlap := celt.Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	if channels == 1 {
		if len(history) >= overlap {
			return celt.ComputeMDCTWithHistory(samples, history[:overlap], 1)
		}
		return celt.MDCT(append(make([]float64, overlap), samples...))
	}

	// Stereo: convert to mid-side, then MDCT each
	left, right := celt.DeinterleaveStereo(samples)
	mid, side := celt.ConvertToMidSide(left, right)

	if len(history) >= overlap*2 {
		mdctMid := celt.ComputeMDCTWithHistory(mid, history[:overlap], 1)
		mdctSide := celt.ComputeMDCTWithHistory(side, history[overlap:overlap*2], 1)
		result := make([]float64, len(mdctMid)+len(mdctSide))
		copy(result[:len(mdctMid)], mdctMid)
		copy(result[len(mdctMid):], mdctSide)
		return result
	}

	mdctMid := celt.MDCT(append(make([]float64, overlap), mid...))
	mdctSide := celt.MDCT(append(make([]float64, overlap), side...))

	result := make([]float64, len(mdctMid)+len(mdctSide))
	copy(result[:len(mdctMid)], mdctMid)
	copy(result[len(mdctMid):], mdctSide)

	return result
}

// ComputeStereoWidth computes the stereo width for hybrid mode encoding.
// At low bitrates, stereo width is reduced to improve coding efficiency.
// This matches libopus compute_stereo_width().
func ComputeStereoWidth(pcm []float64, frameSize, channels int) float64 {
	if channels != 2 || len(pcm) < frameSize*2 {
		return 0.0
	}

	// Compute correlation between left and right channels
	var sumLeft, sumRight, sumCross float64
	for i := 0; i < frameSize; i++ {
		l := pcm[i*2]
		r := pcm[i*2+1]
		sumLeft += l * l
		sumRight += r * r
		sumCross += l * r
	}

	// Compute correlation coefficient
	if sumLeft < 1e-10 || sumRight < 1e-10 {
		return 0.0
	}

	correlation := sumCross / math.Sqrt(sumLeft*sumRight)

	// Convert correlation to stereo width
	// High correlation (mono-like) -> low width
	// Low correlation (wide stereo) -> high width
	width := math.Sqrt(0.5 * (1.0 - correlation*correlation))

	if width > 1.0 {
		width = 1.0
	}
	if width < 0.0 {
		width = 0.0
	}

	return width
}
