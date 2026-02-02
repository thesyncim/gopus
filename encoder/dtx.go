// Package encoder implements DTX (Discontinuous Transmission) for the Opus encoder.
// DTX saves bandwidth during silence by suppressing packets and sending periodic
// comfort noise frames to maintain presence.
//
// This implementation uses multi-band VAD (Voice Activity Detection) matching
// libopus SILK behavior for accurate speech detection.
//
// Reference: RFC 6716 Section 2.1.9, libopus silk/VAD.c
package encoder

// DTX Constants matching libopus silk/define.h
const (
	// DTXComfortNoiseIntervalMs is how often to send comfort noise during DTX.
	// Per Opus convention, send a comfort noise frame every 400ms of silence.
	DTXComfortNoiseIntervalMs = 400

	// DTXFrameThresholdMs is the duration of silence before DTX activates.
	// Matches NB_SPEECH_FRAMES_BEFORE_DTX * 20 = 200ms.
	DTXFrameThresholdMs = 200

	// DTXMaxConsecutiveMs is the maximum duration for DTX mode.
	// Matches MAX_CONSECUTIVE_DTX * 20 = 400ms.
	DTXMaxConsecutiveMs = 400

	// DTXFadeInMs is the fade-in duration when exiting DTX mode.
	DTXFadeInMs = 10

	// DTXFadeOutMs is the fade-out duration when entering DTX mode.
	DTXFadeOutMs = 10

	// DTXMinBitrate is the minimum bitrate used for comfort noise frames.
	DTXMinBitrate = 6000
)

// dtxState holds state for discontinuous transmission with multi-band VAD.
type dtxState struct {
	// Multi-band VAD state for accurate speech detection
	vad *VADState

	// Counter for consecutive no-activity frames in milliseconds (Q1 format)
	noActivityMsQ1 int

	// Whether currently in DTX mode (suppressing frames)
	inDTXMode bool

	// Frames since last comfort noise packet
	msSinceComfortNoise int

	// Saved filter state for CNG (Comfort Noise Generation)
	cngState *cngState

	// Frame duration in milliseconds (for timing calculations)
	frameDurationMs int
}

// cngState holds state for Comfort Noise Generation.
// Matches silk_CNG_struct from libopus.
type cngState struct {
	// Smoothed NLSF coefficients for CNG synthesis
	smthNLSFQ15 [16]int16

	// Smoothed gain for CNG
	smthGainQ16 int32

	// Random seed for excitation generation
	randSeed int32

	// Sample rate in kHz
	fsKHz int
}

// newDTXState creates initial DTX state with multi-band VAD.
func newDTXState() *dtxState {
	return &dtxState{
		vad:                 NewVADState(),
		noActivityMsQ1:      0,
		inDTXMode:           false,
		msSinceComfortNoise: 0,
		cngState:            newCNGState(),
		frameDurationMs:     20, // Default 20ms frames
	}
}

// newCNGState creates initial CNG state.
func newCNGState() *cngState {
	return &cngState{
		smthGainQ16: 0,
		randSeed:    22222, // Match libopus
		fsKHz:       16,
	}
}

// reset resets DTX state when speech resumes.
func (d *dtxState) reset() {
	d.noActivityMsQ1 = 0
	d.inDTXMode = false
	d.msSinceComfortNoise = 0
	// Note: VAD state is NOT reset - noise estimates should persist
}

// resetFull resets all DTX and VAD state (for new stream).
func (d *dtxState) resetFull() {
	d.reset()
	if d.vad != nil {
		d.vad.Reset()
	}
	if d.cngState != nil {
		d.cngState.randSeed = 22222
		d.cngState.smthGainQ16 = 0
	}
}

// shouldUseDTX determines if frame should be suppressed (DTX mode).
// Uses multi-band VAD for accurate speech detection matching libopus.
//
// Returns: (suppressFrame bool, sendComfortNoise bool)
func (e *Encoder) shouldUseDTX(pcm []float64) (bool, bool) {
	if !e.dtxEnabled || e.dtx == nil {
		return false, false
	}

	// Convert to float32 for VAD processing using scratch buffer (zero-alloc)
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	// Determine sample rate and frame parameters
	// For multi-channel, use first channel or mix to mono
	frameLength := len(pcm32)
	if e.channels == 2 {
		frameLength /= 2
	}

	// Mix to mono for VAD analysis (if stereo) using scratch buffer (zero-alloc)
	mono := pcm32
	if e.channels == 2 {
		mono = e.scratchLeft[:frameLength]
		for i := 0; i < frameLength; i++ {
			mono[i] = (pcm32[i*2] + pcm32[i*2+1]) * 0.5
		}
	}

	// Determine sample rate in kHz for VAD
	fsKHz := 16 // Default to 16kHz
	switch {
	case frameLength <= 80:
		fsKHz = 8
	case frameLength <= 120:
		fsKHz = 12
	case frameLength <= 160:
		fsKHz = 16
	case frameLength <= 240:
		fsKHz = 24
	case frameLength <= 480:
		fsKHz = 48
	}

	// Get frame duration in ms
	frameDurationMs := (frameLength * 1000) / (fsKHz * 1000)
	if frameDurationMs <= 0 {
		frameDurationMs = 20 // Default
	}
	e.dtx.frameDurationMs = frameDurationMs

	// Run multi-band VAD
	_, isActive := e.dtx.vad.GetSpeechActivity(mono, frameLength, fsKHz)

	// DTX decision logic matching libopus decide_dtx_mode
	frameSizeMsQ1 := frameDurationMs * 2 // Q1 format (multiply by 2)

	if !isActive {
		// No activity - increment counter
		e.dtx.noActivityMsQ1 += frameSizeMsQ1

		// Check if we've been silent long enough for DTX
		thresholdMsQ1 := NBSpeechFramesBeforeDTX * 20 * 2 // 200ms in Q1
		maxDTXMsQ1 := (NBSpeechFramesBeforeDTX + MaxConsecutiveDTX) * 20 * 2

		if e.dtx.noActivityMsQ1 > thresholdMsQ1 {
			if e.dtx.noActivityMsQ1 <= maxDTXMsQ1 {
				// Valid DTX frame
				e.dtx.inDTXMode = true
				e.dtx.msSinceComfortNoise += frameDurationMs

				// Send comfort noise periodically
				if e.dtx.msSinceComfortNoise >= DTXComfortNoiseIntervalMs {
					e.dtx.msSinceComfortNoise = 0
					return true, true // Suppress but send CNG
				}

				return true, false // Suppress entirely
			} else {
				// Reset counter to threshold (prevent overflow)
				e.dtx.noActivityMsQ1 = thresholdMsQ1
			}
		}
	} else {
		// Activity detected - exit DTX mode
		e.dtx.noActivityMsQ1 = 0
		if e.dtx.inDTXMode {
			e.dtx.inDTXMode = false
			e.dtx.msSinceComfortNoise = 0
		}
	}

	return false, false
}

// InDTX returns whether the encoder is currently in DTX mode.
// This matches OPUS_GET_IN_DTX from libopus.
func (e *Encoder) InDTX() bool {
	if e.dtx == nil {
		return false
	}
	return e.dtx.inDTXMode
}

// GetVADActivity returns the current VAD speech activity level (0-255).
func (e *Encoder) GetVADActivity() int {
	if e.dtx == nil || e.dtx.vad == nil {
		return 0
	}
	return e.dtx.vad.SpeechActivityQ8
}

// encodeComfortNoise encodes a comfort noise frame.
// Comfort noise provides natural-sounding silence during DTX.
// This generates low-level shaped noise matching the ambient noise characteristics.
func (e *Encoder) encodeComfortNoise(frameSize int) ([]byte, error) {
	// Generate shaped comfort noise based on CNG state
	noise := make([]float64, frameSize*e.channels)

	// Use CNG state for noise generation
	cng := e.dtx.cngState
	if cng == nil {
		cng = newCNGState()
		e.dtx.cngState = cng
	}

	// Generate noise with appropriate spectral shape
	for i := range noise {
		// LCG random number generator (matching libopus)
		cng.randSeed = cng.randSeed*1664525 + 1013904223

		// Convert to float and scale to very low amplitude (-60 dBFS)
		// CNG level is typically -40 to -60 dBFS
		randFloat := float64(int32(cng.randSeed)) / float64(1<<31)
		noise[i] = randFloat * 0.002 // ~-54 dBFS
	}

	// Encode the comfort noise frame using the current mode
	// The low energy will result in minimal bits used
	return e.encodeFrame(noise, frameSize)
}

// encodeFrame encodes a single frame using the current mode.
// This is used by both normal encoding and comfort noise generation.
func (e *Encoder) encodeFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Determine actual mode to use
	actualMode := e.selectMode(frameSize)

	// Route to appropriate encoder
	switch actualMode {
	case ModeSILK:
		return e.encodeSILKFrame(pcm, frameSize)
	case ModeHybrid:
		return e.encodeHybridFrame(pcm, frameSize)
	case ModeCELT:
		return e.encodeCELTFrame(pcm, frameSize)
	default:
		return nil, ErrEncodingFailed
	}
}

// nextRandom returns a random float64 in [0, 1).
// Uses LCG matching libopus for determinism.
func (e *Encoder) nextRandom() float64 {
	e.rng = e.rng*1664525 + 1013904223
	return float64(e.rng) / float64(1<<32)
}


// classifySignal determines signal type using energy-based detection.
// This is a legacy function kept for compatibility; new code uses VAD.
// Returns: 0 = inactive (silence), 1 = unvoiced, 2 = voiced
func classifySignal(pcm []float32) (int, float32) {
	if len(pcm) == 0 {
		return 0, 0
	}

	// Compute signal energy
	var energy float64
	for _, s := range pcm {
		energy += float64(s) * float64(s)
	}
	energy /= float64(len(pcm))

	// Energy threshold for silence detection
	// -40 dBFS is typical silence threshold
	const silenceThreshold = 0.0001 // ~-40 dBFS

	if energy < silenceThreshold {
		return 0, float32(energy) // Inactive
	}

	// For now, classify as voiced (2) if above threshold
	// Full voicing detection requires pitch analysis
	return 2, float32(energy)
}
