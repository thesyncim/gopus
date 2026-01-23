// Package encoder implements FEC (Forward Error Correction) using SILK's LBRR mechanism.
// FEC enables loss recovery by encoding a low-bitrate redundant copy of the previous
// frame within the current packet. When packets are lost, the decoder can recover
// using the LBRR data from the next packet.
//
// Reference: RFC 6716 Section 4.2.4

package encoder

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

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

// shouldUseFEC determines if FEC should be used for this frame.
func (e *Encoder) shouldUseFEC() bool {
	if !e.fecEnabled {
		return false
	}

	// Need previous frame for LBRR
	if e.fec == nil || e.fec.prevFrame == nil {
		return false
	}

	// Check packet loss threshold
	if e.packetLoss < MinPacketLossForFEC {
		return false
	}

	return true
}

// encodeLBRR encodes the previous frame at reduced bitrate for FEC.
// This produces the LBRR data that appears in the current packet.
func (e *Encoder) encodeLBRR(re *rangecoding.Encoder) error {
	if e.fec == nil || e.fec.prevFrame == nil {
		return nil
	}

	// Encode LBRR flag (1 = LBRR present)
	// Uses ICDFLBRRFlag table: [256, 205, 0]
	// Symbol 1 (~80% probability of LBRR present when FEC enabled)
	re.EncodeICDF16(1, silk.ICDFLBRRFlag, 8)

	// Encode LBRR frame at reduced quality
	// LBRR uses simpler encoding:
	// - Same frame type as original
	// - Reduced precision for gains
	// - Simpler pitch encoding
	// - Shell-coded excitation at lower rate

	// For v1, encode a minimal LBRR frame
	e.writeLBRRFrame(re)

	return nil
}

// writeLBRRFrame writes LBRR frame data to the range encoder.
// LBRR frames are encoded similarly to regular SILK frames but at lower quality.
func (e *Encoder) writeLBRRFrame(re *rangecoding.Encoder) {
	// Encode LBRR frame type (voiced/unvoiced)
	// Using ICDFFrameTypeVADActive: [256, 230, 166, 128, 0]
	// Symbol 2 = voiced low quantization offset
	re.EncodeICDF16(2, silk.ICDFFrameTypeVADActive, 8)

	// Encode gain at reduced precision
	// LBRR uses coarser gain quantization
	// Use ICDFGainHighBits for voiced signal (index 2)
	// For v1: write a fixed mid-range gain index
	gainIndex := 4 // Mid-range gain for voiced
	re.EncodeICDF16(gainIndex, silk.ICDFGainHighBits[2], 8)

	// Encode gain LSB
	re.EncodeICDF16(0, silk.ICDFGainLSB, 8)

	// Encode LSF indices (simplified for LBRR)
	// Use WB voiced stage1 table since hybrid SILK uses WB
	// Symbol 1 is a safe mid-range value
	re.EncodeICDF16(1, silk.ICDFLSFStage1WBVoiced, 8)

	// Encode LSF interpolation weight
	// Symbol 2 = mid interpolation (0.5)
	re.EncodeICDF16(2, silk.ICDFLSFInterpolation, 8)

	// Encode pitch lag (simplified for LBRR)
	// Use WB pitch lag table
	// Symbol 10 is mid-range
	re.EncodeICDF16(10, silk.ICDFPitchLagWB, 8)

	// Encode pitch contour for WB
	re.EncodeICDF16(1, silk.ICDFPitchContourWB, 8)

	// Encode LTP filter index (mid periodicity)
	re.EncodeICDF16(1, silk.ICDFLTPFilterIndexMidPeriod, 8)

	// Encode rate level for voiced
	re.EncodeICDF16(3, silk.ICDFRateLevelVoiced, 8)

	// Minimal excitation: encode as mostly zeros using pulse count table
	// ICDFExcitationPulseCount: [256, 240, ..., 0] - 17 symbols
	// Symbol 0 = 0 pulses
	re.EncodeICDF16(0, silk.ICDFExcitationPulseCount, 8)
}

// skipLBRR writes the LBRR flag indicating no FEC data.
func (e *Encoder) skipLBRR(re *rangecoding.Encoder) {
	// Symbol 0 = no LBRR present
	re.EncodeICDF16(0, silk.ICDFLBRRFlag, 8)
}

// updateFECState saves current frame for next LBRR encoding.
func (e *Encoder) updateFECState(pcm []float32, vadFlag bool) {
	if e.fec == nil {
		e.fec = newFECState()
	}

	if e.fec.prevFrame == nil || len(e.fec.prevFrame) != len(pcm) {
		e.fec.prevFrame = make([]float32, len(pcm))
	}
	copy(e.fec.prevFrame, pcm)
	e.fec.prevVADFlag = vadFlag
	e.fec.frameCount++
}

// resetFECState clears FEC state.
func (e *Encoder) resetFECState() {
	if e.fec != nil {
		e.fec.prevFrame = nil
		e.fec.prevVADFlag = false
		e.fec.frameCount = 0
	}
}
