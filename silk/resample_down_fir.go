package silk

// Downsampling resampler using AR2 filter followed by FIR interpolation.
// This ports libopus silk_resampler_private_down_FIR for encoder downsampling.
// Reference: libopus silk/resampler.c, silk/resampler_private_down_FIR.c

// Resampler FIR order constants
const (
	resamplerDownOrderFIR0 = 18 // For 3:4 and 2:3 ratios
	resamplerDownOrderFIR1 = 24 // For 1:2 ratio
	resamplerDownOrderFIR2 = 36 // For 1:3, 1:4, 1:6 ratios
)

// Encoder delay matrix from libopus resampler.c
// in  \ out  8  12  16
var delayMatrixEnc = [6][3]int8{
	/*  8 */ {6, 0, 3},
	/* 12 */ {0, 7, 3},
	/* 16 */ {0, 1, 10},
	/* 24 */ {0, 2, 6},
	/* 48 */ {18, 10, 12},
	/* 96 */ {0, 0, 44},
}

// FIR coefficients for downsampling from libopus resampler_rom.c
// Format: [2 AR2 coefficients] + [symmetric FIR coefficients]

// silk_Resampler_3_4_COEFS: 48kHz -> 36kHz (3:4 ratio)
var silkResampler34Coefs = []int16{
	-20694, -13867, // AR2 coefficients
	-49, 64, 17, -157, 353, -496, 163, 11047, 22205, // Phase 0
	-39, 6, 91, -170, 186, 23, -896, 6336, 19928, // Phase 1
	-19, -36, 102, -89, -24, 328, -951, 2568, 15909, // Phase 2
}

// silk_Resampler_2_3_COEFS: 48kHz -> 32kHz, 24kHz -> 16kHz (2:3 ratio)
var silkResampler23Coefs = []int16{
	-14457, -14019, // AR2 coefficients
	64, 128, -122, 36, 310, -768, 584, 9267, 17733, // Phase 0
	12, 128, 18, -142, 288, -117, -865, 4123, 14459, // Phase 1
}

// silk_Resampler_1_2_COEFS: 48kHz -> 24kHz (1:2 ratio)
var silkResampler12Coefs = []int16{
	616, -14323, // AR2 coefficients
	-10, 39, 58, -46, -84, 120, 184, -315, -541, 1284, 5380, 9024, // Symmetric FIR
}

// silk_Resampler_1_3_COEFS: 48kHz -> 16kHz (1:3 ratio) - MOST COMMON FOR SILK ENCODER
var silkResampler13Coefs = []int16{
	16102, -15162, // AR2 coefficients
	-13, 0, 20, 26, 5, -31, -43, -4, 65, 90, 7, -157, -248, -44, 593, 1583, 2612, 3271, // Symmetric FIR
}

// silk_Resampler_1_4_COEFS: 48kHz -> 12kHz (1:4 ratio)
var silkResampler14Coefs = []int16{
	22500, -15099, // AR2 coefficients
	3, -14, -20, -15, 2, 25, 37, 25, -16, -71, -107, -79, 50, 292, 623, 982, 1288, 1464, // Symmetric FIR
}

// silk_Resampler_1_6_COEFS: 48kHz -> 8kHz (1:6 ratio)
var silkResampler16Coefs = []int16{
	27540, -15257, // AR2 coefficients
	17, 12, 8, 1, -10, -22, -30, -32, -22, 3, 44, 100, 168, 243, 317, 381, 429, 455, // Symmetric FIR
}

// DownsamplingResampler implements the libopus down_FIR resampling algorithm.
// This is used for encoder mode (48kHz -> SILK rates).
type DownsamplingResampler struct {
	// AR2 IIR filter state (2 elements)
	sIIR [2]int32

	// FIR filter state (size depends on FIR order)
	sFIR []int32

	// Configuration
	fsInKHz      int32
	fsOutKHz     int32
	batchSize    int32
	inputDelay   int32
	invRatioQ16  int32
	firOrder     int
	firFracs     int
	coefs        []int16 // Full coefficient array (AR2 + FIR)
	firCoefs     []int16 // Just the FIR coefficients (after AR2)

	// Delay buffer
	delayBuf []int16

	// Scratch buffers
	scratchBuf []int32
}

// NewDownsamplingResampler creates a new downsampling resampler for encoder mode.
// Supports: 48kHz -> 8/12/16 kHz, 24kHz -> 8/12/16 kHz, etc.
func NewDownsamplingResampler(fsIn, fsOut int) *DownsamplingResampler {
	r := &DownsamplingResampler{
		fsInKHz:  int32(fsIn / 1000),
		fsOutKHz: int32(fsOut / 1000),
	}

	// Batch size: 10ms of input data
	r.batchSize = r.fsInKHz * 10 // RESAMPLER_MAX_BATCH_SIZE_MS = 10

	// Get encoder delay from delay matrix
	inIdx := rateIDEnc(fsIn)
	outIdx := rateIDEnc(fsOut)
	if inIdx >= 0 && inIdx < 6 && outIdx >= 0 && outIdx < 3 {
		r.inputDelay = int32(delayMatrixEnc[inIdx][outIdx])
	}

	// Select coefficients based on ratio
	fsInHz := int32(fsIn)
	fsOutHz := int32(fsOut)

	if fsOutHz*4 == fsInHz*3 { // 3:4 ratio (48kHz -> 36kHz)
		r.firFracs = 3
		r.firOrder = resamplerDownOrderFIR0
		r.coefs = silkResampler34Coefs
	} else if fsOutHz*3 == fsInHz*2 { // 2:3 ratio (48kHz -> 32kHz, 24kHz -> 16kHz)
		r.firFracs = 2
		r.firOrder = resamplerDownOrderFIR0
		r.coefs = silkResampler23Coefs
	} else if fsOutHz*2 == fsInHz { // 1:2 ratio (48kHz -> 24kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR1
		r.coefs = silkResampler12Coefs
	} else if fsOutHz*3 == fsInHz { // 1:3 ratio (48kHz -> 16kHz) - MOST COMMON
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler13Coefs
	} else if fsOutHz*4 == fsInHz { // 1:4 ratio (48kHz -> 12kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler14Coefs
	} else if fsOutHz*6 == fsInHz { // 1:6 ratio (48kHz -> 8kHz)
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler16Coefs
	} else {
		// Unsupported ratio - use 1:3 as fallback
		r.firFracs = 1
		r.firOrder = resamplerDownOrderFIR2
		r.coefs = silkResampler13Coefs
	}

	// FIR coefficients start after 2 AR2 coefficients
	r.firCoefs = r.coefs[2:]

	// Compute invRatio_Q16 (up2x = 0 for downsampling)
	r.invRatioQ16 = int32((int64(fsInHz) << 14) / int64(fsOutHz) << 2)
	// Round up
	for smulww(r.invRatioQ16, fsOutHz) < (fsInHz) {
		r.invRatioQ16++
	}

	// Initialize state
	r.sFIR = make([]int32, r.firOrder)
	r.delayBuf = make([]int16, r.fsInKHz)
	r.scratchBuf = make([]int32, int(r.batchSize)+r.firOrder)

	return r
}

// rateIDEnc converts sample rate to index for encoder delay matrix
func rateIDEnc(rate int) int {
	switch rate {
	case 8000:
		return 0
	case 12000:
		return 1
	case 16000:
		return 2
	case 24000:
		return 3
	case 48000:
		return 4
	case 96000:
		return 5
	default:
		return -1
	}
}

// Process resamples input samples and returns output samples.
func (r *DownsamplingResampler) Process(in []float32) []float32 {
	// Convert float32 to int16
	inInt := make([]int16, len(in))
	for i, v := range in {
		inInt[i] = float32ToInt16(v)
	}
	if len(inInt) < int(r.fsInKHz) {
		padded := make([]int16, r.fsInKHz)
		copy(padded, inInt)
		inInt = padded
	}

	// Calculate output length
	outLen := int(int64(len(in)) * int64(r.fsOutKHz) / int64(r.fsInKHz))
	outInt := make([]int16, outLen)

	// Process with libopus-style delay handling
	r.processWithDelay(outInt, inInt)

	// Convert back to float32
	out := make([]float32, len(outInt))
	for i, v := range outInt {
		out[i] = float32(v) / 32768.0
	}
	return out
}

// ProcessInto resamples into a pre-allocated buffer.
func (r *DownsamplingResampler) ProcessInto(in []float32, out []float32) int {
	// Convert float32 to int16
	inInt := make([]int16, len(in))
	for i, v := range in {
		inInt[i] = float32ToInt16(v)
	}
	if len(inInt) < int(r.fsInKHz) {
		padded := make([]int16, r.fsInKHz)
		copy(padded, inInt)
		inInt = padded
	}

	// Calculate output length
	outLen := int(int64(len(in)) * int64(r.fsOutKHz) / int64(r.fsInKHz))
	if outLen > len(out) {
		outLen = len(out)
	}
	outInt := make([]int16, outLen)

	// Process with libopus-style delay handling
	r.processWithDelay(outInt, inInt)

	// Convert back to float32
	for i := 0; i < outLen && i < len(outInt); i++ {
		out[i] = float32(outInt[i]) / 32768.0
	}
	return outLen
}

// processWithDelay matches libopus silk_resampler() for encoder downsampling.
// It applies input delay buffering before calling the down_FIR core.
func (r *DownsamplingResampler) processWithDelay(out []int16, in []int16) {
	inLen := len(in)
	if inLen == 0 {
		return
	}

	nSamples := int(r.fsInKHz - r.inputDelay)
	if nSamples < 0 {
		nSamples = 0
	}
	if nSamples > inLen {
		nSamples = inLen
	}

	// Copy to delay buffer (preserve first inputDelay samples from previous call).
	if nSamples > 0 {
		copy(r.delayBuf[r.inputDelay:], in[:nSamples])
	}

	// Process delay buffer (1ms = fsInKHz samples).
	if len(out) >= int(r.fsOutKHz) {
		r.processInt16(out[:r.fsOutKHz], r.delayBuf[:r.fsInKHz])
	}

	// Process remaining input, excluding the last inputDelay samples.
	if inLen > int(r.fsInKHz) && len(out) > int(r.fsOutKHz) {
		end := inLen - int(r.inputDelay)
		if end < nSamples {
			end = nSamples
		}
		if end > inLen {
			end = inLen
		}
		r.processInt16(out[r.fsOutKHz:], in[nSamples:end])
	}

	// Save last inputDelay samples to delay buffer.
	if r.inputDelay > 0 && inLen >= int(r.inputDelay) {
		copy(r.delayBuf[:r.inputDelay], in[inLen-int(r.inputDelay):])
	}
}

// processInt16 is the core downsampling function.
// Matches libopus silk_resampler_private_down_FIR exactly.
func (r *DownsamplingResampler) processInt16(out []int16, in []int16) {
	inLen := int32(len(in))
	outIdx := 0

	// Ensure scratch buffer is large enough
	bufSize := int(r.batchSize) + r.firOrder
	if len(r.scratchBuf) < bufSize {
		r.scratchBuf = make([]int32, bufSize)
	}
	buf := r.scratchBuf

	// Copy FIR state (buffered samples) to start of buffer
	copy(buf, r.sFIR)

	inOffset := int32(0)
	indexIncrementQ16 := r.invRatioQ16
	var lastNSamplesIn int32

	for {
		nSamplesIn := min32(inLen, r.batchSize)
		lastNSamplesIn = nSamplesIn

		// Apply AR2 filter (output in Q8)
		r.ar2Filter(buf[r.firOrder:], in[inOffset:inOffset+nSamplesIn])

		// Interpolate filtered signal
		maxIndexQ16 := nSamplesIn << 16
		outIdx = r.firInterpolate(out[outIdx:], buf, maxIndexQ16, indexIncrementQ16, outIdx)

		inOffset += nSamplesIn
		inLen -= nSamplesIn

		if inLen > 1 {
			// More iterations to do; copy last part of filtered signal to beginning of buffer
			copy(buf, buf[nSamplesIn:nSamplesIn+int32(r.firOrder)])
		} else {
			break
		}
	}

	// Save FIR state for next call (from the last nSamplesIn position)
	copy(r.sFIR, buf[lastNSamplesIn:lastNSamplesIn+int32(r.firOrder)])
}

// ar2Filter applies the second-order AR pre-filter.
// Output is in Q8 format.
// Matches libopus silk_resampler_private_AR2 exactly.
//
// Reference: silk/resampler_private_AR2.c
//
//	for( k = 0; k < len; k++ ) {
//	    out32       = silk_ADD_LSHIFT32( S[ 0 ], (opus_int32)in[ k ], 8 );
//	    out_Q8[ k ] = out32;
//	    out32       = silk_LSHIFT( out32, 2 );
//	    S[ 0 ]      = silk_SMLAWB( S[ 1 ], out32, A_Q14[ 0 ] );
//	    S[ 1 ]      = silk_SMULWB( out32, A_Q14[ 1 ] );
//	}
func (r *DownsamplingResampler) ar2Filter(out []int32, in []int16) {
	A0Q14 := int32(r.coefs[0]) // Q14 coefficient
	A1Q14 := int32(r.coefs[1]) // Q14 coefficient

	for k := 0; k < len(in); k++ {
		// out32 = S[0] + (in[k] << 8)
		out32 := r.sIIR[0] + (int32(in[k]) << 8)

		// Store output in Q8 format
		out[k] = out32

		// out32 = out32 << 2 (for filter coefficient application)
		out32 = out32 << 2

		// S[0] = S[1] + silk_SMULWB(out32, A_Q14[0])
		// silk_SMULWB: (a * (int16)b) >> 16
		r.sIIR[0] = r.sIIR[1] + silkSMULWB(out32, A0Q14)

		// S[1] = silk_SMULWB(out32, A_Q14[1])
		r.sIIR[1] = silkSMULWB(out32, A1Q14)
	}
}

// firInterpolate performs FIR interpolation on the filtered signal.
// Matches libopus silk_resampler_private_down_FIR_INTERPOL.
func (r *DownsamplingResampler) firInterpolate(out []int16, buf []int32, maxIndexQ16, indexIncrementQ16 int32, startOutIdx int) int {
	outIdx := 0

	switch r.firOrder {
	case resamplerDownOrderFIR0:
		// 18-tap filter with multiple phases
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)
			interpolInd := int(smulwb(indexQ16&0xFFFF, int32(r.firFracs)))

			// Forward taps
			interpol := r.firCoefs[resamplerDownOrderFIR0/2*interpolInd:]
			resQ6 := smulwb(buf[bufPtr+0], int32(interpol[0]))
			resQ6 = smlawb(resQ6, buf[bufPtr+1], int32(interpol[1]))
			resQ6 = smlawb(resQ6, buf[bufPtr+2], int32(interpol[2]))
			resQ6 = smlawb(resQ6, buf[bufPtr+3], int32(interpol[3]))
			resQ6 = smlawb(resQ6, buf[bufPtr+4], int32(interpol[4]))
			resQ6 = smlawb(resQ6, buf[bufPtr+5], int32(interpol[5]))
			resQ6 = smlawb(resQ6, buf[bufPtr+6], int32(interpol[6]))
			resQ6 = smlawb(resQ6, buf[bufPtr+7], int32(interpol[7]))
			resQ6 = smlawb(resQ6, buf[bufPtr+8], int32(interpol[8]))

			// Reverse taps (symmetric filter)
			interpol = r.firCoefs[resamplerDownOrderFIR0/2*(r.firFracs-1-interpolInd):]
			resQ6 = smlawb(resQ6, buf[bufPtr+17], int32(interpol[0]))
			resQ6 = smlawb(resQ6, buf[bufPtr+16], int32(interpol[1]))
			resQ6 = smlawb(resQ6, buf[bufPtr+15], int32(interpol[2]))
			resQ6 = smlawb(resQ6, buf[bufPtr+14], int32(interpol[3]))
			resQ6 = smlawb(resQ6, buf[bufPtr+13], int32(interpol[4]))
			resQ6 = smlawb(resQ6, buf[bufPtr+12], int32(interpol[5]))
			resQ6 = smlawb(resQ6, buf[bufPtr+11], int32(interpol[6]))
			resQ6 = smlawb(resQ6, buf[bufPtr+10], int32(interpol[7]))
			resQ6 = smlawb(resQ6, buf[bufPtr+9], int32(interpol[8]))

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}

	case resamplerDownOrderFIR1:
		// 24-tap symmetric filter (single phase)
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)

			resQ6 := smulwb(buf[bufPtr+0]+buf[bufPtr+23], int32(r.firCoefs[0]))
			resQ6 = smlawb(resQ6, buf[bufPtr+1]+buf[bufPtr+22], int32(r.firCoefs[1]))
			resQ6 = smlawb(resQ6, buf[bufPtr+2]+buf[bufPtr+21], int32(r.firCoefs[2]))
			resQ6 = smlawb(resQ6, buf[bufPtr+3]+buf[bufPtr+20], int32(r.firCoefs[3]))
			resQ6 = smlawb(resQ6, buf[bufPtr+4]+buf[bufPtr+19], int32(r.firCoefs[4]))
			resQ6 = smlawb(resQ6, buf[bufPtr+5]+buf[bufPtr+18], int32(r.firCoefs[5]))
			resQ6 = smlawb(resQ6, buf[bufPtr+6]+buf[bufPtr+17], int32(r.firCoefs[6]))
			resQ6 = smlawb(resQ6, buf[bufPtr+7]+buf[bufPtr+16], int32(r.firCoefs[7]))
			resQ6 = smlawb(resQ6, buf[bufPtr+8]+buf[bufPtr+15], int32(r.firCoefs[8]))
			resQ6 = smlawb(resQ6, buf[bufPtr+9]+buf[bufPtr+14], int32(r.firCoefs[9]))
			resQ6 = smlawb(resQ6, buf[bufPtr+10]+buf[bufPtr+13], int32(r.firCoefs[10]))
			resQ6 = smlawb(resQ6, buf[bufPtr+11]+buf[bufPtr+12], int32(r.firCoefs[11]))

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}

	case resamplerDownOrderFIR2:
		// 36-tap symmetric filter (single phase) - MOST COMMON (48kHz -> 16kHz)
		for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
			bufPtr := int(indexQ16 >> 16)

			resQ6 := smulwb(buf[bufPtr+0]+buf[bufPtr+35], int32(r.firCoefs[0]))
			resQ6 = smlawb(resQ6, buf[bufPtr+1]+buf[bufPtr+34], int32(r.firCoefs[1]))
			resQ6 = smlawb(resQ6, buf[bufPtr+2]+buf[bufPtr+33], int32(r.firCoefs[2]))
			resQ6 = smlawb(resQ6, buf[bufPtr+3]+buf[bufPtr+32], int32(r.firCoefs[3]))
			resQ6 = smlawb(resQ6, buf[bufPtr+4]+buf[bufPtr+31], int32(r.firCoefs[4]))
			resQ6 = smlawb(resQ6, buf[bufPtr+5]+buf[bufPtr+30], int32(r.firCoefs[5]))
			resQ6 = smlawb(resQ6, buf[bufPtr+6]+buf[bufPtr+29], int32(r.firCoefs[6]))
			resQ6 = smlawb(resQ6, buf[bufPtr+7]+buf[bufPtr+28], int32(r.firCoefs[7]))
			resQ6 = smlawb(resQ6, buf[bufPtr+8]+buf[bufPtr+27], int32(r.firCoefs[8]))
			resQ6 = smlawb(resQ6, buf[bufPtr+9]+buf[bufPtr+26], int32(r.firCoefs[9]))
			resQ6 = smlawb(resQ6, buf[bufPtr+10]+buf[bufPtr+25], int32(r.firCoefs[10]))
			resQ6 = smlawb(resQ6, buf[bufPtr+11]+buf[bufPtr+24], int32(r.firCoefs[11]))
			resQ6 = smlawb(resQ6, buf[bufPtr+12]+buf[bufPtr+23], int32(r.firCoefs[12]))
			resQ6 = smlawb(resQ6, buf[bufPtr+13]+buf[bufPtr+22], int32(r.firCoefs[13]))
			resQ6 = smlawb(resQ6, buf[bufPtr+14]+buf[bufPtr+21], int32(r.firCoefs[14]))
			resQ6 = smlawb(resQ6, buf[bufPtr+15]+buf[bufPtr+20], int32(r.firCoefs[15]))
			resQ6 = smlawb(resQ6, buf[bufPtr+16]+buf[bufPtr+19], int32(r.firCoefs[16]))
			resQ6 = smlawb(resQ6, buf[bufPtr+17]+buf[bufPtr+18], int32(r.firCoefs[17]))

			if outIdx < len(out) {
				out[outIdx] = int16(sat16(rshiftRound(resQ6, 6)))
				outIdx++
			}
		}
	}

	return startOutIdx + outIdx
}

// Helper functions for fixed-point arithmetic
// Note: These use the existing functions from resample_libopus.go:
// smulww, smulwb, smlawb, sat16, rshiftRound
