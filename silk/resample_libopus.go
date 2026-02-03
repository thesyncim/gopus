package silk

// LibopusResampler implements the exact SILK resampler from libopus.
// This uses a combination of 2x allpass upsampling followed by FIR interpolation.
type LibopusResampler struct {
	// IIR state for 2x upsampler (6 elements for 3rd order allpass)
	sIIR [6]int32

	// FIR delay buffer (8 samples for the 8-tap symmetric FIR)
	sFIR [8]int16

	// Configuration
	invRatioQ16 int32 // Input/output ratio in Q16
	batchSize   int32 // Number of samples per batch
	inputDelay  int32 // Delay compensation
	fsInKHz     int32
	fsOutKHz    int32

	// Delay buffer for continuity (size = fsInKHz)
	delayBuf []int16

	// Debug: capture state at start of Process() call
	debugEnabled          bool
	debugProcessCallSIIR  [6]int32
	debugInputFirst10     [10]float32
	debugDelayBufFirst8   [8]int16
	debugProcessCallCount int
	debugLastProcessID    int // ID captured during Process()
	debugOutputFirst10    [10]float32

	// Unique ID for tracking
	debugID int

	// Pre-allocated scratch buffers for zero-allocation resampling
	scratchBuf    []int16   // Size: 2*batchSize + resamplerOrderFIR12
	scratchIn     []int16   // Size: max input samples
	scratchOut    []int16   // Size: max output samples
	scratchResult []float32 // Size: max output samples
}

var debugResamplerNextID int

// resampleIIRFIRSlice processes a slice of input samples and writes to output.
// This is the core IIR_FIR processing without the delay buffer management.
func (r *LibopusResampler) resampleIIRFIRSlice(out []int16, in []int16) {
	r.resampleIIRFIRSliceWithScratch(out, in, nil)
}

// resampleIIRFIRSliceWithScratch is like resampleIIRFIRSlice but uses a pre-allocated scratch buffer.
func (r *LibopusResampler) resampleIIRFIRSliceWithScratch(out []int16, in []int16, scratch []int16) {
	inLen := int32(len(in))
	outIdx := 0

	// Use pre-allocated buffer for 2x upsampled data + FIR history
	bufSize := int(2*r.batchSize + resamplerOrderFIR12)
	var buf []int16
	if scratch != nil && len(scratch) >= bufSize {
		buf = scratch[:bufSize]
		// Clear the buffer
		for i := range buf {
			buf[i] = 0
		}
	} else if r.scratchBuf != nil && len(r.scratchBuf) >= bufSize {
		buf = r.scratchBuf[:bufSize]
		for i := range buf {
			buf[i] = 0
		}
	} else {
		buf = make([]int16, bufSize)
	}

	// Copy FIR state to start of buffer
	copy(buf, r.sFIR[:])

	inOffset := int32(0)
	var lastNSamplesIn int32
	for {
		nSamplesIn := min32(inLen, r.batchSize)
		lastNSamplesIn = nSamplesIn

		// 2x upsample using allpass filters
		r.up2HQ(buf[resamplerOrderFIR12:], in[inOffset:inOffset+nSamplesIn])

		// FIR interpolation
		maxIndexQ16 := nSamplesIn << 17 // nSamplesIn * 2 * 65536
		outIdx = r.firInterpol(out, outIdx, buf, maxIndexQ16)

		inOffset += nSamplesIn
		inLen -= nSamplesIn

		if inLen > 0 {
			// Copy last part of buffer to beginning for next iteration
			copy(buf, buf[nSamplesIn*2:nSamplesIn*2+resamplerOrderFIR12])
		} else {
			break
		}
	}

	// Save FIR state for next call
	copy(r.sFIR[:], buf[lastNSamplesIn*2:lastNSamplesIn*2+resamplerOrderFIR12])
}

// Coefficients for 2x upsampler allpass filters (from resampler_rom.h)
var (
	// Tables for 2x upsampler, high quality
	// Even samples: 3rd order allpass
	silkResamplerUp2HQ0 = [3]int16{1746, 14986, 39083 - 65536}
	// Odd samples: 3rd order allpass
	silkResamplerUp2HQ1 = [3]int16{6854, 25769, 55542 - 65536}
)

// FIR interpolation coefficients (12 phases, 4 coefficients each - symmetric)
// Table with interpolation fractions of 1/24, 3/24, 5/24, ..., 23/24
var silkResamplerFracFIR12 = [12][4]int16{
	{189, -600, 617, 30567},
	{117, -159, -1070, 29704},
	{52, 221, -2392, 28276},
	{-4, 529, -3350, 26341},
	{-48, 758, -3956, 23973},
	{-80, 905, -4235, 21254},
	{-99, 972, -4222, 18278},
	{-107, 967, -3957, 15143},
	{-103, 896, -3487, 11950},
	{-91, 773, -2865, 8798},
	{-71, 611, -2143, 5784},
	{-46, 425, -1375, 2996},
}

// Delay matrix for decoder (from resampler.c)
// in \ out  8  12  16  24  48
var delayMatrixDec = [3][5]int8{
	/*  8 */ {4, 0, 2, 0, 0},
	/* 12 */ {0, 9, 4, 7, 4},
	/* 16 */ {0, 3, 12, 7, 7},
}

// rateID converts sample rate to index: 8000->0, 12000->1, 16000->2, 24000->3, 48000->4
func rateID(rate int) int {
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
	default:
		return 0
	}
}

const resamplerOrderFIR12 = 8
const resamplerMaxBatchSizeMs = 10
const resamplerMaxFrameMs = 60

// NewLibopusResampler creates a new resampler matching libopus behavior.
func NewLibopusResampler(fsIn, fsOut int) *LibopusResampler {
	r := &LibopusResampler{
		fsInKHz:  int32(fsIn / 1000),
		fsOutKHz: int32(fsOut / 1000),
	}

	// Delay compensation from libopus
	inIdx := rateID(fsIn)
	outIdx := rateID(fsOut)
	if inIdx < 3 && outIdx < 5 {
		r.inputDelay = int32(delayMatrixDec[inIdx][outIdx])
	}

	// Batch size
	r.batchSize = r.fsInKHz * resamplerMaxBatchSizeMs

	// Calculate invRatio_Q16 for upsampling
	// For IIR_FIR: up2x = 1, so we first 2x upsample
	// invRatio_Q16 = ((Fs_in << (14 + up2x)) / Fs_out) << 2
	up2x := 1
	invRatio := int32((fsIn << (14 + up2x)) / fsOut)
	r.invRatioQ16 = invRatio << 2

	// Make sure the ratio is rounded up
	for smulww(r.invRatioQ16, int32(fsOut)) < int32(fsIn<<up2x) {
		r.invRatioQ16++
	}

	// Initialize delay buffer
	r.delayBuf = make([]int16, r.fsInKHz)

	// Pre-allocate scratch buffers for zero-allocation resampling.
	// Opus allows up to 60ms frames, so size for that worst case.
	// Max input: fsInKHz * 60
	// Max output: fsOutKHz * 60
	maxInputSamples := int(r.fsInKHz * resamplerMaxFrameMs)
	maxOutputSamples := int(r.fsOutKHz * resamplerMaxFrameMs)
	r.scratchBuf = make([]int16, 2*r.batchSize+resamplerOrderFIR12)
	r.scratchIn = make([]int16, maxInputSamples)
	r.scratchOut = make([]int16, maxOutputSamples)
	r.scratchResult = make([]float32, maxOutputSamples)

	// Assign unique ID for debugging
	debugResamplerNextID++
	r.debugID = debugResamplerNextID

	return r
}

// ResamplerState holds the internal state of the resampler.
type ResamplerState struct {
	sIIR     [6]int32
	sFIR     [8]int16
	delayBuf []int16
}

// State returns a snapshot of the current resampler state.
func (r *LibopusResampler) State() ResamplerState {
	s := ResamplerState{
		sIIR: r.sIIR,
		sFIR: r.sFIR,
	}
	if len(r.delayBuf) > 0 {
		s.delayBuf = make([]int16, len(r.delayBuf))
		copy(s.delayBuf, r.delayBuf)
	}
	return s
}

// SetState restores the resampler state from a snapshot.
func (r *LibopusResampler) SetState(s ResamplerState) {
	r.sIIR = s.sIIR
	r.sFIR = s.sFIR
	if len(s.delayBuf) > 0 && len(r.delayBuf) >= len(s.delayBuf) {
		copy(r.delayBuf, s.delayBuf)
	}
}

// Reset clears the resampler state.
func (r *LibopusResampler) Reset() {
	for i := range r.sIIR {
		r.sIIR[i] = 0
	}
	for i := range r.sFIR {
		r.sFIR[i] = 0
	}
	for i := range r.delayBuf {
		r.delayBuf[i] = 0
	}
}

// CopyFrom copies state from another resampler.
// This is used to sync stereo resampler state when switching from mono.
func (r *LibopusResampler) CopyFrom(src *LibopusResampler) {
	if r == nil || src == nil {
		return
	}

	r.sIIR = src.sIIR
	r.sFIR = src.sFIR
	r.invRatioQ16 = src.invRatioQ16
	r.batchSize = src.batchSize
	r.inputDelay = src.inputDelay
	r.fsInKHz = src.fsInKHz
	r.fsOutKHz = src.fsOutKHz

	if src.delayBuf == nil {
		r.delayBuf = nil
		return
	}
	if len(r.delayBuf) != len(src.delayBuf) {
		r.delayBuf = make([]int16, len(src.delayBuf))
	}
	copy(r.delayBuf, src.delayBuf)
}

// Process resamples float32 samples from input rate to output rate.
// This implements the exact libopus silk_resampler() flow with delay buffer.
func (r *LibopusResampler) Process(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Debug: capture state at start of Process() call
	if r.debugEnabled {
		r.debugProcessCallCount++
		r.debugLastProcessID = r.debugID
		r.debugProcessCallSIIR = r.sIIR
		for i := 0; i < 10 && i < len(samples); i++ {
			r.debugInputFirst10[i] = samples[i]
		}
		for i := 0; i < 8 && i < len(r.delayBuf); i++ {
			r.debugDelayBufFirst8[i] = r.delayBuf[i]
		}
	}

	inLen := int32(len(samples))

	// Need at least 1 ms of input data
	if inLen < r.fsInKHz {
		// Pad with zeros if needed - use scratch buffer if available
		paddedLen := int(r.fsInKHz)
		var padded []float32
		if r.scratchResult != nil && len(r.scratchResult) >= paddedLen {
			padded = r.scratchResult[:paddedLen]
			for i := range padded {
				padded[i] = 0
			}
		} else {
			padded = make([]float32, paddedLen)
		}
		copy(padded, samples)
		samples = padded
		inLen = r.fsInKHz
	}

	// Convert float32 to int16 for processing - use scratch buffer if available
	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= len(samples) {
		in = r.scratchIn[:len(samples)]
	} else {
		in = make([]int16, len(samples))
	}
	for i, s := range samples {
		in[i] = float32ToInt16(s)
	}

	// Calculate output size
	outLen := int(inLen) * int(r.fsOutKHz) / int(r.fsInKHz)

	// Use scratch buffer for output if available
	var out []int16
	if r.scratchOut != nil && len(r.scratchOut) >= outLen {
		out = r.scratchOut[:outLen]
		for i := range out {
			out[i] = 0
		}
	} else {
		out = make([]int16, outLen)
	}

	// Match libopus silk_resampler() exactly:
	// 1. Fill delay buffer with first samples
	// 2. Process delay buffer (1ms worth)
	// 3. Process remaining input
	// 4. Save last samples to delay buffer

	nSamples := r.fsInKHz - r.inputDelay

	// Copy first nSamples to delay buffer
	copy(r.delayBuf[r.inputDelay:], in[:nSamples])

	// Process delay buffer (1ms = fsInKHz samples)
	r.resampleIIRFIRSliceWithScratch(out[:r.fsOutKHz], r.delayBuf[:r.fsInKHz], r.scratchBuf)

	// Process remaining input (exclude the last inputDelay samples, which are saved for next call)
	if inLen > r.fsInKHz {
		end := inLen - r.inputDelay
		if end < nSamples {
			end = nSamples
		}
		if end > inLen {
			end = inLen
		}
		r.resampleIIRFIRSliceWithScratch(out[r.fsOutKHz:], in[nSamples:end], r.scratchBuf)
	}

	// Save last inputDelay samples to delay buffer for next call
	if r.inputDelay > 0 {
		copy(r.delayBuf[:r.inputDelay], in[inLen-r.inputDelay:])
	}

	// Convert back to float32 - use scratch buffer if available
	var result []float32
	if r.scratchResult != nil && len(r.scratchResult) >= len(out) {
		result = r.scratchResult[:len(out)]
	} else {
		result = make([]float32, len(out))
	}
	for i, s := range out {
		result[i] = float32(s) / 32768.0
	}

	// Debug: capture output first 10 samples
	if r.debugEnabled {
		for i := 0; i < 10 && i < len(result); i++ {
			r.debugOutputFirst10[i] = result[i]
		}
	}

	return result
}

// ProcessInto resamples float32 samples from input rate to output rate into a caller-provided buffer.
// This is the zero-allocation version of Process().
// Returns the number of samples written to the output buffer.
func (r *LibopusResampler) ProcessInto(samples []float32, out []float32) int {
	if len(samples) == 0 {
		return 0
	}

	// Debug: capture state at start of Process() call
	if r.debugEnabled {
		r.debugProcessCallCount++
		r.debugLastProcessID = r.debugID
		r.debugProcessCallSIIR = r.sIIR
		for i := 0; i < 10 && i < len(samples); i++ {
			r.debugInputFirst10[i] = samples[i]
		}
		for i := 0; i < 8 && i < len(r.delayBuf); i++ {
			r.debugDelayBufFirst8[i] = r.delayBuf[i]
		}
	}

	inLen := int32(len(samples))

	// Need at least 1 ms of input data
	if inLen < r.fsInKHz {
		// Pad with zeros if needed - use scratch buffer if available
		paddedLen := int(r.fsInKHz)
		var padded []float32
		if r.scratchResult != nil && len(r.scratchResult) >= paddedLen {
			padded = r.scratchResult[:paddedLen]
			for i := range padded {
				padded[i] = 0
			}
		} else {
			// Fall back to temporary allocation (rare edge case)
			padded = make([]float32, paddedLen)
		}
		copy(padded, samples)
		samples = padded
		inLen = r.fsInKHz
	}

	// Convert float32 to int16 for processing - use scratch buffer if available
	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= len(samples) {
		in = r.scratchIn[:len(samples)]
	} else {
		// Fall back to temporary allocation (rare edge case)
		in = make([]int16, len(samples))
	}
	for i, s := range samples {
		in[i] = float32ToInt16(s)
	}

	// Calculate output size
	outLen := int(inLen) * int(r.fsOutKHz) / int(r.fsInKHz)

	// Use scratch buffer for intermediate output
	var outInt16 []int16
	if r.scratchOut != nil && len(r.scratchOut) >= outLen {
		outInt16 = r.scratchOut[:outLen]
		for i := range outInt16 {
			outInt16[i] = 0
		}
	} else {
		// Fall back to temporary allocation (rare edge case)
		outInt16 = make([]int16, outLen)
	}

	// Match libopus silk_resampler() exactly:
	// 1. Fill delay buffer with first samples
	// 2. Process delay buffer (1ms worth)
	// 3. Process remaining input
	// 4. Save last samples to delay buffer

	nSamples := r.fsInKHz - r.inputDelay

	// Copy first nSamples to delay buffer
	copy(r.delayBuf[r.inputDelay:], in[:nSamples])

	// Process delay buffer (1ms = fsInKHz samples)
	r.resampleIIRFIRSliceWithScratch(outInt16[:r.fsOutKHz], r.delayBuf[:r.fsInKHz], r.scratchBuf)

	// Process remaining input (exclude the last inputDelay samples, which are saved for next call)
	if inLen > r.fsInKHz {
		end := inLen - r.inputDelay
		if end < nSamples {
			end = nSamples
		}
		if end > inLen {
			end = inLen
		}
		r.resampleIIRFIRSliceWithScratch(outInt16[r.fsOutKHz:], in[nSamples:end], r.scratchBuf)
	}

	// Save last inputDelay samples to delay buffer for next call
	if r.inputDelay > 0 {
		copy(r.delayBuf[:r.inputDelay], in[inLen-r.inputDelay:])
	}

	// Convert back to float32 directly into output buffer
	written := outLen
	if written > len(out) {
		written = len(out)
	}
	for i := 0; i < written; i++ {
		out[i] = float32(outInt16[i]) / 32768.0
	}

	// Debug: capture output first 10 samples
	if r.debugEnabled {
		for i := 0; i < 10 && i < written; i++ {
			r.debugOutputFirst10[i] = out[i]
		}
	}

	return written
}

// up2HQ implements silk_resampler_private_up2_HQ.
// 2x upsampling using 3rd order allpass filters.
func (r *LibopusResampler) up2HQ(out []int16, in []int16) {
	for k := 0; k < len(in); k++ {
		// Convert to Q10
		in32 := int32(in[k]) << 10

		// First all-pass section for even output sample
		Y := in32 - r.sIIR[0]
		X := smulwb(Y, int32(silkResamplerUp2HQ0[0]))
		out32_1 := r.sIIR[0] + X
		r.sIIR[0] = in32 + X

		// Second all-pass section for even output sample
		Y = out32_1 - r.sIIR[1]
		X = smulwb(Y, int32(silkResamplerUp2HQ0[1]))
		out32_2 := r.sIIR[1] + X
		r.sIIR[1] = out32_1 + X

		// Third all-pass section for even output sample
		Y = out32_2 - r.sIIR[2]
		X = smlawb(Y, Y, int32(silkResamplerUp2HQ0[2]))
		out32_1 = r.sIIR[2] + X
		r.sIIR[2] = out32_2 + X

		// Convert back to int16 and store even sample
		out[2*k] = int16(sat16(rshiftRound(out32_1, 10)))

		// First all-pass section for odd output sample
		Y = in32 - r.sIIR[3]
		X = smulwb(Y, int32(silkResamplerUp2HQ1[0]))
		out32_1 = r.sIIR[3] + X
		r.sIIR[3] = in32 + X

		// Second all-pass section for odd output sample
		Y = out32_1 - r.sIIR[4]
		X = smulwb(Y, int32(silkResamplerUp2HQ1[1]))
		out32_2 = r.sIIR[4] + X
		r.sIIR[4] = out32_1 + X

		// Third all-pass section for odd output sample
		Y = out32_2 - r.sIIR[5]
		X = smlawb(Y, Y, int32(silkResamplerUp2HQ1[2]))
		out32_1 = r.sIIR[5] + X
		r.sIIR[5] = out32_2 + X

		// Convert back to int16 and store odd sample
		out[2*k+1] = int16(sat16(rshiftRound(out32_1, 10)))
	}
}

// firInterpol implements silk_resampler_private_IIR_FIR_INTERPOL.
// FIR interpolation using the 12-phase coefficient table.
func (r *LibopusResampler) firInterpol(out []int16, outIdx int, buf []int16, maxIndexQ16 int32) int {
	indexIncrQ16 := r.invRatioQ16

	for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrQ16 {
		// Get fractional position for table lookup (0-11)
		tableIndex := smulwb(indexQ16&0xFFFF, 12)

		// Get integer sample position in buffer
		bufIdx := int(indexQ16 >> 16)

		// 8-tap symmetric FIR filter
		resQ15 := smulbb(int32(buf[bufIdx+0]), int32(silkResamplerFracFIR12[tableIndex][0]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+1]), int32(silkResamplerFracFIR12[tableIndex][1]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+2]), int32(silkResamplerFracFIR12[tableIndex][2]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+3]), int32(silkResamplerFracFIR12[tableIndex][3]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+4]), int32(silkResamplerFracFIR12[11-tableIndex][3]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+5]), int32(silkResamplerFracFIR12[11-tableIndex][2]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+6]), int32(silkResamplerFracFIR12[11-tableIndex][1]))
		resQ15 = smlabb(resQ15, int32(buf[bufIdx+7]), int32(silkResamplerFracFIR12[11-tableIndex][0]))

		if outIdx < len(out) {
			out[outIdx] = int16(sat16(rshiftRound(resQ15, 15)))
			outIdx++
		}
	}

	return outIdx
}

// Fixed-point arithmetic helpers matching libopus SigProc_FIX.h

// smulwb: (a * b[15:0]) >> 16, treating b as signed 16-bit
func smulwb(a, b int32) int32 {
	return silkSMULWB(a, b)
}

// smulww: (a * b) >> 16
func smulww(a, b int32) int32 {
	return silkSMULWW(a, b)
}

// smulbb: a[15:0] * b[15:0], both treated as signed 16-bit
func smulbb(a, b int32) int32 {
	return silkSMULBB(a, b)
}

// smlabb: a + smulbb(b, c)
func smlabb(a, b, c int32) int32 {
	return silkSMLABB(a, b, c)
}

// smlawb: a + smulwb(b, c)
func smlawb(a, b, c int32) int32 {
	return silkSMLAWB(a, b, c)
}

// sat16: saturate to 16-bit range
func sat16(x int32) int16 {
	return silkSAT16(x)
}

// rshiftRound: (x + (1 << (shift-1))) >> shift with rounding
func rshiftRound(x int32, shift int) int32 {
	return silkRSHIFT_ROUND(x, shift)
}

// min32 returns the minimum of two int32 values.
func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// Debug getters for testing
func (r *LibopusResampler) InputDelay() int        { return int(r.inputDelay) }
func (r *LibopusResampler) FsInKHz() int           { return int(r.fsInKHz) }
func (r *LibopusResampler) FsOutKHz() int          { return int(r.fsOutKHz) }
func (r *LibopusResampler) InvRatioQ16() int       { return int(r.invRatioQ16) }
func (r *LibopusResampler) BatchSize() int         { return int(r.batchSize) }
func (r *LibopusResampler) GetSIIR() [6]int32      { return r.sIIR }
func (r *LibopusResampler) GetSFIR() [8]int16      { return r.sFIR }
func (r *LibopusResampler) GetDelayBuf() []int16   { return r.delayBuf }
func (r *LibopusResampler) SetSIIR(state [6]int32) { r.sIIR = state }
func (r *LibopusResampler) EnableDebug(enable bool) {
	r.debugEnabled = enable
	r.debugProcessCallCount = 0
}
func (r *LibopusResampler) GetDebugProcessCallSIIR() [6]int32  { return r.debugProcessCallSIIR }
func (r *LibopusResampler) GetDebugInputFirst10() [10]float32  { return r.debugInputFirst10 }
func (r *LibopusResampler) GetDebugDelayBufFirst8() [8]int16   { return r.debugDelayBufFirst8 }
func (r *LibopusResampler) GetDebugProcessCallCount() int      { return r.debugProcessCallCount }
func (r *LibopusResampler) GetDebugID() int                    { return r.debugID }
func (r *LibopusResampler) GetDebugLastProcessID() int         { return r.debugLastProcessID }
func (r *LibopusResampler) GetDebugOutputFirst10() [10]float32 { return r.debugOutputFirst10 }
