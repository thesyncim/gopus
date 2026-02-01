package silk

import "github.com/thesyncim/gopus/rangecoding"

// EncodeFrame encodes a complete SILK frame to bitstream.
// Returns encoded bytes. If a range encoder was pre-set via SetRangeEncoder(),
// it will be used (for hybrid mode) and nil is returned since the caller
// manages the shared encoder.
func (e *Encoder) EncodeFrame(pcm []float32, vadFlag bool) []byte {
	config := GetBandwidthConfig(e.bandwidth)
	numSubframes := 4 // 20ms frame = 4 subframes

	// Check if we have a pre-set range encoder (hybrid mode)
	// Note: rangeEncoder is set externally via SetRangeEncoder() for hybrid mode.
	// In standalone mode, rangeEncoder should be nil at the start of each frame.
	useSharedEncoder := e.rangeEncoder != nil

	if !useSharedEncoder {
		// Standalone SILK mode: create our own range encoder
		bufSize := len(pcm) / 3
		if bufSize < 80 {
			bufSize = 80
		}
		if bufSize > 200 {
			bufSize = 200
		}
		output := make([]byte, bufSize)
		e.rangeEncoder = &rangecoding.Encoder{}
		e.rangeEncoder.Init(output)
	}

	// Step 1: Classify frame (VAD)
	var signalType, quantOffset int
	if vadFlag {
		signalType, quantOffset = e.classifyFrame(pcm)
	} else {
		signalType, quantOffset = 0, 0
	}

	// Step 2: Encode frame type using ICDFFrameTypeVADActive
	e.encodeFrameType(vadFlag, signalType, quantOffset)

	// Step 3: Compute and encode gains
	gains := e.computeSubframeGains(pcm, numSubframes)
	e.encodeSubframeGains(gains, signalType, numSubframes)

	// Step 4: Compute LPC coefficients
	lpcQ12 := e.computeLPCFromFrame(pcm)

	// Step 5: Convert to LSF and quantize
	lsfQ15 := lpcToLSFEncode(lpcQ12)
	stage1Idx, residuals, interpIdx := e.quantizeLSF(lsfQ15, e.bandwidth, signalType)
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)

	// Step 6: Pitch detection and LTP (voiced only)
	var pitchLags []int
	if signalType == 2 {
		pitchLags = e.detectPitch(pcm, numSubframes)
		e.encodePitchLags(pitchLags, numSubframes)

		ltpCoeffs := e.analyzeLTP(pcm, pitchLags, numSubframes)
		periodicity := e.determinePeriodicity(pcm, pitchLags)
		e.encodeLTPCoeffs(ltpCoeffs, periodicity, numSubframes)
	}

	// Step 7: Encode seed (LAST in indices, BEFORE pulses)
	// Per libopus: seed = frameCounter++ & 3
	seed := e.frameCounter & 3
	e.frameCounter++
	e.rangeEncoder.EncodeICDF16(seed, ICDFLCGSeed, 8)

	// Step 8: Compute excitation for ENTIRE frame (not per-subframe)
	// Per libopus silk_encode_pulses(), pulses are encoded for full frame_length
	subframeSamples := config.SubframeSamples
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	// Compute excitation for all subframes combined
	allExcitation := make([]int32, frameSamples)
	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		subframe := pcm[start:end]

		// Compute excitation (LPC residual) for this subframe
		var gain float32 = 1.0
		if sf < len(gains) {
			gain = gains[sf]
		}
		excitation := e.computeExcitation(subframe, lpcQ12, gain)

		// Copy to combined buffer
		for i := 0; i < len(excitation) && start+i < frameSamples; i++ {
			allExcitation[start+i] = excitation[i]
		}
	}

	// Encode ALL pulses for the entire frame at once
	e.encodePulses(allExcitation, signalType, quantOffset)

	// Update state for next frame
	e.isPreviousFrameVoiced = (signalType == 2)
	copy(e.prevLSFQ15, lsfQ15)
	e.MarkEncoded()

	// Finalize encoding
	if useSharedEncoder {
		// Hybrid mode: caller manages the range encoder
		// Capture range state for FinalRange() before returning
		e.lastRng = e.rangeEncoder.Range()
		return nil
	}

	// Standalone mode: get the encoded bytes and clear the range encoder
	// so the next frame creates a fresh one
	// Capture range state BEFORE Done() clears it
	e.lastRng = e.rangeEncoder.Range()
	result := e.rangeEncoder.Done()
	e.rangeEncoder = nil
	return result
}

// encodeFrameType encodes VAD flag, signal type, and quantization offset.
// Uses ICDFFrameTypeVADActive from tables.go
func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	if !vadFlag {
		// Inactive frame - minimal encoding
		// For inactive frames, signal type is 0, use different handling
		e.rangeEncoder.EncodeICDF16(0, ICDFFrameTypeVADActive, 8)
		return
	}

	// Active frame: encode signal type and quant offset
	// idx = (signalType-1)*2 + quantOffset for signalType 1,2
	// signalType 0 (inactive) handled above
	if signalType < 1 {
		signalType = 1 // Default to unvoiced if inactive with VAD
	}
	idx := (signalType-1)*2 + quantOffset
	if idx < 0 {
		idx = 0
	}
	if idx > 3 {
		idx = 3
	}
	e.rangeEncoder.EncodeICDF16(idx, ICDFFrameTypeVADActive, 8)
}
