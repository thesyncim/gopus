package silk

type nlsfCB struct {
	nVectors           int
	order              int
	quantStepSizeQ16   int
	invQuantStepSizeQ6 int
	cb1NLSFQ8          []uint8
	cb1WghtQ9          []int16
	cb1ICDF            []uint8
	predQ8             []uint8
	ecSel              []uint8
	ecICDF             []uint8
	ecRatesQ5          []uint8
	deltaMinQ15        []int16
}

type sideInfoIndices struct {
	GainsIndices     [maxNbSubfr]int8
	LTPIndex         [maxNbSubfr]int8
	NLSFIndices      [maxLPCOrder + 1]int8
	lagIndex         int16
	contourIndex     int8
	signalType       int8
	quantOffsetType  int8
	NLSFInterpCoefQ2 int8
	PERIndex         int8
	LTPScaleIndex    int8
	Seed             int8
}

type decoderState struct {
	prevGainQ16          int32
	excQ14               [maxFrameLength]int32
	sLPCQ14Buf           [maxLPCOrder]int32
	outBuf               [maxFrameLength + 2*maxSubFrameLength]int16
	lagPrev              int
	lastGainIndex        int8
	nFramesDecoded       int
	nFramesPerPacket     int
	VADFlags             [maxFramesPerPacket]int
	LBRRFlags            [maxFramesPerPacket]int
	LBRRFlag             int
	fsKHz                int
	nbSubfr              int
	frameLength          int
	subfrLength          int
	ltpMemLength         int
	lpcOrder             int
	prevNLSFQ15          [maxLPCOrder]int16
	firstFrameAfterReset bool
	pitchLagLowBitsICDF  []uint8
	pitchContourICDF     []uint8
	nlsfCB               *nlsfCB
	indices              sideInfoIndices
	lossCnt              int
	prevSignalType       int
	ecPrevSignalType     int
	ecPrevLagIndex       int

	// PLC glue state for smooth transitions from concealed to real frames.
	// These fields implement silk_PLC_glue_frames from libopus PLC.c.
	plcConcEnergy      int32 // Energy of last concealed frame (for gluing)
	plcConcEnergyShift int   // Shift amount for concealed energy
	plcLastFrameLost   bool  // True if last frame was lost (concealed)

	// Scratch buffer references (set by parent Decoder for hot-path optimization).
	// These are nil if the decoderState is used standalone (e.g., in tests).
	scratchSLPC    []int32 // Pre-allocated sLPC buffer
	scratchSLTP    []int16 // Pre-allocated sLTP buffer
	scratchSLTPQ15 []int32 // Pre-allocated sLTP_Q15 buffer
	scratchPresQ14 []int32 // Pre-allocated presQ14 buffer

	// Additional scratch buffers for silkDecodeIndices
	scratchEcIx   []int16 // Pre-allocated ecIx buffer
	scratchPredQ8 []uint8 // Pre-allocated predQ8 buffer

	// Scratch buffers for shell decoder
	scratchPulses3 []int16 // Size: 2
	scratchPulses2 []int16 // Size: 4
	scratchPulses1 []int16 // Size: 8

	// Scratch buffers for pulse decoder
	scratchSumPulses []int // Size: 21
	scratchNLshifts  []int // Size: 21
}

type decoderControl struct {
	pitchL      [maxNbSubfr]int
	GainsQ16    [maxNbSubfr]int32
	PredCoefQ12 [2][maxLPCOrder]int16
	LTPCoefQ14  [ltpOrder * maxNbSubfr]int16
	LTPScaleQ14 int32
}

type stereoDecState struct {
	predPrevQ13 [2]int32
	sMid        [2]int16
	sSide       [2]int16
}

// stereoEncState holds encoder-side stereo state, matching libopus stereo_enc_state.
// This enables proper LP filtering for stereo mid/side predictor analysis.
type stereoEncState struct {
	predPrevQ13   [2]int32 // Previous frame prediction coefficients (Q13)
	sMid          [2]int16 // Mid signal buffer for LP filter continuity
	sSide         [2]int16 // Side signal buffer for LP filter continuity
	midSideAmpQ0  [4]int32 // Smoothed mid/side amplitudes [LP_mid, LP_res, HP_mid, HP_res]
	smthWidthQ14  int16    // Smoothed stereo width (Q14)
	widthPrevQ14  int16    // Previous frame's stereo width (Q14)
	silentSideLen int16    // Length of silent side samples for mid-only transition
}
