package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// EncodeFrame encodes a complete SILK frame to bitstream.
// Returns encoded bytes.
func (e *Encoder) EncodeFrame(pcm []float32, vadFlag bool) []byte {
	config := GetBandwidthConfig(e.bandwidth)
	numSubframes := 4 // 20ms frame = 4 subframes

	// Allocate output buffer with a simple bitrate heuristic.
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

	// Step 7: Compute and encode excitation per subframe
	subframeSamples := config.SubframeSamples
	for sf := 0; sf < numSubframes; sf++ {
		start := sf * subframeSamples
		end := start + subframeSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		subframe := pcm[start:end]

		// Compute excitation (LPC residual)
		var gain float32 = 1.0
		if sf < len(gains) {
			gain = gains[sf]
		}
		excitation := e.computeExcitation(subframe, lpcQ12, gain)
		e.encodeExcitation(excitation, signalType, quantOffset)
	}

	// Update state for next frame
	e.isPreviousFrameVoiced = (signalType == 2)
	copy(e.prevLSFQ15, lsfQ15)
	e.MarkEncoded()

	// Finalize encoding
	return e.rangeEncoder.Done()
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
