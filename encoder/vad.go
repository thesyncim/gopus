// Package encoder implements multi-band Voice Activity Detection (VAD) for DTX.
// This implementation matches libopus SILK VAD behavior from silk/VAD.c.
//
// The VAD splits the signal into 4 frequency bands using analysis filter banks:
//   - Band 0: 0-1 kHz
//   - Band 1: 1-2 kHz
//   - Band 2: 2-4 kHz
//   - Band 3: 4-8 kHz
//
// Each band's energy is compared against adaptive noise estimates to determine
// speech activity probability.
//
// Reference: libopus silk/VAD.c, silk/define.h
package encoder

import (
	"math"
)

// VAD Constants matching libopus silk/define.h
const (
	// VADNBands is the number of frequency bands for VAD analysis.
	VADNBands = 4

	// VADInternalSubframesLog2 is log2 of the number of internal subframes.
	VADInternalSubframesLog2 = 2
	// VADInternalSubframes is the number of internal subframes (1 << 2 = 4).
	VADInternalSubframes = 1 << VADInternalSubframesLog2

	// VADNoiseLevelSmoothCoefQ16 is the noise level smoothing coefficient.
	// Must be < 4096.
	VADNoiseLevelSmoothCoefQ16 = 1024
	// VADNoiseLevelsBias is the bias for noise level estimation.
	VADNoiseLevelsBias = 50

	// VADNegativeOffsetQ5 is the sigmoid offset (sigmoid is 0 at -128).
	VADNegativeOffsetQ5 = 128
	// VADSNRFactorQ16 is the SNR scaling factor.
	VADSNRFactorQ16 = 45000

	// VADSNRSmoothCoefQ18 is the smoothing coefficient for SNR measurement.
	VADSNRSmoothCoefQ18 = 4096

	// DTXActivityThreshold is the activity probability threshold for DTX.
	// Matches libopus DTX_ACTIVITY_THRESHOLD = 0.1f
	DTXActivityThreshold = 0.1

	// NBSpeechFramesBeforeDTX is frames of speech required before DTX (200ms).
	NBSpeechFramesBeforeDTX = 10
	// MaxConsecutiveDTX is maximum consecutive DTX frames (400ms).
	MaxConsecutiveDTX = 20
)

// Tilt weights for spectral analysis (matching libopus tiltWeights).
var vadTiltWeights = [VADNBands]int32{30000, 6000, -12000, -12000}

// Analysis filter bank coefficients (matching silk/ana_filt_bank_1.c).
const (
	aFB1_20 = 5394 << 1    // 10788
	aFB1_21 = -24290       // (20623 << 1) cast to int16
)

// VADState holds the state for Voice Activity Detection.
// This mirrors silk_VAD_state from libopus silk/structs.h.
type VADState struct {
	// Analysis filterbank states
	AnaState  [2]int32 // 0-8 kHz state
	AnaState1 [2]int32 // 0-4 kHz state
	AnaState2 [2]int32 // 0-2 kHz state

	// Subframe energies from previous frame
	XnrgSubfr [VADNBands]int32

	// Smoothed energy-to-noise ratio per band (Q8)
	NrgRatioSmthQ8 [VADNBands]int32

	// High-pass filter state for lowest band differentiator
	HPState int16

	// Noise energy level per band
	NL [VADNBands]int32

	// Inverse noise energy level per band
	InvNL [VADNBands]int32

	// Noise level estimator bias per band
	NoiseLevelBias [VADNBands]int32

	// Frame counter for initial faster adaptation
	Counter int32

	// Speech activity probability (Q8, 0-255)
	SpeechActivityQ8 int

	// Input quality bands (Q15)
	InputQualityBandsQ15 [VADNBands]int

	// Input tilt (Q15)
	InputTiltQ15 int

	// Hangover counter for smooth transitions
	HangoverCount int

	// Previous activity decision for hysteresis
	PrevActivity bool
}

// NewVADState creates and initializes a new VAD state.
// Matches silk_VAD_Init from libopus.
func NewVADState() *VADState {
	v := &VADState{}
	v.Reset()
	return v
}

// Reset initializes VAD state to default values.
// Matches silk_VAD_Init from libopus silk/VAD.c.
func (v *VADState) Reset() {
	// Initialize noise level bias (approx pink noise - PSD proportional to 1/f)
	for b := 0; b < VADNBands; b++ {
		v.NoiseLevelBias[b] = maxInt32(VADNoiseLevelsBias/(int32(b)+1), 1)
	}

	// Initialize state with noise estimates
	for b := 0; b < VADNBands; b++ {
		v.NL[b] = 100 * v.NoiseLevelBias[b]
		if v.NL[b] > 0 {
			v.InvNL[b] = math.MaxInt32 / v.NL[b]
		} else {
			v.InvNL[b] = math.MaxInt32
		}
	}

	// Frame counter starts at 15 for initial faster smoothing
	v.Counter = 15

	// Initialize smoothed energy-to-noise ratio to 20 dB SNR (100 * 256)
	for b := 0; b < VADNBands; b++ {
		v.NrgRatioSmthQ8[b] = 100 * 256
	}

	// Clear other state
	v.AnaState = [2]int32{}
	v.AnaState1 = [2]int32{}
	v.AnaState2 = [2]int32{}
	v.XnrgSubfr = [VADNBands]int32{}
	v.HPState = 0
	v.SpeechActivityQ8 = 0
	v.InputQualityBandsQ15 = [VADNBands]int{}
	v.InputTiltQ15 = 0
	v.HangoverCount = 0
	v.PrevActivity = false
}

// GetSpeechActivity analyzes the input signal and returns speech activity level.
// This is the main VAD function matching silk_VAD_GetSA_Q8 from libopus.
//
// Parameters:
//   - pcm: Input PCM samples (mono, 16-bit range scaled to int16)
//   - frameLength: Number of samples in the frame
//   - fsKHz: Sample rate in kHz (8, 12, or 16)
//
// Returns:
//   - activityQ8: Speech activity level (0-255, Q8)
//   - isActive: True if speech activity detected
func (v *VADState) GetSpeechActivity(pcm []float32, frameLength int, fsKHz int) (int, bool) {
	// Safety checks
	if frameLength == 0 || len(pcm) < frameLength {
		return 0, false
	}

	// Convert float32 samples to int16 for fixed-point processing
	input := make([]int16, frameLength)
	for i := 0; i < frameLength; i++ {
		// Clamp to int16 range
		sample := pcm[i] * 32768.0
		if sample > 32767 {
			sample = 32767
		} else if sample < -32768 {
			sample = -32768
		}
		input[i] = int16(sample)
	}

	// Calculate decimated frame lengths
	decimatedFrameLength1 := frameLength >> 1 // frame_length / 2
	decimatedFrameLength2 := frameLength >> 2 // frame_length / 4
	decimatedFrameLength := frameLength >> 3  // frame_length / 8

	// Allocate workspace for decimated signals
	// Layout: [0-1kHz][temp][1-2kHz][2-4kHz][4-8kHz]
	xOffset := [VADNBands]int{
		0,
		decimatedFrameLength + decimatedFrameLength2,
		decimatedFrameLength + decimatedFrameLength2 + decimatedFrameLength,
		decimatedFrameLength + decimatedFrameLength2 + decimatedFrameLength + decimatedFrameLength2,
	}
	xLen := xOffset[3] + decimatedFrameLength1
	X := make([]int16, xLen)

	// Filter and decimate into 4 bands

	// 0-8 kHz to 0-4 kHz and 4-8 kHz
	anaFiltBank1(input, &v.AnaState, X[:decimatedFrameLength1], X[xOffset[3]:], frameLength)

	// 0-4 kHz to 0-2 kHz and 2-4 kHz
	anaFiltBank1(X[:decimatedFrameLength1], &v.AnaState1, X[:decimatedFrameLength2], X[xOffset[2]:], decimatedFrameLength1)

	// 0-2 kHz to 0-1 kHz and 1-2 kHz
	anaFiltBank1(X[:decimatedFrameLength2], &v.AnaState2, X[:decimatedFrameLength], X[xOffset[1]:], decimatedFrameLength2)

	// HP filter on lowest band (differentiator)
	X[decimatedFrameLength-1] = X[decimatedFrameLength-1] >> 1
	hpStateTmp := X[decimatedFrameLength-1]
	for i := decimatedFrameLength - 1; i > 0; i-- {
		X[i-1] = X[i-1] >> 1
		X[i] -= X[i-1]
	}
	X[0] -= v.HPState
	v.HPState = hpStateTmp

	// Calculate energy in each band
	Xnrg := [VADNBands]int32{}
	for b := 0; b < VADNBands; b++ {
		// Find decimated frame length for this band
		var decLen int
		if b == 0 {
			decLen = frameLength >> 3 // Band 0: frame/8
		} else if b == 1 {
			decLen = frameLength >> 3 // Band 1: frame/8
		} else if b == 2 {
			decLen = frameLength >> 2 // Band 2: frame/4
		} else {
			decLen = frameLength >> 1 // Band 3: frame/2
		}

		// Split into subframes
		decSubframeLength := decLen >> VADInternalSubframesLog2
		decSubframeOffset := 0

		// Initialize with energy from previous frame's last subframe
		Xnrg[b] = v.XnrgSubfr[b]

		var sumSquared int32
		for s := 0; s < VADInternalSubframes; s++ {
			sumSquared = 0
			for i := 0; i < decSubframeLength; i++ {
				idx := xOffset[b] + i + decSubframeOffset
				if idx < len(X) {
					xTmp := int32(X[idx]) >> 3
					sumSquared += xTmp * xTmp
				}
			}

			// Add/saturate energy
			if s < VADInternalSubframes-1 {
				Xnrg[b] = addPosSat32(Xnrg[b], sumSquared)
			} else {
				// Look-ahead subframe gets half weight
				Xnrg[b] = addPosSat32(Xnrg[b], sumSquared>>1)
			}

			decSubframeOffset += decSubframeLength
		}
		v.XnrgSubfr[b] = sumSquared
	}

	// Noise estimation
	v.getNoiseLevels(Xnrg[:])

	// Signal-plus-noise to noise ratio estimation
	var sumSquared int32
	var inputTilt int32
	NrgToNoiseRatioQ8 := [VADNBands]int32{}

	for b := 0; b < VADNBands; b++ {
		speechNrg := Xnrg[b] - v.NL[b]
		if speechNrg > 0 {
			// Compute energy to noise ratio
			if (uint32(Xnrg[b]) & 0xFF800000) == 0 {
				NrgToNoiseRatioQ8[b] = (Xnrg[b] << 8) / (v.NL[b] + 1)
			} else {
				NrgToNoiseRatioQ8[b] = Xnrg[b] / ((v.NL[b] >> 8) + 1)
			}

			// Convert to log domain
			snrQ7 := lin2log(NrgToNoiseRatioQ8[b]) - 8*128

			// Sum-of-squares for mean calculation
			sumSquared += snrQ7 * snrQ7

			// Tilt measure
			snrForTilt := snrQ7
			if speechNrg < (1 << 20) {
				// Scale down SNR for small speech energies
				snrForTilt = (sqrtApprox(speechNrg) << 6) * snrQ7 >> 15
			}
			inputTilt += (vadTiltWeights[b] * snrForTilt) >> 15
		} else {
			NrgToNoiseRatioQ8[b] = 256 // 1.0 in Q8
		}
	}

	// Mean-of-squares
	sumSquared /= VADNBands

	// Root-mean-square approximation, scale to dBs
	pSNRdBQ7 := int32(3 * sqrtApprox(sumSquared))

	// Speech probability estimation using sigmoid
	saQ15 := sigmQ15((VADSNRFactorQ16*pSNRdBQ7)>>15 - VADNegativeOffsetQ5)

	// Frequency tilt measure
	v.InputTiltQ15 = int((sigmQ15(inputTilt) - 16384) << 1)

	// Scale sigmoid output based on power levels
	var speechNrg int32
	for b := 0; b < VADNBands; b++ {
		// Higher frequency bands have more weight
		speechNrg += int32(b+1) * ((Xnrg[b] - v.NL[b]) >> 4)
	}

	// Adjust for frame length (20ms has half the energy of 10ms)
	if frameLength == 20*fsKHz {
		speechNrg >>= 1
	}

	// Power scaling
	if speechNrg <= 0 {
		saQ15 >>= 1
	} else if speechNrg < 16384 {
		speechNrg <<= 16
		speechNrg = sqrtApprox(speechNrg)
		saQ15 = ((32768 + speechNrg) * saQ15) >> 15
	}

	// Convert to Q8 (0-255) and clamp
	v.SpeechActivityQ8 = minInt(int(saQ15>>7), 255)

	// Smoothing coefficient based on activity
	smoothCoefQ16 := (VADSNRSmoothCoefQ18 * saQ15 * saQ15) >> 30

	if frameLength == 10*fsKHz {
		smoothCoefQ16 >>= 1
	}

	// Update smoothed energy-to-noise ratios and quality bands
	for b := 0; b < VADNBands; b++ {
		// Smooth energy-to-noise ratio
		v.NrgRatioSmthQ8[b] += ((NrgToNoiseRatioQ8[b] - v.NrgRatioSmthQ8[b]) * smoothCoefQ16) >> 16

		// SNR in dB per band
		snrQ7 := int32(3) * (lin2log(v.NrgRatioSmthQ8[b]) - 8*128)
		// Quality = sigmoid(0.25 * (SNR_dB - 16))
		v.InputQualityBandsQ15[b] = int(sigmQ15((snrQ7 - 16*128) >> 4))
	}

	// Apply hangover and hysteresis for smooth transitions
	activityProb := float64(v.SpeechActivityQ8) / 255.0
	isActive := activityProb > DTXActivityThreshold

	// Hangover: keep activity flag high for a few frames after speech ends
	if isActive {
		v.HangoverCount = 5 // 5 frames hangover (~100ms at 20ms frames)
	} else if v.HangoverCount > 0 {
		v.HangoverCount--
		isActive = true
	}

	// Hysteresis: use different thresholds for on/off transitions
	if v.PrevActivity && activityProb > DTXActivityThreshold*0.5 {
		isActive = true
	}
	v.PrevActivity = isActive

	return v.SpeechActivityQ8, isActive
}

// getNoiseLevels updates noise level estimates for each band.
// Matches silk_VAD_GetNoiseLevels from libopus.
func (v *VADState) getNoiseLevels(pX []int32) {
	// Initially faster smoothing during first 20 seconds
	var minCoef int32
	if v.Counter < 1000 {
		minCoef = 32767 / ((v.Counter >> 4) + 1)
		v.Counter++
	} else {
		minCoef = 0
	}

	for k := 0; k < VADNBands; k++ {
		// Get old noise level estimate
		nl := v.NL[k]

		// Add bias
		nrg := addPosSat32(pX[k], v.NoiseLevelBias[k])
		if nrg <= 0 {
			nrg = 1
		}

		// Invert energy
		invNrg := int32(math.MaxInt32) / nrg

		// Adaptive smoothing coefficient based on energy vs noise level
		var coef int32
		if nrg > (nl << 3) {
			// Much higher than noise - slow update
			coef = VADNoiseLevelSmoothCoefQ16 >> 3
		} else if nrg < nl {
			// Below noise level - fast update
			coef = VADNoiseLevelSmoothCoefQ16
		} else {
			// In between - interpolate
			coef = ((invNrg * nl) >> 15) * (VADNoiseLevelSmoothCoefQ16 << 1) >> 16
		}

		// Initially faster smoothing
		if coef < minCoef {
			coef = minCoef
		}

		// Smooth inverse energies
		v.InvNL[k] += ((invNrg - v.InvNL[k]) * coef) >> 16
		if v.InvNL[k] < 0 {
			v.InvNL[k] = 0
		}

		// Compute noise level by inverting again
		if v.InvNL[k] > 0 {
			nl = int32(math.MaxInt32) / v.InvNL[k]
		} else {
			nl = int32(math.MaxInt32)
		}
		if nl < 0 {
			nl = 0
		}

		// Limit noise levels (guarantee 7 bits of headroom)
		if nl > 0x00FFFFFF {
			nl = 0x00FFFFFF
		}

		v.NL[k] = nl
	}
}

// anaFiltBank1 splits signal into two decimated bands using first-order allpass filters.
// Matches silk_ana_filt_bank_1 from libopus silk/ana_filt_bank_1.c.
func anaFiltBank1(in []int16, S *[2]int32, outL, outH []int16, N int) {
	N2 := N >> 1
	if N2 == 0 {
		return
	}

	// Internal variables and state are in Q10 format
	for k := 0; k < N2; k++ {
		if 2*k >= len(in) || 2*k+1 >= len(in) {
			break
		}

		// Convert to Q10
		in32 := int32(in[2*k]) << 10

		// All-pass section for even input sample
		Y := in32 - S[0]
		X := Y + ((Y * int32(aFB1_21)) >> 16)
		out1 := S[0] + X
		S[0] = in32 + X

		// Convert to Q10
		in32 = int32(in[2*k+1]) << 10

		// All-pass section for odd input sample
		Y = in32 - S[1]
		X = (Y * int32(aFB1_20)) >> 16
		out2 := S[1] + X
		S[1] = in32 + X

		// Add/subtract, convert back to int16 and store
		if k < len(outL) {
			sum := (out2 + out1 + (1 << 10)) >> 11
			outL[k] = satInt16(sum)
		}
		if k < len(outH) {
			diff := (out2 - out1 + (1 << 10)) >> 11
			outH[k] = satInt16(diff)
		}
	}
}

// Helper functions for fixed-point arithmetic

// lin2log converts linear value to log domain.
// Approximation of log2(x) * 128 for Q8 input.
// Matches silk_lin2log from libopus.
func lin2log(x int32) int32 {
	if x <= 0 {
		return 0
	}

	// Find position of highest bit (0-indexed)
	var i int32
	for tmp := x; tmp > 1; tmp >>= 1 {
		i++
	}

	// For very small values, return directly
	if i < 15 {
		// Fractional part approximation
		frac := (x << uint(15-i)) & 0x7FFF
		return (i << 7) + ((frac * 128) >> 15)
	}

	// For larger values, avoid overflow
	frac := (x >> uint(i-15)) & 0x7FFF
	return (i << 7) + ((frac * 128) >> 15)
}

// sigmQ15 computes sigmoid function in Q15.
// sigmoid(x) = 1 / (1 + exp(-x))
func sigmQ15(x int32) int32 {
	// Clamp input - sigmoid saturates at boundaries
	if x >= 127 {
		return 32767 // Max Q15 positive value (0.9999...)
	}
	if x <= -128 {
		return 0 // Min value
	}

	// Piecewise linear approximation
	// sigmoid(x) approx= 0.5 + 0.25*x for small |x|
	return 16384 + (x * 128) // 0.5 + x/256 in Q15
}

// sqrtApprox computes approximate integer square root.
func sqrtApprox(x int32) int32 {
	if x <= 0 {
		return 0
	}
	return int32(math.Sqrt(float64(x)))
}

// addPosSat32 adds two positive int32 values with saturation.
func addPosSat32(a, b int32) int32 {
	sum := a + b
	if sum < a || sum < b {
		return math.MaxInt32
	}
	return sum
}

// satInt16 saturates an int32 to int16 range.
func satInt16(x int32) int16 {
	if x > 32767 {
		return 32767
	}
	if x < -32768 {
		return -32768
	}
	return int16(x)
}

// maxInt32 returns the maximum of two int32 values.
func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// minInt returns the minimum of two int values.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
