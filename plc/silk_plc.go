package plc

import (
	"math"
	"math/bits"
)

// Constants from libopus silk/PLC.h
const (
	// ltpOrder is the number of LTP filter taps (5-tap filter).
	ltpOrder = 5

	// maxLPCOrder is the maximum LPC order (16 for WB, 10 for NB/MB).
	maxLPCOrder = 16

	// bweCoef is the bandwidth expansion coefficient for LPC during PLC.
	// Applied to prevent filter instability during concealment.
	bweCoef = 0.99

	// vPitchGainStartMinQ14 is the minimum LTP gain (0.7 in Q14).
	// LTP gains below this are scaled up for better concealment.
	vPitchGainStartMinQ14 = 11469

	// vPitchGainStartMaxQ14 is the maximum LTP gain (0.95 in Q14).
	// LTP gains above this are scaled down to prevent instability.
	vPitchGainStartMaxQ14 = 15565

	// maxPitchLagMs is the maximum pitch lag in milliseconds.
	maxPitchLagMs = 18

	// randBufSize is the size of the random noise buffer.
	randBufSize = 128

	// randBufMask is used for random buffer index masking.
	randBufMask = randBufSize - 1

	// pitchDriftFacQ16 is the pitch lag drift factor (0.01 in Q16).
	// Pitch lag slowly increases during extended loss.
	pitchDriftFacQ16 = 655

	// log2InvLPCGainHighThres/log2InvLPCGainLowThres mirror libopus PLC.h.
	log2InvLPCGainHighThres = 3
	log2InvLPCGainLowThres  = 8

	// Constants for fixed-point LPC inverse prediction gain.
	lpcInvPredQA        = 24
	lpcInvPredALimitQ24 = 16773023 // SILK_FIX_CONST(0.99975, 24)
	minInvPredGainQ30   = 107374   // SILK_FIX_CONST(1 / 1e4, 30)

	// Attenuation constants (Q15 format)
	harmAttQ15_0    = 32440 // 0.99 - first lost frame
	harmAttQ15_1    = 31130 // 0.95 - subsequent frames
	randAttVQ15_0   = 31130 // 0.95 - voiced first frame
	randAttVQ15_1   = 26214 // 0.8 - voiced subsequent
	randAttUVQ15_0  = 32440 // 0.99 - unvoiced first frame
	randAttUVQ15_1  = 29491 // 0.9 - unvoiced subsequent
)

// SILKDecoderState provides access to SILK decoder state needed for PLC.
// This interface allows PLC to access decoder state without importing the silk package.
type SILKDecoderState interface {
	// PrevLPCValues returns the LPC filter state from the last frame.
	PrevLPCValues() []float32
	// LPCOrder returns the current LPC order (10 for NB/MB, 16 for WB).
	LPCOrder() int
	// IsPreviousFrameVoiced returns true if the last frame was voiced.
	IsPreviousFrameVoiced() bool
	// OutputHistory returns the output history buffer for pitch prediction.
	OutputHistory() []float32
	// HistoryIndex returns the current position in the history buffer.
	HistoryIndex() int
}

// SILKPitchLagProvider optionally exposes the previous pitch lag from the
// underlying decoder state.
type SILKPitchLagProvider interface {
	GetLagPrev() int
}

// SILKSignalTypeProvider optionally exposes libopus signal type tracking.
// Returns 0=inactive, 1=unvoiced, 2=voiced.
type SILKSignalTypeProvider interface {
	GetLastSignalType() int
}

// SILKSLPCQ14Provider optionally exposes the decoder's LPC synthesis history
// buffer in Q14 (most recent lpcOrder samples).
type SILKSLPCQ14Provider interface {
	GetSLPCQ14HistoryQ14() []int32
}

// SILKOutBufProvider optionally exposes decoder outBuf history in Q0
// (typically the last ltp_mem_length samples used by libopus PLC rewhitening).
type SILKOutBufProvider interface {
	GetOutBufHistoryQ0() []int16
}

// SILKDecoderStateExtended provides extended SILK decoder state access for LTP-aware PLC.
// Implementations should provide this interface for full LTP coefficient support.
type SILKDecoderStateExtended interface {
	SILKDecoderState

	// GetLastSignalType returns 0=inactive, 1=unvoiced, 2=voiced.
	GetLastSignalType() int

	// GetLTPCoefficients returns the LTP coefficients from the last good frame.
	// Returns 5 coefficients in Q14 format.
	GetLTPCoefficients() [ltpOrder]int16

	// GetPitchLag returns the pitch lag from the last good frame.
	GetPitchLag() int

	// GetLastGain returns the gain from the last subframe (Q16 format).
	GetLastGain() int32

	// GetLTPScale returns the LTP scale factor (Q14 format).
	GetLTPScale() int32

	// GetExcitationHistory returns the excitation signal history (Q14 format).
	GetExcitationHistory() []int32

	// GetLPCCoefficientsQ12 returns LPC coefficients in Q12 format.
	GetLPCCoefficientsQ12() []int16

	// GetSampleRateKHz returns the sample rate in kHz (8, 12, or 16).
	GetSampleRateKHz() int

	// GetSubframeLength returns the subframe length in samples.
	GetSubframeLength() int

	// GetNumSubframes returns the number of subframes (2 or 4).
	GetNumSubframes() int

	// GetLTPMemoryLength returns the LTP memory length in samples.
	GetLTPMemoryLength() int
}

// SILKPLCState stores persistent state for SILK PLC across lost frames.
// This mirrors the silk_PLC_struct from libopus.
type SILKPLCState struct {
	// LTP coefficients from the last good voiced frame (Q14)
	LTPCoefQ14 [ltpOrder]int16

	// Pitch lag in Q8 format for sub-sample precision
	PitchLQ8 int32

	// Previous gains from last 2 subframes (Q16)
	PrevGainQ16 [2]int32

	// Previous LPC coefficients (Q12)
	PrevLPCQ12 [maxLPCOrder]int16

	// Previous LTP scale factor (Q14)
	PrevLTPScaleQ14 int32

	// Random scale for noise mixing (Q14)
	RandScaleQ14 int16

	// Random seed for noise generation
	RandSeed int32

	// Sample rate in kHz
	FsKHz int

	// Subframe length
	SubfrLength int

	// Number of subframes
	NbSubfr int

	// LPC order
	LPCOrder int

	// Concealed frame energy for glue frames
	ConcEnergy      int32
	ConcEnergyShift int

	// Flag indicating if last frame was lost
	LastFrameLost bool
}

// NewSILKPLCState creates a new SILK PLC state with default values.
func NewSILKPLCState() *SILKPLCState {
	return &SILKPLCState{
		// Default pitch lag: half frame length in Q8
		// 160 samples * 256 / 2 = 20480 (10ms at 16kHz)
		PitchLQ8: 160 << 7, // 160 samples = 10ms at 16kHz

		// Initialize gains to 1.0 (Q16)
		PrevGainQ16: [2]int32{1 << 16, 1 << 16},

		// Default subframe parameters
		SubfrLength: 80, // 5ms at 16kHz
		NbSubfr:     4,
		FsKHz:       16,
		LPCOrder:    16,

		// Initial random scale (1.0 in Q14)
		RandScaleQ14: 1 << 14,

		// Initial random seed
		RandSeed: 22222,
	}
}

// Reset resets the PLC state for a new stream.
func (s *SILKPLCState) Reset(frameLength int) {
	s.PitchLQ8 = int32(frameLength) << 7 // Half frame length in Q8

	s.PrevGainQ16[0] = 1 << 16
	s.PrevGainQ16[1] = 1 << 16

	s.SubfrLength = 80
	s.NbSubfr = 4

	for i := range s.LTPCoefQ14 {
		s.LTPCoefQ14[i] = 0
	}

	for i := range s.PrevLPCQ12 {
		s.PrevLPCQ12[i] = 0
	}

	s.PrevLTPScaleQ14 = 0
	s.RandScaleQ14 = 1 << 14
	s.RandSeed = 22222
	s.LastFrameLost = false
}

// UpdateFromGoodFrame updates PLC state from a successfully decoded frame.
// This should be called after each good frame to prepare for potential future losses.
// This mirrors silk_PLC_update from libopus.
func (s *SILKPLCState) UpdateFromGoodFrame(
	signalType int, // 0=inactive, 1=unvoiced, 2=voiced
	pitchL []int, // Pitch lags for each subframe
	ltpCoefQ14 []int16, // LTP coefficients for all subframes (nbSubfr * ltpOrder)
	ltpScaleQ14 int32, // LTP scale factor
	gainsQ16 []int32, // Gains for each subframe (Q16)
	lpcQ12 []int16, // LPC coefficients (Q12)
	fsKHz int,
	nbSubfr int,
	subfrLength int,
) {
	s.FsKHz = fsKHz
	s.SubfrLength = subfrLength
	s.NbSubfr = nbSubfr
	s.LPCOrder = len(lpcQ12)

	// Save LPC coefficients
	copy(s.PrevLPCQ12[:], lpcQ12)

	// Save last two gains
	if len(gainsQ16) >= 2 {
		s.PrevGainQ16[0] = gainsQ16[len(gainsQ16)-2]
		s.PrevGainQ16[1] = gainsQ16[len(gainsQ16)-1]
	}

	if signalType == 2 { // Voiced
		// Find the subframe with the highest LTP gain to use for concealment
		var ltpGainQ14 int32
		var tempLtpGainQ14 int32

		for j := 0; j*subfrLength < pitchL[nbSubfr-1] && j < nbSubfr; j++ {
			tempLtpGainQ14 = 0
			subfrIdx := nbSubfr - 1 - j

			// Sum LTP coefficients for this subframe
			for i := 0; i < ltpOrder; i++ {
				tempLtpGainQ14 += int32(ltpCoefQ14[subfrIdx*ltpOrder+i])
			}

			if tempLtpGainQ14 > ltpGainQ14 {
				ltpGainQ14 = tempLtpGainQ14

				// Copy LTP coefficients from this subframe
				for i := 0; i < ltpOrder; i++ {
					s.LTPCoefQ14[i] = ltpCoefQ14[subfrIdx*ltpOrder+i]
				}

				// Save pitch lag in Q8
				s.PitchLQ8 = int32(pitchL[subfrIdx]) << 8
			}
		}

		// Center the LTP gain on the middle tap (like libopus)
		for i := range s.LTPCoefQ14 {
			s.LTPCoefQ14[i] = 0
		}
		s.LTPCoefQ14[ltpOrder/2] = int16(ltpGainQ14)

		// Limit LTP coefficients to valid range
		if ltpGainQ14 < vPitchGainStartMinQ14 {
			// Scale up if gain is too low
			if ltpGainQ14 > 0 {
				scaleQ10 := (vPitchGainStartMinQ14 << 10) / ltpGainQ14
				for i := range s.LTPCoefQ14 {
					s.LTPCoefQ14[i] = int16((int32(s.LTPCoefQ14[i]) * scaleQ10) >> 10)
				}
			}
		} else if ltpGainQ14 > vPitchGainStartMaxQ14 {
			// Scale down if gain is too high
			scaleQ14 := (vPitchGainStartMaxQ14 << 14) / ltpGainQ14
			for i := range s.LTPCoefQ14 {
				s.LTPCoefQ14[i] = int16((int32(s.LTPCoefQ14[i]) * scaleQ14) >> 14)
			}
		}

		s.PrevLTPScaleQ14 = ltpScaleQ14
	} else {
		// Unvoiced: use default pitch lag
		s.PitchLQ8 = int32(fsKHz*18) << 8 // 18ms pitch lag
		for i := range s.LTPCoefQ14 {
			s.LTPCoefQ14[i] = 0
		}
		s.PrevLTPScaleQ14 = 0
	}

	s.LastFrameLost = false
}

// ConcealSILK generates concealment audio for a lost SILK frame.
//
// SILK PLC strategy (per RFC 6716 Section 4.2.8):
//  1. Reuse LPC coefficients from last frame
//  2. For voiced frames: continue pitch prediction with decaying gain
//  3. For unvoiced frames: generate comfort noise
//  4. Apply fade factor to output
//
// This provides smooth transitions during packet loss by maintaining
// the spectral characteristics of the last successfully decoded frame.
//
// Parameters:
//   - dec: SILK decoder state from last good frame
//   - frameSize: samples to generate at native SILK rate (8/12/16kHz)
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: concealed samples at native SILK rate
func ConcealSILK(dec SILKDecoderState, frameSize int, fadeFactor float64) []float32 {
	if dec == nil || frameSize <= 0 {
		return make([]float32, frameSize)
	}

	// If fade is effectively zero, return silence
	if fadeFactor < 0.001 {
		return make([]float32, frameSize)
	}

	output := make([]float32, frameSize)

	// Get state from decoder
	prevLPC := dec.PrevLPCValues()
	order := dec.LPCOrder()
	if order == 0 {
		order = 10 // Default NB/MB order
	}

	wasVoiced := dec.IsPreviousFrameVoiced()

	// RNG state for noise generation
	rng := uint32(22222)

	if wasVoiced {
		// Voiced PLC: use pitch repetition with LPC filtering
		// Get pitch information from history
		concealVoicedSILK(dec, output, prevLPC, order, fadeFactor, &rng)
	} else {
		// Unvoiced PLC: generate comfort noise filtered by LPC
		concealUnvoicedSILK(output, prevLPC, order, fadeFactor, &rng)
	}

	return output
}

// ConcealSILKWithLTP generates concealment using full LTP coefficient support.
// This is the enhanced version that uses LTP coefficients for better quality.
//
// Parameters:
//   - dec: Extended SILK decoder state from last good frame
//   - plcState: PLC state (will be updated)
//   - lossCnt: Number of consecutive lost frames (0 for first loss)
//   - frameSize: samples to generate at native SILK rate
//
// Returns: concealed samples at native SILK rate (int16 Q0 format)
func ConcealSILKWithLTP(dec SILKDecoderStateExtended, plcState *SILKPLCState, lossCnt int, frameSize int) []int16 {
	if dec == nil || plcState == nil || frameSize <= 0 {
		return make([]int16, frameSize)
	}

	fsKHz := dec.GetSampleRateKHz()
	if fsKHz == 0 {
		fsKHz = 16
	}

	nbSubfr := dec.GetNumSubframes()
	if nbSubfr == 0 {
		nbSubfr = 4
	}

	subfrLength := dec.GetSubframeLength()
	if subfrLength == 0 {
		subfrLength = 80
	}

	lpcOrder := dec.LPCOrder()
	if lpcOrder == 0 {
		lpcOrder = 16
	}

	ltpMemLength := dec.GetLTPMemoryLength()
	if ltpMemLength == 0 {
		ltpMemLength = 320
	}

	signalType := dec.GetLastSignalType()
	prevGainQ10 := [2]int32{
		plcState.PrevGainQ16[0] >> 6,
		plcState.PrevGainQ16[1] >> 6,
	}

	// Get attenuation factors based on loss count
	attIdx := lossCnt
	if attIdx > 1 {
		attIdx = 1
	}

	var harmGainQ15 int32
	var randGainQ15 int32

	if attIdx == 0 {
		harmGainQ15 = harmAttQ15_0
	} else {
		harmGainQ15 = harmAttQ15_1
	}

	if signalType == 2 { // Voiced
		if attIdx == 0 {
			randGainQ15 = randAttVQ15_0
		} else {
			randGainQ15 = randAttVQ15_1
		}
	} else {
		if attIdx == 0 {
			randGainQ15 = randAttUVQ15_0
		} else {
			randGainQ15 = randAttUVQ15_1
		}
	}

	// Apply bandwidth expansion to LPC coefficients for stability
	lpcQ12 := make([]int16, lpcOrder)
	copy(lpcQ12, plcState.PrevLPCQ12[:lpcOrder])
	bwExpandQ12(lpcQ12, bweCoef)

	// Initialize random scale on first lost frame
	randScaleQ14 := plcState.RandScaleQ14
	if lossCnt == 0 {
		randScaleQ14 = 1 << 14 // 1.0

		// For voiced frames, reduce noise based on LTP gain
		if signalType == 2 {
			for i := 0; i < ltpOrder; i++ {
				randScaleQ14 -= plcState.LTPCoefQ14[i]
			}
			if randScaleQ14 < 3277 { // 0.2 in Q14
				randScaleQ14 = 3277
			}
			// Apply LTP scale
			randScaleQ14 = int16((int32(randScaleQ14) * plcState.PrevLTPScaleQ14) >> 14)
		} else {
			// For unvoiced frames, reduce random gain for high LPC gain.
			// Mirrors libopus silk_PLC_conceal().
			invGainQ30 := lpcInversePredGainQ30(lpcQ12, lpcOrder)
			downScaleQ30 := minInt32((1<<30)>>log2InvLPCGainHighThres, invGainQ30)
			downScaleQ30 = maxInt32((1<<30)>>log2InvLPCGainLowThres, downScaleQ30)
			downScaleQ30 <<= log2InvLPCGainHighThres
			randGainQ15 = smulwb(downScaleQ30, randGainQ15) >> 14
		}
	}

	// Get pitch lag
	lag := int(plcState.PitchLQ8+128) >> 8

	// Prepare output buffers
	output := make([]int16, frameSize)

	// Generate excitation history buffer for random noise source
	excHistory := dec.GetExcitationHistory()
	randBuf := make([]int32, randBufSize)
	if len(excHistory) > 0 {
		// Use excitation from subframe with lower energy
		energy1, shift1 := computeEnergy(excHistory, prevGainQ10[0], subfrLength, (nbSubfr-2)*subfrLength)
		energy2, shift2 := computeEnergy(excHistory, prevGainQ10[1], subfrLength, (nbSubfr-1)*subfrLength)

		var randStart int
		if (energy1 >> uint(shift2)) < (energy2 >> uint(shift1)) {
			randStart = max(0, (nbSubfr-1)*subfrLength-randBufSize)
		} else {
			randStart = max(0, nbSubfr*subfrLength-randBufSize)
		}

		for i := 0; i < randBufSize && randStart+i < len(excHistory); i++ {
			randBuf[i] = excHistory[randStart+i]
		}
	}

	// LTP synthesis filtering
	sLTPQ15 := make([]int32, ltpMemLength+frameSize)
	sLTPBufIdx := ltpMemLength

	// Rewhiten LTP state using LPC analysis
	if signalType == 2 {
		startIdx := ltpMemLength - lag - lpcOrder - ltpOrder/2
		if startIdx <= 0 {
			startIdx = 1
		}

		// Perform LPC analysis to get sLTP.
		// Prefer decoder outBuf history (Q0), which matches libopus PLC inputs.
		sLTP := make([]int16, ltpMemLength)
		haveOutBufQ0 := false
		if provider, ok := dec.(SILKOutBufProvider); ok {
			outBufQ0 := provider.GetOutBufHistoryQ0()
			if len(outBufQ0) >= ltpMemLength && startIdx < ltpMemLength {
				lpcAnalysisFilterInt16(
					sLTP[startIdx:],
					outBufQ0[startIdx:ltpMemLength],
					lpcQ12,
					ltpMemLength-startIdx,
					lpcOrder,
				)
				haveOutBufQ0 = true
			}
		}
		if !haveOutBufQ0 {
			// Fallback for decoders that don't expose outBuf history.
			outHistory := dec.OutputHistory()
			if len(outHistory) > 0 {
				lpcAnalysisFilter(sLTP[startIdx:], outHistory, lpcQ12, ltpMemLength-startIdx, lpcOrder, startIdx)
			}
		}

		// Scale LTP state
		invGainQ30 := inverse32VarQ(plcState.PrevGainQ16[1], 46)
		if invGainQ30 > (1<<30 - 1) {
			invGainQ30 = 1<<30 - 1
		}

		for i := startIdx + lpcOrder; i < ltpMemLength; i++ {
			sLTPQ15[i] = smulwb(invGainQ30, int32(sLTP[i]))
		}
	}

	randSeed := plcState.RandSeed
	B_Q14 := plcState.LTPCoefQ14

	// Process each subframe
	sLPCQ14 := make([]int32, frameSize+maxLPCOrder)
	haveSLPCHistory := false
	if provider, ok := dec.(SILKSLPCQ14Provider); ok {
		historyQ14 := provider.GetSLPCQ14HistoryQ14()
		if len(historyQ14) >= lpcOrder {
			start := maxLPCOrder - lpcOrder
			copy(sLPCQ14[start:maxLPCOrder], historyQ14[:lpcOrder])
			haveSLPCHistory = true
		}
	}
	if !haveSLPCHistory {
		prev := dec.PrevLPCValues()
		if len(prev) >= lpcOrder {
			start := maxLPCOrder - lpcOrder
			for i := 0; i < lpcOrder; i++ {
				sLPCQ14[start+i] = int32(math.Round(float64(prev[i] * 16384.0)))
			}
		}
	}

	for k := 0; k < nbSubfr; k++ {
		// LTP prediction for voiced frames
		if signalType == 2 {
			predLagPtr := sLTPBufIdx - lag + ltpOrder/2

			for i := 0; i < subfrLength; i++ {
				// 5-tap LTP filter
				ltpPredQ12 := int32(2) // Rounding
				ltpPredQ12 = smlawb(ltpPredQ12, sLTPQ15[predLagPtr+0], int32(B_Q14[0]))
				ltpPredQ12 = smlawb(ltpPredQ12, sLTPQ15[predLagPtr-1], int32(B_Q14[1]))
				ltpPredQ12 = smlawb(ltpPredQ12, sLTPQ15[predLagPtr-2], int32(B_Q14[2]))
				ltpPredQ12 = smlawb(ltpPredQ12, sLTPQ15[predLagPtr-3], int32(B_Q14[3]))
				ltpPredQ12 = smlawb(ltpPredQ12, sLTPQ15[predLagPtr-4], int32(B_Q14[4]))
				predLagPtr++

				// Generate random noise
				randSeed = silkRand(randSeed)
				idx := (randSeed >> 25) & randBufMask
				randExc := randBuf[idx]

				// Combine LTP prediction with scaled noise
				sLTPQ15[sLTPBufIdx] = (ltpPredQ12 << 2) + smulwb(int32(randScaleQ14)<<2, randExc)
				sLTPBufIdx++
			}
		} else {
			// Unvoiced: just noise
			for i := 0; i < subfrLength; i++ {
				randSeed = silkRand(randSeed)
				idx := (randSeed >> 25) & randBufMask
				randExc := randBuf[idx]
				sLTPQ15[sLTPBufIdx] = smulwb(int32(randScaleQ14)<<2, randExc)
				sLTPBufIdx++
			}
		}

		// Attenuate LTP gain
		for j := 0; j < ltpOrder; j++ {
			B_Q14[j] = int16((int32(B_Q14[j]) * harmGainQ15) >> 15)
		}

		// Attenuate random scale
		randScaleQ14 = int16((int32(randScaleQ14) * randGainQ15) >> 15)

		// Increase pitch lag slowly
		plcState.PitchLQ8 = plcState.PitchLQ8 + ((plcState.PitchLQ8 * pitchDriftFacQ16) >> 16)
		maxLag := int32(maxPitchLagMs*fsKHz) << 8
		if plcState.PitchLQ8 > maxLag {
			plcState.PitchLQ8 = maxLag
		}
		lag = int(plcState.PitchLQ8+128) >> 8
	}

	// LPC synthesis filtering
	sLPCBufIdx := ltpMemLength - maxLPCOrder
	for i := 0; i < frameSize; i++ {
		// LPC prediction
		lpcPredQ10 := int32(lpcOrder >> 1) // Rounding
		for j := 0; j < lpcOrder; j++ {
			lpcPredQ10 = smlawb(lpcPredQ10, sLPCQ14[maxLPCOrder+i-j-1], int32(lpcQ12[j]))
		}

		// Add LTP excitation to LPC prediction
		sLPCQ14[maxLPCOrder+i] = addSat32(sLTPQ15[sLPCBufIdx+maxLPCOrder+i], lshiftSat32(lpcPredQ10, 4))

		// Scale with gain
		outVal := smulww(sLPCQ14[maxLPCOrder+i], prevGainQ10[1])
		output[i] = sat16(rshiftRound(outVal, 8))
	}

	// Update PLC state for next concealment
	plcState.RandScaleQ14 = randScaleQ14
	plcState.RandSeed = randSeed
	plcState.LTPCoefQ14 = B_Q14
	plcState.LastFrameLost = true

	return output
}

// concealVoicedSILK generates concealment for voiced (pitched) speech.
// It extrapolates the pitch pattern from previous frames.
func concealVoicedSILK(dec SILKDecoderState, output []float32, prevLPC []float32, order int, fade float64, rng *uint32) {
	// Get history for pitch repetition
	history := dec.OutputHistory()
	histIdx := dec.HistoryIndex()
	histLen := len(history)

	if histLen == 0 {
		// No history available, fall back to noise
		concealUnvoicedSILK(output, prevLPC, order, fade, rng)
		return
	}

	// Prefer decoder-tracked pitch lag (lagPrev) when available.
	pitchLag := 0
	if p, ok := dec.(SILKPitchLagProvider); ok {
		pitchLag = p.GetLagPrev()
	}
	if pitchLag <= 0 {
		// Fallback to autocorrelation estimate.
		pitchLag = estimatePitchFromHistory(history, histIdx, histLen)
	}
	if pitchLag < 10 {
		pitchLag = 80 // Default to ~5ms at 16kHz if estimation fails
	}

	// Generate voiced excitation by repeating pitch period
	excitation := make([]float32, len(output))
	for i := range excitation {
		// Get sample from pitch-delayed history
		srcIdx := histIdx - pitchLag + (i % pitchLag)
		for srcIdx < 0 {
			srcIdx += histLen
		}
		srcIdx = srcIdx % histLen

		// Copy with decay
		excitation[i] = history[srcIdx] * float32(fade)

		// Add small noise to prevent pure repetition artifacts
		*rng = *rng*1664525 + 1013904223
		noise := (float32(*rng>>16) - 32768.0) / 32768.0 * 0.01
		excitation[i] += noise * float32(fade)
	}

	// Apply simple smoothing to avoid harsh transitions
	for i := range output {
		output[i] = excitation[i]
	}
}

// concealUnvoicedSILK generates concealment for unvoiced (noise-like) speech.
// It produces comfort noise shaped by the previous LPC filter.
func concealUnvoicedSILK(output []float32, prevLPC []float32, order int, fade float64, rng *uint32) {
	// Generate white noise excitation
	excitation := make([]float32, len(output))
	for i := range excitation {
		*rng = *rng*1664525 + 1013904223
		// Generate noise in [-1, 1] range
		noise := (float32(*rng>>16) - 32768.0) / 65536.0
		excitation[i] = noise * float32(fade)
	}

	// Apply simple LPC filter to shape the noise
	// This gives the noise the spectral character of speech
	if order > 0 && len(prevLPC) >= order {
		state := make([]float32, order)
		copy(state, prevLPC[:order])

		for i := range output {
			// IIR filter: y[n] = x[n] + sum(a[k]*y[n-k-1])
			y := excitation[i]
			for k := 0; k < order && k < len(state); k++ {
				if i-k-1 >= 0 {
					y += state[k] * output[i-k-1] * 0.1
				}
			}
			output[i] = y

			// Clamp to prevent instability
			if output[i] > 1.0 {
				output[i] = 1.0
			} else if output[i] < -1.0 {
				output[i] = -1.0
			}
		}
	} else {
		// No LPC available, just use the noise directly
		copy(output, excitation)
	}
}

// estimatePitchFromHistory tries to find the pitch period in recent history.
// Uses simple autocorrelation to detect periodicity.
func estimatePitchFromHistory(history []float32, histIdx, histLen int) int {
	// Search range: 32 to 288 samples (typical pitch range)
	// At 16kHz: 32 samples = 2ms (500Hz), 288 samples = 18ms (55Hz)
	minLag := 32
	maxLag := 288
	if maxLag > histLen/2 {
		maxLag = histLen / 2
	}
	if minLag >= maxLag {
		return 80 // Default
	}

	// Look at last analysisLen samples
	analysisLen := 320 // ~20ms at 16kHz
	if analysisLen > histLen {
		analysisLen = histLen
	}

	var bestLag int
	var bestCorr float32 = -1e10

	// Simple autocorrelation search
	for lag := minLag; lag < maxLag; lag++ {
		var corr float32

		for i := 0; i < analysisLen-lag; i++ {
			idx1 := (histIdx - analysisLen + i + histLen) % histLen
			idx2 := (histIdx - analysisLen + i + lag + histLen) % histLen
			corr += history[idx1] * history[idx2]
		}

		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	if bestLag < minLag {
		bestLag = 80 // Default
	}

	return bestLag
}

// ConcealSILKStereo generates concealment for a stereo SILK frame.
// It applies the same PLC algorithm to both channels.
//
// Parameters:
//   - dec: SILK decoder state (used for both channels)
//   - frameSize: samples per channel at native SILK rate
//   - fadeFactor: gain multiplier (0.0 to 1.0)
//
// Returns: left and right channel concealed samples
func ConcealSILKStereo(dec SILKDecoderState, frameSize int, fadeFactor float64) (left, right []float32) {
	// For stereo, apply mono PLC to both channels
	// A more sophisticated approach would use the stereo prediction weights
	mono := ConcealSILK(dec, frameSize, fadeFactor)

	// Copy mono to both channels (simple approach)
	// In practice, you'd want to maintain separate L/R state
	left = make([]float32, frameSize)
	right = make([]float32, frameSize)
	copy(left, mono)
	copy(right, mono)

	return left, right
}

// Helper functions for fixed-point arithmetic (matching libopus)

func silkRand(seed int32) int32 {
	return seed*196314165 + 907633515
}

func smulwb(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

func smulww(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

func smlawb(a, b, c int32) int32 {
	return a + smulwb(b, c)
}

func rshiftRound(a int32, shift int) int32 {
	if shift == 0 {
		return a
	}
	return (a + (1 << (shift - 1))) >> shift
}

func sat16(a int32) int16 {
	if a > 32767 {
		return 32767
	}
	if a < -32768 {
		return -32768
	}
	return int16(a)
}

func addSat32(a, b int32) int32 {
	res := int64(a) + int64(b)
	if res > math.MaxInt32 {
		return math.MaxInt32
	}
	if res < math.MinInt32 {
		return math.MinInt32
	}
	return int32(res)
}

func lshiftSat32(a int32, shift int) int32 {
	if shift == 0 {
		return a
	}
	max := int32(math.MaxInt32 >> shift)
	min := int32(math.MinInt32 >> shift)
	if a > max {
		return math.MaxInt32
	}
	if a < min {
		return math.MinInt32
	}
	return a << shift
}

func inverse32VarQ(b, qRes int32) int32 {
	if b == 0 {
		return math.MaxInt32
	}

	// Simple approximation
	bNorm := int32(1)
	lshift := 0
	for bNorm < b && lshift < 30 {
		bNorm <<= 1
		lshift++
	}

	result := int64(1) << qRes
	result = result / int64(b)
	if result > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(result)
}

func smmul(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 32)
}

func rshiftRound64(a int64, shift int) int64 {
	if shift <= 0 {
		return a
	}
	if a < 0 {
		return -(((-a) + (1 << (shift - 1))) >> shift)
	}
	return (a + (1 << (shift - 1))) >> shift
}

func subSat32(a, b int32) int32 {
	v := int64(a) - int64(b)
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

func mul32FracQ(a, b int32, q uint) int32 {
	return int32(rshiftRound64(int64(a)*int64(b), int(q)))
}

func abs32Int(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}

func clz32(a int32) int {
	if a == 0 {
		return 32
	}
	return bits.LeadingZeros32(uint32(a))
}

func lpcInversePredGainQ30(aQ12 []int16, order int) int32 {
	if order <= 0 {
		return 1 << 30
	}
	if order > len(aQ12) {
		order = len(aQ12)
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}

	var aQA [maxLPCOrder]int32
	dcResp := int32(0)
	for k := 0; k < order; k++ {
		dcResp += int32(aQ12[k])
		aQA[k] = int32(aQ12[k]) << (lpcInvPredQA - 12)
	}
	if dcResp >= 4096 {
		return 0
	}
	return lpcInversePredGainQAC(aQA[:order], order)
}

func lpcInversePredGainQAC(aQA []int32, order int) int32 {
	invGainQ30 := int32(1 << 30)

	for k := order - 1; k > 0; k-- {
		if aQA[k] > lpcInvPredALimitQ24 || aQA[k] < -lpcInvPredALimitQ24 {
			return 0
		}

		rcQ31 := -(aQA[k] << (31 - lpcInvPredQA))
		rcMult1Q30 := (1 << 30) - smmul(rcQ31, rcQ31)
		if rcMult1Q30 <= (1<<15) || rcMult1Q30 > (1<<30) {
			return 0
		}

		invGainQ30 = smmul(invGainQ30, rcMult1Q30) << 2
		if invGainQ30 < minInvPredGainQ30 {
			return 0
		}

		mult2Q := 32 - clz32(abs32Int(rcMult1Q30))
		rcMult2 := inverse32VarQ(rcMult1Q30, int32(mult2Q+30))

		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]

			v1 := subSat32(tmp1, mul32FracQ(tmp2, rcQ31, 31))
			upd1 := rshiftRound64(int64(v1)*int64(rcMult2), mult2Q)
			if upd1 > math.MaxInt32 || upd1 < math.MinInt32 {
				return 0
			}
			aQA[n] = int32(upd1)

			v2 := subSat32(tmp2, mul32FracQ(tmp1, rcQ31, 31))
			upd2 := rshiftRound64(int64(v2)*int64(rcMult2), mult2Q)
			if upd2 > math.MaxInt32 || upd2 < math.MinInt32 {
				return 0
			}
			aQA[k-n-1] = int32(upd2)
		}
	}

	if aQA[0] > lpcInvPredALimitQ24 || aQA[0] < -lpcInvPredALimitQ24 {
		return 0
	}
	rcQ31 := -(aQA[0] << (31 - lpcInvPredQA))
	rcMult1Q30 := (1 << 30) - smmul(rcQ31, rcQ31)
	invGainQ30 = smmul(invGainQ30, rcMult1Q30) << 2
	if invGainQ30 < minInvPredGainQ30 {
		return 0
	}

	return invGainQ30
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func bwExpandQ12(ar []int16, coef float64) {
	chirpQ16 := int32(coef * 65536.0)
	chirpMinusOneQ16 := chirpQ16 - 65536

	for i := 0; i < len(ar); i++ {
		ar[i] = int16(rshiftRound(chirpQ16*int32(ar[i]), 16))
		chirpQ16 = chirpQ16 + rshiftRound(chirpQ16*chirpMinusOneQ16, 16)
	}
}

func computeEnergy(exc []int32, gainQ10 int32, length, offset int) (energy int32, shift int) {
	if length <= 0 || offset >= len(exc) {
		return 0, 0
	}

	end := offset + length
	if end > len(exc) {
		end = len(exc)
	}

	var sum int64
	shft := 0
	for i := offset; i < end; i++ {
		// Match silk_PLC_energy():
		// exc_buf[i] = SAT16( RSHIFT( SMULWW( exc_Q14, prevGain_Q10 ), 8 ) )
		scaled := sat16(smulww(exc[i], gainQ10) >> 8)
		s := int64(scaled)
		sum += s * s

		// Match silk_sum_sqr_shift overflow handling (coarse right shifts).
		if sum > 0x3FFFFFFF {
			sum >>= 2
			shft += 2
		}
	}

	for sum > 0x7FFFFFFF {
		sum >>= 1
		shft++
	}

	return int32(sum), shft
}

func lpcAnalysisFilter(out []int16, in []float32, B []int16, length, order, startIdx int) {
	for i := 0; i < order && i < length; i++ {
		out[i] = 0
	}

	histIdx := len(in) - 1

	for ix := order; ix < length; ix++ {
		inIdx := histIdx - (length - 1 - ix - startIdx)
		if inIdx < 0 {
			inIdx = 0
		}

		outQ12 := int32(0)
		for j := 0; j < order; j++ {
			prevIdx := inIdx - j - 1
			if prevIdx < 0 {
				break
			}
			if prevIdx < len(in) {
				inVal := int32(in[prevIdx] * 32768.0)
				outQ12 += (inVal * int32(B[j])) >> 12
			}
		}

		if inIdx < len(in) {
			inVal := int32(in[inIdx] * 32768.0)
			outQ12 = (inVal << 0) - outQ12
		}

		out32 := rshiftRound(outQ12, 0)
		out[ix] = sat16(out32)
	}
}

func lpcAnalysisFilterInt16(out []int16, in []int16, B []int16, length, order int) {
	for i := 0; i < order && i < length; i++ {
		out[i] = 0
	}

	for ix := order; ix < length; ix++ {
		inPos := ix
		if inPos >= len(in) {
			break
		}

		outQ12 := int32(in[inPos-1]) * int32(B[0])
		for j := 1; j < order; j++ {
			outQ12 += int32(in[inPos-1-j]) * int32(B[j])
		}

		outQ12 = (int32(in[inPos]) << 12) - outQ12
		out[ix] = sat16(rshiftRound(outQ12, 12))
	}
}
