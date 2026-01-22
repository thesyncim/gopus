// Package encoder implements DTX (Discontinuous Transmission) for the Opus encoder.
// DTX saves bandwidth during silence by suppressing packets and sending periodic
// comfort noise frames to maintain presence.
//
// Reference: RFC 6716 Section 2.1.9
package encoder

// DTX Constants
const (
	// DTXComfortNoiseIntervalMs is how often to send comfort noise during DTX.
	// Per Opus convention, send a comfort noise frame every 400ms of silence.
	DTXComfortNoiseIntervalMs = 400

	// DTXFrameThreshold is the number of consecutive silent frames before DTX activates.
	// At 20ms frames: 20 frames = 400ms of silence before DTX mode.
	DTXFrameThreshold = 20

	// DTXFadeInMs is the fade-in duration when exiting DTX mode.
	DTXFadeInMs = 10

	// DTXFadeOutMs is the fade-out duration when entering DTX mode.
	DTXFadeOutMs = 10

	// DTXMinBitrate is the minimum bitrate used for comfort noise frames.
	// This is used internally for DTX encoding.
	DTXMinBitrate = 6000
)

// dtxState holds state for discontinuous transmission.
type dtxState struct {
	// Consecutive silent frames count
	silentFrameCount int

	// Whether currently in DTX mode
	inDTXMode bool

	// Frames since last comfort noise packet
	framesSinceComfortNoise int

	// Saved filter state for comfort noise
	savedFilterState []float64
}

// newDTXState creates initial DTX state.
func newDTXState() *dtxState {
	return &dtxState{
		silentFrameCount:        0,
		inDTXMode:               false,
		framesSinceComfortNoise: 0,
		savedFilterState:        nil,
	}
}

// reset resets DTX state when speech resumes.
func (d *dtxState) reset() {
	d.silentFrameCount = 0
	d.inDTXMode = false
	d.framesSinceComfortNoise = 0
}

// shouldUseDTX determines if frame should be suppressed (DTX mode).
// Returns: (suppressFrame bool, sendComfortNoise bool)
func (e *Encoder) shouldUseDTX(pcm []float64) (bool, bool) {
	if !e.dtxEnabled {
		return false, false
	}

	// Use energy-based silence detection
	pcm32 := toFloat32(pcm)
	signalType, _ := classifySignal(pcm32)

	if signalType == 0 {
		// Silence detected
		e.dtx.silentFrameCount++

		if e.dtx.silentFrameCount >= DTXFrameThreshold {
			// Enter or stay in DTX mode
			e.dtx.inDTXMode = true
			e.dtx.framesSinceComfortNoise++

			// Send comfort noise periodically
			framesPerInterval := DTXComfortNoiseIntervalMs / 20 // 20ms frames
			if e.dtx.framesSinceComfortNoise >= framesPerInterval {
				e.dtx.framesSinceComfortNoise = 0
				return true, true // Suppress but send comfort noise
			}

			return true, false // Suppress entirely
		}
	} else {
		// Speech detected - exit DTX mode
		e.dtx.reset()
	}

	return false, false
}

// classifySignal determines signal type using energy-based detection.
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

// encodeComfortNoise encodes a comfort noise frame.
// Comfort noise provides natural-sounding silence during DTX.
func (e *Encoder) encodeComfortNoise(frameSize int) ([]byte, error) {
	// Generate low-level noise to maintain presence
	noise := make([]float64, frameSize*e.channels)
	for i := range noise {
		// Very low amplitude noise (-60 dBFS)
		noise[i] = (e.nextRandom() - 0.5) * 0.002
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

// toFloat32 converts float64 slice to float32.
func toFloat32(pcm []float64) []float32 {
	result := make([]float32, len(pcm))
	for i, v := range pcm {
		result[i] = float32(v)
	}
	return result
}
