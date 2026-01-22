// Package encoder implements FEC (Forward Error Correction) using SILK's LBRR mechanism.
// FEC enables loss recovery by encoding a low-bitrate redundant copy of the previous
// frame within the current packet. When packets are lost, the decoder can recover
// using the LBRR data from the next packet.
//
// Reference: RFC 6716 Section 4.2.4

package encoder

// FEC Constants
const (
	// LBRRBitrateFactor is the bitrate reduction for LBRR encoding.
	// LBRR uses ~60% of normal SILK bitrate.
	LBRRBitrateFactor = 0.6

	// MinPacketLossForFEC is the minimum expected loss to enable FEC.
	// Below this, FEC overhead isn't worth it.
	MinPacketLossForFEC = 1

	// MaxPacketLossForFEC is where FEC becomes less effective.
	// Above this, increasing primary bitrate is better.
	MaxPacketLossForFEC = 50

	// MinSILKBitrate is the minimum bitrate for SILK encoding (6 kbps).
	MinSILKBitrate = 6000
)

// fecState holds state for FEC encoding.
type fecState struct {
	// Previous frame data for LBRR encoding
	prevFrame []float32

	// VAD flag from previous frame
	prevVADFlag bool

	// Frame count for multi-frame LBRR selection
	frameCount int
}

// newFECState creates initial FEC state.
func newFECState() *fecState {
	return &fecState{
		prevFrame:   nil,
		prevVADFlag: false,
		frameCount:  0,
	}
}

// computeLBRRBitrate calculates bitrate for LBRR encoding.
func computeLBRRBitrate(normalBitrate int) int {
	lbrrBitrate := int(float64(normalBitrate) * LBRRBitrateFactor)
	if lbrrBitrate < MinSILKBitrate {
		return MinSILKBitrate
	}
	return lbrrBitrate
}
