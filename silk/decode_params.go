package silk

// FrameParams holds decoded parameters for a single SILK frame.
// All control parameters needed for synthesis filtering.
type FrameParams struct {
	// Frame classification
	SignalType  int // 0=inactive, 1=unvoiced, 2=voiced
	QuantOffset int // 0=low, 1=high quantization

	// Subframe parameters (4 subframes for 20ms, 2 for 10ms)
	NumSubframes int
	Gains        []int32 // Q16 format, one per subframe

	// LPC parameters
	LPCOrder  int
	LPCCoeffs []int16 // Q12 format

	// Pitch parameters (voiced only)
	PitchLags      []int    // One per subframe
	LTPCoeffs      [][]int8 // Q7 format, [subframe][5 taps]
	LTPPeriodicity int      // 0, 1, or 2 (selects LTP codebook)
	LTPScaleIndex  int      // LTP scale index for gain adjustment

	// Excitation (filled by excitation decoder)
	Excitation []int32
}

// DecodeFrameType decodes signal type and quantization offset from VAD flag.
// Per RFC 6716 Section 4.2.7.3.
//
// For inactive frames (vadFlag=false), returns (0, 0).
// For active frames (vadFlag=true), decodes signal type (1=unvoiced, 2=voiced)
// and quantization offset (0=low, 1=high).
func (d *Decoder) DecodeFrameType(vadFlag bool) (signalType, quantOffset int) {
	var idx int
	if vadFlag {
		// ICDFFrameTypeVADActive encodes 4 outcomes:
		// 0: unvoiced low, 1: unvoiced high, 2: voiced low, 3: voiced high
		idx = d.rangeDecoder.DecodeICDF16(ICDFFrameTypeVADActive, 8) + 2
	} else {
		// VAD inactive uses a 2-outcome table (typeOffset 0 or 1).
		idx = d.rangeDecoder.DecodeICDF16(ICDFFrameTypeVADInactive, 8)
	}
	signalType = idx >> 1
	quantOffset = idx & 1
	return
}
