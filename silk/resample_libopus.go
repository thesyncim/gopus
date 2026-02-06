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

// resampleIIRFIRSliceWithScratch is like resampleIIRFIRSlice but uses a pre-allocated scratch buffer.
func (r *LibopusResampler) resampleIIRFIRSliceWithScratch(out []int16, in []int16, scratch []int16) {
	inLen := int32(len(in))
	outIdx := 0

	// Use pre-allocated buffer for 2x upsampled data + FIR history
	bufSize := int(2*r.batchSize + resamplerOrderFIR12)
	var buf []int16
	if scratch != nil && len(scratch) >= bufSize {
		buf = scratch[:bufSize]
	} else if r.scratchBuf != nil && len(r.scratchBuf) >= bufSize {
		buf = r.scratchBuf[:bufSize]
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

func (r *LibopusResampler) debugCaptureInputFloat32(samples []float32) {
	if !r.debugEnabled {
		return
	}
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

func (r *LibopusResampler) debugCaptureInputInt16(samples []int16) {
	if !r.debugEnabled {
		return
	}
	r.debugProcessCallCount++
	r.debugLastProcessID = r.debugID
	r.debugProcessCallSIIR = r.sIIR
	for i := 0; i < 10 && i < len(samples); i++ {
		r.debugInputFirst10[i] = float32(samples[i]) / 32768.0
	}
	for i := 0; i < 8 && i < len(r.delayBuf); i++ {
		r.debugDelayBufFirst8[i] = r.delayBuf[i]
	}
}

func (r *LibopusResampler) debugCaptureOutput(out []float32, written int) {
	if !r.debugEnabled {
		return
	}
	for i := 0; i < 10 && i < written && i < len(out); i++ {
		r.debugOutputFirst10[i] = out[i]
	}
}

// prepareInputFromFloat32 converts input to int16 and pads to >=1ms if needed.
func (r *LibopusResampler) prepareInputFromFloat32(samples []float32) ([]int16, int32) {
	inLen := int32(len(samples))
	neededLen := len(samples)
	if inLen < r.fsInKHz {
		neededLen = int(r.fsInKHz)
	}

	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= neededLen {
		in = r.scratchIn[:neededLen]
	} else {
		in = make([]int16, neededLen)
	}

	for i, s := range samples {
		in[i] = float32ToInt16(s)
	}
	if neededLen > len(samples) {
		clear(in[len(samples):])
		inLen = r.fsInKHz
	}
	return in, inLen
}

// prepareInputFromInt16 pads int16 input to >=1ms if needed.
func (r *LibopusResampler) prepareInputFromInt16(samples []int16) ([]int16, int32) {
	inLen := int32(len(samples))
	if inLen >= r.fsInKHz {
		return samples, inLen
	}

	neededLen := int(r.fsInKHz)
	var in []int16
	if r.scratchIn != nil && len(r.scratchIn) >= neededLen {
		in = r.scratchIn[:neededLen]
		clear(in)
	} else {
		in = make([]int16, neededLen)
	}
	copy(in, samples)
	return in, r.fsInKHz
}

// processInt16Core runs the libopus-matching resampler core for int16 input.
func (r *LibopusResampler) processInt16Core(in []int16, inLen int32) []int16 {
	outLen := int(inLen) * int(r.fsOutKHz) / int(r.fsInKHz)
	var outInt16 []int16
	if r.scratchOut != nil && len(r.scratchOut) >= outLen {
		outInt16 = r.scratchOut[:outLen]
	} else {
		outInt16 = make([]int16, outLen)
	}

	// Match libopus silk_resampler() flow:
	// 1) Prime delay buffer with current input
	// 2) Process delay buffer (1 ms)
	// 3) Process remaining input
	// 4) Preserve tail in delay buffer
	nSamples := r.fsInKHz - r.inputDelay
	copy(r.delayBuf[int(r.inputDelay):], in[:int(nSamples)])
	r.resampleIIRFIRSliceWithScratch(outInt16[:int(r.fsOutKHz)], r.delayBuf[:int(r.fsInKHz)], r.scratchBuf)

	if inLen > r.fsInKHz {
		end := inLen - r.inputDelay
		if end < nSamples {
			end = nSamples
		}
		if end > inLen {
			end = inLen
		}
		r.resampleIIRFIRSliceWithScratch(outInt16[int(r.fsOutKHz):], in[int(nSamples):int(end)], r.scratchBuf)
	}

	if r.inputDelay > 0 {
		copy(r.delayBuf[:int(r.inputDelay)], in[int(inLen-r.inputDelay):int(inLen)])
	}

	return outInt16
}

func writeInt16AsFloat32(dst []float32, src []int16) int {
	written := len(src)
	if written > len(dst) {
		written = len(dst)
	}
	for i := 0; i < written; i++ {
		dst[i] = float32(src[i]) / 32768.0
	}
	return written
}

// Process resamples float32 samples from input rate to output rate.
// This implements the exact libopus silk_resampler() flow with delay buffer.
func (r *LibopusResampler) Process(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	r.debugCaptureInputFloat32(samples)
	in, inLen := r.prepareInputFromFloat32(samples)
	outInt16 := r.processInt16Core(in, inLen)

	var result []float32
	if r.scratchResult != nil && len(r.scratchResult) >= len(outInt16) {
		result = r.scratchResult[:len(outInt16)]
	} else {
		result = make([]float32, len(outInt16))
	}
	written := writeInt16AsFloat32(result, outInt16)
	r.debugCaptureOutput(result, written)
	return result[:written]
}

// ProcessInto resamples float32 samples from input rate to output rate into a caller-provided buffer.
// This is the zero-allocation version of Process().
// Returns the number of samples written to the output buffer.
func (r *LibopusResampler) ProcessInto(samples []float32, out []float32) int {
	if len(samples) == 0 {
		return 0
	}

	r.debugCaptureInputFloat32(samples)
	in, inLen := r.prepareInputFromFloat32(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(out, outInt16)
	r.debugCaptureOutput(out, written)
	return written
}

// ProcessInt16Into resamples int16 input samples into float32 output.
// This avoids float32->int16 conversion when the caller already has native int16 samples.
func (r *LibopusResampler) ProcessInt16Into(samples []int16, out []float32) int {
	if len(samples) == 0 {
		return 0
	}

	r.debugCaptureInputInt16(samples)
	in, inLen := r.prepareInputFromInt16(samples)
	outInt16 := r.processInt16Core(in, inLen)
	written := writeInt16AsFloat32(out, outInt16)
	r.debugCaptureOutput(out, written)
	return written
}

// up2HQ implements silk_resampler_private_up2_HQ.
// 2x upsampling using 3rd order allpass filters.
func (r *LibopusResampler) up2HQ(out []int16, in []int16) {
	n := len(in)
	if n == 0 {
		return
	}

	// Keep allpass filter state in locals during the hot loop.
	s0, s1, s2 := r.sIIR[0], r.sIIR[1], r.sIIR[2]
	s3, s4, s5 := r.sIIR[3], r.sIIR[4], r.sIIR[5]

	c00 := int32(silkResamplerUp2HQ0[0])
	c01 := int32(silkResamplerUp2HQ0[1])
	c02 := int32(silkResamplerUp2HQ0[2])
	c10 := int32(silkResamplerUp2HQ1[0])
	c11 := int32(silkResamplerUp2HQ1[1])
	c12 := int32(silkResamplerUp2HQ1[2])

	_ = out[2*n-1]

	outPos := 0
	for k := 0; k < n; k++ {
		// Convert to Q10
		in32 := int32(in[k]) << 10

		// First all-pass section for even output sample
		Y := in32 - s0
		X := smulwb(Y, c00)
		out32_1 := s0 + X
		s0 = in32 + X

		// Second all-pass section for even output sample
		Y = out32_1 - s1
		X = smulwb(Y, c01)
		out32_2 := s1 + X
		s1 = out32_1 + X

		// Third all-pass section for even output sample
		Y = out32_2 - s2
		X = smlawb(Y, Y, c02)
		out32_1 = s2 + X
		s2 = out32_2 + X

		// Convert back to int16 and store even sample
		out[outPos] = int16(sat16(rshiftRound(out32_1, 10)))

		// First all-pass section for odd output sample
		Y = in32 - s3
		X = smulwb(Y, c10)
		out32_1 = s3 + X
		s3 = in32 + X

		// Second all-pass section for odd output sample
		Y = out32_1 - s4
		X = smulwb(Y, c11)
		out32_2 = s4 + X
		s4 = out32_1 + X

		// Third all-pass section for odd output sample
		Y = out32_2 - s5
		X = smlawb(Y, Y, c12)
		out32_1 = s5 + X
		s5 = out32_2 + X

		// Convert back to int16 and store odd sample
		out[outPos+1] = int16(sat16(rshiftRound(out32_1, 10)))
		outPos += 2
	}

	r.sIIR[0], r.sIIR[1], r.sIIR[2] = s0, s1, s2
	r.sIIR[3], r.sIIR[4], r.sIIR[5] = s3, s4, s5
}

// firInterpol implements silk_resampler_private_IIR_FIR_INTERPOL.
// FIR interpolation using the 12-phase coefficient table.
func (r *LibopusResampler) firInterpol(out []int16, outIdx int, buf []int16, maxIndexQ16 int32) int {
	indexIncrQ16 := r.invRatioQ16
	if maxIndexQ16 <= 0 || indexIncrQ16 <= 0 || outIdx >= len(out) {
		return outIdx
	}

	// Number of interpolation points generated by:
	// for indexQ16 := 0; indexQ16 < maxIndexQ16; indexQ16 += indexIncrQ16
	nOut := int((maxIndexQ16 + indexIncrQ16 - 1) / indexIncrQ16)
	remain := len(out) - outIdx
	if nOut > remain {
		nOut = remain
	}
	if nOut <= 0 {
		return outIdx
	}

	// BCE hints for hot inner-loop accesses.
	_ = out[outIdx+nOut-1]
	lastIndexQ16 := int32(nOut-1) * indexIncrQ16
	_ = buf[int(lastIndexQ16>>16)+7]

	outPos := outIdx
	for indexQ16, n := int32(0), 0; n < nOut; n, indexQ16 = n+1, indexQ16+indexIncrQ16 {
		// Fractional position for table lookup (0..11), matching libopus smulwb(indexQ16&0xFFFF, 12).
		tableIndex := int((uint32(indexQ16&0xFFFF) * 12) >> 16)
		bufIdx := int(indexQ16 >> 16)

		// 8-tap symmetric FIR filter.
		c0 := silkResamplerFracFIR12[tableIndex]
		c1 := silkResamplerFracFIR12[11-tableIndex]
		resQ15 := int32(buf[bufIdx+0]) * int32(c0[0])
		resQ15 += int32(buf[bufIdx+1]) * int32(c0[1])
		resQ15 += int32(buf[bufIdx+2]) * int32(c0[2])
		resQ15 += int32(buf[bufIdx+3]) * int32(c0[3])
		resQ15 += int32(buf[bufIdx+4]) * int32(c1[3])
		resQ15 += int32(buf[bufIdx+5]) * int32(c1[2])
		resQ15 += int32(buf[bufIdx+6]) * int32(c1[1])
		resQ15 += int32(buf[bufIdx+7]) * int32(c1[0])

		out[outPos] = int16(silkSAT16(silkRSHIFT_ROUND(resQ15, 15)))
		outPos++
	}

	return outPos
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

// sat16: saturate to 16-bit range.
func sat16(x int32) int16 {
	return silkSAT16(x)
}

// rshiftRound: (x + (1 << (shift-1))) >> shift with rounding.
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
