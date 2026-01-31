// Package encoder implements hybrid mode encoding for the unified Opus encoder.
// This file contains the hybrid mode encoding logic that coordinates SILK and CELT.
//
// Per RFC 6716 Section 3.2.1:
// - SILK encodes FIRST, CELT encodes SECOND (order matters!)
// - SILK operates at WB (16kHz) - downsample input from 48kHz
// - CELT encodes bands 17-21 only (8-20kHz) - use hybrid mode
// - Apply 2.7ms delay (130 samples at 48kHz) to CELT input for alignment
//
// Reference: RFC 6716 Section 3.2

package encoder

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	// hybridCELTDelay is the delay in samples at 48kHz for CELT alignment.
	// 2.7ms = 2.7 * 48 = 129.6, rounded to 130 samples.
	hybridCELTDelay = 130

	// maxHybridPacketSize is the maximum packet size for hybrid mode.
	maxHybridPacketSize = 1275
)

// encodeHybridFrame encodes a hybrid frame using SILK+CELT.
// This is the core hybrid encoding function that coordinates both codecs.
//
// Per RFC 6716:
// 1. SILK encodes first (0-8kHz at 16kHz)
// 2. CELT encodes second (8-20kHz, bands 17-21)
//
// For v1, we use separate range encoders and concatenate the results.
// This produces valid hybrid output that can be decoded.
func (e *Encoder) encodeHybridFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Validate: only 480 (10ms) or 960 (20ms) for hybrid
	if frameSize != 480 && frameSize != 960 {
		return nil, ErrInvalidHybridFrameSize
	}

	// Ensure sub-encoders exist
	e.ensureSILKEncoder()
	e.ensureCELTEncoder()

	// Initialize shared range encoder
	buf := make([]byte, maxHybridPacketSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Step 1: Downsample 48kHz -> 16kHz for SILK
	silkInput := downsample48to16(pcm, e.channels)

	// Step 2: SILK encodes first (uses shared range encoder)
	// SILK in hybrid mode always uses WB (16kHz)
	e.silkEncoder.SetRangeEncoder(re)
	e.encodeSILKHybrid(silkInput, frameSize)

	// Step 3: Apply CELT delay compensation (130 samples)
	celtInput := e.applyInputDelay(pcm)

	// Step 4: CELT encodes high frequencies (bands 17-21)
	e.celtEncoder.SetRangeEncoder(re)
	e.encodeCELTHybrid(celtInput, frameSize)

	// Finalize and return encoded bytes
	return re.Done(), nil
}

// downsample48to16 downsamples from 48kHz to 16kHz (3:1 decimation).
// Uses a simple anti-aliasing filter with averaging.
func downsample48to16(samples []float64, channels int) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Input is at 48kHz, output at 16kHz (divide by 3)
	totalSamples := len(samples) / channels
	outputSamples := totalSamples / 3
	output := make([]float32, outputSamples*channels)

	for ch := 0; ch < channels; ch++ {
		for i := 0; i < outputSamples; i++ {
			// Simple 3-tap averaging filter for anti-aliasing
			var sum float64
			for j := 0; j < 3; j++ {
				srcIdx := (i*3+j)*channels + ch
				if srcIdx < len(samples) {
					sum += samples[srcIdx]
				}
			}
			output[i*channels+ch] = float32(sum / 3.0)
		}
	}

	return output
}

// applyInputDelay applies the CELT delay compensation to align with SILK.
// The delay is 130 samples at 48kHz (2.7ms).
func (e *Encoder) applyInputDelay(pcm []float64) []float64 {
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
		// Shift previous samples and append current
		copy(e.prevSamples, e.prevSamples[totalSamples:])
		copy(e.prevSamples[delayedSamples-totalSamples:], pcm)
	}

	return output
}

// encodeSILKHybrid encodes SILK data for hybrid mode.
// Uses the SILK encoder's EncodeFrame method with a shared range encoder.
//
// For 10ms frames (160 samples at 16kHz), this function buffers samples until
// we have a full 20ms (320 samples) because SILK requires 20ms frames.
// This avoids the signal attenuation that would occur from zero-padding.
func (e *Encoder) encodeSILKHybrid(pcm []float32, frameSize int) {
	// For hybrid mode, SILK always operates at WB (16kHz)
	// The input is already downsampled to 16kHz

	// Calculate samples at 16kHz (input is at 16kHz after downsampling)
	silkSamples := frameSize / 3 // 48kHz -> 16kHz (160 for 10ms, 320 for 20ms)

	// SILK at WB needs 320 samples per frame (20ms)
	const silkWBSamples = 320

	// Extract mono signal for SILK encoding
	var inputSamples []float32
	if e.channels == 1 {
		inputSamples = pcm[:min(len(pcm), silkSamples)]
	} else {
		// For stereo, compute mid channel: (L + R) / 2
		actualSamples := len(pcm) / 2
		if actualSamples < silkSamples {
			silkSamples = actualSamples
		}
		inputSamples = make([]float32, silkSamples)
		for i := 0; i < silkSamples && i*2+1 < len(pcm); i++ {
			left := pcm[i*2]
			right := pcm[i*2+1]
			inputSamples[i] = (left + right) / 2
		}
	}

	// Handle 10ms frames by buffering to 20ms
	if silkSamples < silkWBSamples {
		// 10ms frame - need to buffer
		if e.silkBufferFilled == 0 {
			// First 10ms - store in buffer and wait for second half
			copy(e.silkFrameBuffer[:silkSamples], inputSamples)
			e.silkBufferFilled = silkSamples
			// Don't encode yet - wait for next 10ms
			return
		} else {
			// Second 10ms - combine with buffer and encode full 20ms
			copy(e.silkFrameBuffer[e.silkBufferFilled:], inputSamples)
			inputSamples = e.silkFrameBuffer[:silkWBSamples]
			e.silkBufferFilled = 0 // Reset buffer
		}
	}

	// Encode the SILK frame (now have full 20ms worth of samples)
	// Note: EncodeFrame creates its own range encoder if none is set,
	// but we've already set one via SetRangeEncoder
	_ = e.silkEncoder.EncodeFrame(inputSamples, true)
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// encodeCELTHybrid encodes CELT data for hybrid mode.
// In hybrid mode, CELT only encodes high-frequency bands (17-21).
// Per RFC 6716 Section 3.2:
// - CELT start_band = 17 (bands 0-16 handled by SILK)
// - Only bands 17-21 carry signal in the CELT layer
func (e *Encoder) encodeCELTHybrid(pcm []float64, frameSize int) {
	// Set hybrid mode flag on CELT encoder
	// This affects postfilter flag encoding (skipped in hybrid mode)
	// Reference: libopus celt_encoder.c line 2047-2048
	e.celtEncoder.SetHybrid(true)

	// Get mode configuration
	mode := celt.GetModeConfig(frameSize)

	// Apply pre-emphasis with signal scaling to match libopus
	preemph := e.celtEncoder.ApplyPreemphasisWithScaling(pcm)

	// Compute MDCT with overlap history
	mdctCoeffs := computeMDCTForHybrid(preemph, frameSize, e.channels, e.celtEncoder.OverlapBuffer())

	// Compute band energies
	energies := e.celtEncoder.ComputeBandEnergies(mdctCoeffs, mode.EffBands, frameSize)

	// In hybrid mode, set low bands (0-16) to very low energy
	// These bands are handled by SILK; CELT should not allocate bits to them
	startBand := celt.HybridCELTStartBand // 17
	for i := 0; i < startBand && i < len(energies); i++ {
		energies[i] = -28.0 // Very low energy for SILK bands
	}

	// Normalize bands (only high bands will have meaningful shapes)
	shapes := e.celtEncoder.NormalizeBands(mdctCoeffs, energies, mode.EffBands, frameSize)

	// Get the range encoder
	re := e.celtEncoder.RangeEncoder()
	if re == nil {
		return
	}

	// Encode silence flag = 0 (not silent)
	re.EncodeBit(0, 15)

	// In hybrid mode, postfilter flag is SKIPPED entirely (not encoded)
	// Reference: libopus celt_encoder.c line 2047-2048:
	// if(!hybrid && tell+16<=total_bits) ec_enc_bit_logp(enc, 0, 1);
	// Since we're in hybrid mode, we skip encoding the postfilter flag

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

	// Initialize caps for allocation - use hybrid mode caps that zero out low bands
	caps := celt.InitCapsForHybrid(mode.EffBands, mode.LM, e.channels, startBand)

	// Compute bit allocation
	bitsUsed := re.Tell()
	totalBits := maxHybridPacketSize*8 - bitsUsed
	if totalBits < 0 {
		totalBits = 64
	}

	allocResult := celt.ComputeAllocation(
		totalBits,
		mode.EffBands,
		e.channels,
		caps,  // Use hybrid caps
		nil,   // dynalloc
		5,     // trim (default)
		-1,    // intensity (disabled)
		false, // dualStereo
		mode.LM,
	)

	// Encode fine energy
	e.celtEncoder.EncodeFineEnergy(energies, quantizedEnergies, mode.EffBands, allocResult.FineBits)

	// Encode bands (PVQ)
	// With hybrid caps, bands 0-16 will have 0 bits allocated automatically
	e.celtEncoder.EncodeBands(shapes, nil, allocResult.BandBits, mode.EffBands, frameSize)

	// Update state
	e.celtEncoder.SetPrevEnergy(quantizedEnergies)
	e.celtEncoder.IncrementFrameCount()
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

	// Concatenate
	result := make([]float64, len(mdctMid)+len(mdctSide))
	copy(result[:len(mdctMid)], mdctMid)
	copy(result[len(mdctMid):], mdctSide)

	return result
}
