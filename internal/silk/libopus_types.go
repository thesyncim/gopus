package silk

type nlsfCB struct {
	nVectors           int16
	order              int16
	quantStepSizeQ16   int16
	invQuantStepSizeQ6 int16
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

type cngState struct {
	excBufQ14     [maxFrameLength]int32
	smthNLSFQ15   [maxLPCOrder]int16
	synthStateQ14 [maxLPCOrder]int32
	smthGainQ16   int32
	randSeed      int32
	fsKHz         int32
}

type decoderState struct {
	pitchLagLowBitsICDF []uint8
	pitchContourICDF    []uint8
	nlsfCB              *nlsfCB
	// Scratch buffer references (set by parent Decoder for hot-path optimization).
	// Grouped at offset 0 so the large value arrays below sit in the GC noscan tail.
	scratchSLPC      []int32 // Pre-allocated sLPC buffer
	scratchSLTP      []int16 // Pre-allocated sLTP buffer
	scratchSLTPQ15   []int32 // Pre-allocated sLTP_Q15 buffer
	scratchPresQ14   []int32 // Pre-allocated presQ14 buffer
	scratchEcIx      []int16 // Pre-allocated ecIx buffer
	scratchPredQ8    []uint8 // Pre-allocated predQ8 buffer
	scratchSumPulses []int32 // Size: 21
	scratchNLshifts  []int32 // Size: 21
	prevGainQ16      int32
	excQ14               [maxFrameLength]int32
	sLPCQ14Buf           [maxLPCOrder]int32
	outBuf               [maxFrameLength + 2*maxSubFrameLength]int16
	lagPrev              int32
	lastGainIndex        int8
	nFramesDecoded       int32
	nFramesPerPacket     int32
	VADFlags             [maxFramesPerPacket]int32
	LBRRFlags            [maxFramesPerPacket]int32
	LBRRFlag             int32
	fsKHz                int32
	nbSubfr              int32
	frameLength          int32
	subfrLength          int32
	ltpMemLength         int32
	lpcOrder             int32
	prevNLSFQ15          [maxLPCOrder]int16
	firstFrameAfterReset bool
	indices              sideInfoIndices
	lossCnt              int32
	prevSignalType       int32
	ecPrevSignalType     int32
	ecPrevLagIndex       int16

	// PLC glue state for smooth transitions from concealed to real frames.
	// These fields implement silk_PLC_glue_frames from libopus PLC.c.
	plcConcEnergy       int32 // Energy of last concealed frame (for gluing)
	plcConcEnergyShift  int32 // opus_int conc_energy_shift in silk/structs.h
	plcLastFrameLost    bool  // True if last frame was lost (concealed)
	plcSkipRecoveryGlue bool  // Skip the next recovery ramp after live deep PLC

	// Comfort noise generation state (libopus silk_CNG).
	cng cngState
}

type decoderControl struct {
	pitchL      [maxNbSubfr]int32
	GainsQ16    [maxNbSubfr]int32
	PredCoefQ12 [2][maxLPCOrder]int16
	LTPCoefQ14  [ltpOrder * maxNbSubfr]int16
	LTPScaleQ14 int32
	NumBits     int32
}

type stereoDecState struct {
	predPrevQ13 [2]int16
	sMid        [2]int16
	sSide       [2]int16
}

// stereoEncState is the encoder-side stereo state, libopus stereo_enc_state
// (silk/structs.h).
type stereoEncState struct {
	predPrevQ13   [2]int16 // Previous frame prediction coefficients (Q13)
	sMid          [2]int16 // Mid signal history for LP filter continuity
	sSide         [2]int16 // Side signal history for LP filter continuity
	midSideAmpQ0  [4]int32 // Smoothed mid/residual amplitudes for LP/HP (Q0)
	smthWidthQ14  int16    // Smoothed stereo width (Q14)
	widthPrevQ14  int16    // Previous frame's stereo width (Q14)
	silentSideLen int16    // Accumulated silent side length (samples)
	// Per-frame predictor indices and mid-only flags of the current packet,
	// coded again with the packet's LBRR data in the next packet.
	predIx       [maxFramesPerPacket][2][3]int8
	midOnlyFlags [maxFramesPerPacket]int8
}
