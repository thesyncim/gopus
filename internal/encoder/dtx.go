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
