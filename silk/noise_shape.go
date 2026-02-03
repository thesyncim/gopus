package silk

import "math"

// Noise shaping analysis constants from libopus tuning_parameters.h
// These control the spectral shaping of quantization noise.
const (
	// Harmonic shaping (Q14 = 16384 = 1.0)
	harmonicShaping                        = 0.3  // Base harmonic shaping
	harmonicShapingQ14                     = 4915 // 0.3 * 16384
	highRateOrLowQualityHarmonicShaping    = 0.2  // Additional for high rate/low quality
	highRateOrLowQualityHarmonicShapingQ14 = 3277 // 0.2 * 16384

	// Noise tilt (HP filtering of noise)
	hpNoiseCoef        = 0.25    // High-pass coefficient
	hpNoiseCoefQ14     = 4096    // 0.25 * 16384
	harmHPNoiseCoef    = 0.35    // Additional HP for voiced
	harmHPNoiseCoefQ24 = 5872026 // 0.35 * 16777216 (Q24 for libopus matching)

	// Low-frequency shaping
	lowFreqShaping               = 0.4 // Base low-freq shaping strength
	lowFreqShapingQ14            = 6554
	lowQualityLowFreqShapingDecr = 0.5 // Decrease for low quality

	// Noise shape analysis constants (tuning_parameters.h)
	bgSNRDecrDB                       = 2.0
	harmSNRIncrDB                     = 2.0
	energyVariationThresholdQntOffset = 0.6
	shapeWhiteNoiseFraction           = 3e-5
	bandwidthExpansion                = 0.94
	shapeCoefLimit                    = 3.999
	warpingMultiplier                 = 0.015

	// Subframe smoothing coefficient
	subfrSmthCoef = 0.4 // Smoothing between subframes

	// Lambda (rate-distortion tradeoff) constants (Q10 = 1024 = 1.0)
	lambdaOffset              = 1.2   // Base lambda
	lambdaOffsetQ10           = 1229  // 1.2 * 1024
	lambdaSpeechAct           = -0.2  // Speech activity adjustment
	lambdaSpeechActQ10        = -205  // -0.2 * 1024
	lambdaDelayedDecisions    = -0.05 // Delayed decisions adjustment
	lambdaDelayedDecisionsQ10 = -51   // -0.05 * 1024
	lambdaInputQuality        = -0.1  // Input quality adjustment
	lambdaInputQualityQ10     = -102  // -0.1 * 1024
	lambdaCodingQuality       = -0.2  // Coding quality adjustment
	lambdaCodingQualityQ10    = -205  // -0.2 * 1024
	lambdaQuantOffset         = 0.8   // Quantization offset adjustment
	lambdaQuantOffsetQ10      = 819   // 0.8 * 1024
)

// NoiseShapeState holds the noise shaping state that persists across frames.
// This mirrors libopus silk_shape_state_FLP structure.
type NoiseShapeState struct {
	// Smoothed harmonic shaping gain (Q14)
	HarmShapeGainSmth int32

	// Smoothed tilt (Q14)
	TiltSmth int32

	// Last gain index for conditional coding
	LastGainIndex int8
}

// NoiseShapeParams holds the computed noise shaping parameters for a frame.
type NoiseShapeParams struct {
	// Per-subframe parameters
	HarmShapeGainQ14 []int   // Harmonic shaping gain (Q14)
	TiltQ14          []int   // Spectral tilt (Q14)
	LFShpQ14         []int32 // Low-frequency shaping (packed MA/AR, Q14)
	ARShpQ13         []int16 // Noise shaping AR coefficients (Q13, per subframe)

	// Frame-level parameters
	LambdaQ10     int     // Rate-distortion tradeoff (Q10)
	CodingQuality float32 // Coding quality [0, 1]
	InputQuality  float32 // Input quality [0, 1]
}

// computeLambdaQ10 recomputes the Lambda (rate-distortion tradeoff) using the
// provided quantization offset type. This mirrors the logic in ComputeNoiseShapeParams.
func computeLambdaQ10(signalType, speechActivityQ8, quantOffsetType int, codingQuality, inputQuality float32) int {
	quantOffset := float32(silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]) / 1024.0
	lambda := lambdaOffset +
		lambdaSpeechAct*float32(speechActivityQ8)/256.0 +
		lambdaInputQuality*inputQuality +
		lambdaCodingQuality*codingQuality +
		lambdaQuantOffset*quantOffset

	// Clamp lambda to valid range [0.0, 2.0)
	if lambda < 0 {
		lambda = 0
	}
	if lambda >= 2.0 {
		lambda = 1.99
	}
	return int(lambda * 1024.0)
}

// ComputeNoiseShapeParams computes adaptive noise shaping parameters.
// This ports libopus silk/float/noise_shape_analysis_FLP.c logic.
//
// Parameters:
//   - signalType: TYPE_VOICED (2), TYPE_UNVOICED (1), or TYPE_INACTIVE (0)
//   - speechActivityQ8: Speech activity level [0, 255]
//   - ltpCorr: LTP correlation [0.0, 1.0] - higher means more periodic
//   - pitchLags: Pitch lags per subframe (for LF shaping)
//   - snrDBQ7: Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
//   - numSubframes: Number of subframes
//   - fsKHz: Sample rate in kHz (8, 12, or 16)
func (s *NoiseShapeState) ComputeNoiseShapeParams(
	signalType int,
	speechActivityQ8 int,
	ltpCorr float32,
	pitchLags []int,
	originalSNR float64,
	quantOffsetType int,
	inputQualityBandsQ15 [4]int,
	numSubframes int,
	fsKHz int,
) *NoiseShapeParams {
	params := &NoiseShapeParams{
		HarmShapeGainQ14: make([]int, numSubframes),
		TiltQ14:          make([]int, numSubframes),
		LFShpQ14:         make([]int32, numSubframes),
	}

	// Compute coding quality from ORIGINAL SNR (sigmoid mapping)
	// coding_quality = sigmoid(0.25 * (SNR_dB - 20))
	params.CodingQuality = Sigmoid(0.25 * (float32(originalSNR) - 20.0))

	// Input quality (average of the quality in the lowest two VAD bands)
	var inputQualityBand0 float32
	if inputQualityBandsQ15[0] >= 0 {
		inputQualityBand0 = float32(inputQualityBandsQ15[0]) / 32768.0
		params.InputQuality = 0.5 * (float32(inputQualityBandsQ15[0]) + float32(inputQualityBandsQ15[1])) / 32768.0
	} else {
		inputQualityBand0 = float32(speechActivityQ8) / 256.0
		params.InputQuality = float32(speechActivityQ8) / 256.0
	}

	// Compute Lambda (rate-distortion tradeoff)
	// Lambda = LAMBDA_OFFSET + LAMBDA_SPEECH_ACT * speech_activity
	//        + LAMBDA_INPUT_QUALITY * input_quality
	//        + LAMBDA_CODING_QUALITY * coding_quality
	//        + LAMBDA_QUANT_OFFSET * quant_offset
	quantOffset := float32(silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]) / 1024.0
	lambda := lambdaOffset +
		lambdaSpeechAct*float32(speechActivityQ8)/256.0 +
		lambdaInputQuality*params.InputQuality +
		lambdaCodingQuality*params.CodingQuality +
		lambdaQuantOffset*quantOffset

	// Clamp lambda to valid range [0.0, 2.0)
	if lambda < 0 {
		lambda = 0
	}
	if lambda >= 2.0 {
		lambda = 1.99
	}
	params.LambdaQ10 = int(lambda * 1024.0)

	// Compute Tilt (spectral noise tilt)
	var tilt float32
	if signalType == typeVoiced {
		// For voiced: reduce HF noise, more with higher speech activity
		// Tilt = -HP_NOISE_COEF - (1-HP_NOISE_COEF) * HARM_HP_NOISE_COEF * speech_activity
		tilt = -hpNoiseCoef - (1.0-hpNoiseCoef)*harmHPNoiseCoef*float32(speechActivityQ8)/256.0
	} else {
		// For unvoiced: just HP noise shaping
		tilt = -hpNoiseCoef
	}
	tiltQ14 := int(tilt * 16384.0)

	// Compute HarmShapeGain (harmonic noise shaping for voiced)
	var harmShapeGain float32
	if signalType == typeVoiced {
		// Base harmonic shaping
		harmShapeGain = harmonicShaping

		// More harmonic shaping for high bitrates or noisy input
		harmShapeGain += highRateOrLowQualityHarmonicShaping *
			(1.0 - (1.0-params.CodingQuality)*params.InputQuality)

		// Less for less periodic signals (scale by sqrt of LTP correlation)
		if ltpCorr > 0 {
			harmShapeGain *= sqrt32(ltpCorr)
		} else {
			harmShapeGain = 0
		}
	} else {
		harmShapeGain = 0
	}
	harmShapeGainQ14 := int(harmShapeGain * 16384.0)

	// Compute LF shaping (low-frequency noise shaping)
	// strength = LOW_FREQ_SHAPING * (1 + LOW_QUALITY_DECR * (input_quality_bands[0] - 1))
	strength := lowFreqShaping * (1.0 + lowQualityLowFreqShapingDecr*(inputQualityBand0-1.0))
	strength *= float32(speechActivityQ8) / 256.0

	// Apply smoothing and compute per-subframe values
	for k := 0; k < numSubframes; k++ {
		// Smooth harmonic shaping gain
		s.HarmShapeGainSmth += int32(subfrSmthCoef * float32(int32(harmShapeGainQ14)-s.HarmShapeGainSmth))
		params.HarmShapeGainQ14[k] = int(s.HarmShapeGainSmth)

		// Smooth tilt
		s.TiltSmth += int32(subfrSmthCoef * float32(int32(tiltQ14)-s.TiltSmth))
		params.TiltQ14[k] = int(s.TiltSmth)

		// LF shaping depends on pitch lag
		var lfMaShp, lfArShp float32
		if signalType == typeVoiced && len(pitchLags) > k && pitchLags[k] > 0 {
			// Reduce LF noise for periodic signals
			// b = 0.2/fs_kHz + 3.0/pitch_lag
			b := 0.2/float32(fsKHz) + 3.0/float32(pitchLags[k])
			lfMaShp = -1.0 + b
			lfArShp = 1.0 - b - b*strength
		} else {
			// Unvoiced: simpler LF shaping
			b := 1.3 / float32(fsKHz)
			lfMaShp = -1.0 + b
			lfArShp = 1.0 - b - b*strength*0.6
		}

		// Pack LF_MA and LF_AR into single int32 (libopus format)
		// LF_shp_Q14 = (LF_AR_shp << 16) | (LF_MA_shp & 0xFFFF)
		lfMaQ14 := int16(lfMaShp * 16384.0)
		lfArQ14 := int16(lfArShp * 16384.0)
		params.LFShpQ14[k] = (int32(lfArQ14) << 16) | (int32(lfMaQ14) & 0xFFFF)
	}

	return params
}

// Sigmoid computes the sigmoid function: 1 / (1 + exp(-x))
func Sigmoid(x float32) float32 {
	if x > 10 {
		return 1.0
	}
	if x < -10 {
		return 0.0
	}
	return 1.0 / (1.0 + exp32(-x))
}

// exp32 computes exp(x) for float32 using a fast approximation
func exp32(x float32) float32 {
	// Use standard library via float64 conversion for accuracy
	return float32(exp64(float64(x)))
}

// exp64 computes exp(x) for float64
func exp64(x float64) float64 {
	// Fast exp approximation using series expansion for small x
	// For larger values, use the identity exp(x) = exp(x/2)^2
	if x > 20 {
		return 485165195.4 // exp(20) approx
	}
	if x < -20 {
		return 0
	}

	// For moderate values, use polynomial approximation
	// exp(x) ≈ 1 + x + x²/2 + x³/6 + x⁴/24 + x⁵/120 + x⁶/720
	if x >= -1 && x <= 1 {
		x2 := x * x
		x3 := x2 * x
		x4 := x2 * x2
		x5 := x4 * x
		x6 := x3 * x3
		return 1 + x + x2/2 + x3/6 + x4/24 + x5/120 + x6/720
	}

	// For larger |x|, use identity
	half := exp64(x / 2)
	return half * half
}

// sqrt32 computes sqrt(x) for float32
func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(math.Sqrt(float64(x)))
}

// NewNoiseShapeState creates a new noise shaping state.
func NewNoiseShapeState() *NoiseShapeState {
	return &NoiseShapeState{
		HarmShapeGainSmth: 0,
		TiltSmth:          -hpNoiseCoefQ14, // Start with default unvoiced tilt
		LastGainIndex:     0,
	}
}
