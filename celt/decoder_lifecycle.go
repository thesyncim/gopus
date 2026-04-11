package celt

import "github.com/thesyncim/gopus/plc"

// NewDecoder creates a new CELT decoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The decoder is ready to process CELT frames after creation.
func NewDecoder(channels int) *Decoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	d := &Decoder{
		channels:   channels,
		sampleRate: 48000, // CELT always operates at 48kHz internally

		// Allocate energy arrays for all bands and channels
		prevEnergy:       make([]float64, MaxBands*channels),
		prevEnergy2:      make([]float64, MaxBands*channels),
		prevLogE:         make([]float64, MaxBands*channels),
		prevLogE2:        make([]float64, MaxBands*channels),
		backgroundEnergy: make([]float64, MaxBands*channels),
		qextOldBandE:     make([]float64, MaxBands*channels),

		// Overlap buffer for CELT (full overlap per channel)
		overlapBuffer: make([]float64, Overlap*channels),

		// De-emphasis filter state, one per channel
		preemphState: make([]float64, channels),

		// Postfilter history buffer for comb filter
		postfilterMem: make([]float64, combFilterHistory*channels),
		// PLC decode history sized to libopus DEC_PITCH_BUF_SIZE.
		plcDecodeMem: make([]float64, plcDecodeBufferSize*channels),
		plcLPC:       make([]float64, celtPLCLPCOrder*channels),

		// RNG state (libopus initializes to zero)
		rng: 0,

		bandwidth: CELTFullband,
		plcState:  plc.NewState(),
	}

	// Match libopus init/reset defaults (oldLogE/oldLogE2 = -28, buffers cleared).
	d.Reset()

	return d
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	// Clear energy arrays (match libopus reset: oldBandE=0, oldLogE/oldLogE2=-28).
	for i := range d.prevEnergy {
		d.prevEnergy[i] = 0
		d.prevEnergy2[i] = 0
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
		d.backgroundEnergy[i] = 0
	}

	// Clear overlap buffer
	for i := range d.overlapBuffer {
		d.overlapBuffer[i] = 0
	}

	// Clear de-emphasis state
	for i := range d.preemphState {
		d.preemphState[i] = 0
	}
	for i := range d.plcDecodeMem {
		d.plcDecodeMem[i] = 0
	}
	for i := range d.plcLPC {
		d.plcLPC[i] = 0
	}

	// Reset postfilter
	d.resetPostfilterState()

	// Reset RNG (libopus resets to zero)
	d.rng = 0
	d.decodeFrameIndex = 0
	d.bandDebug = bandDebugState{}
	d.plcLastPitchPeriod = 0
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	d.plcLossDuration = 0

	// Clear range decoder reference
	d.rangeDecoder = nil
	d.pendingQEXTPayload = nil
	for i := range d.qextOldBandE {
		d.qextOldBandE[i] = 0
	}

	// Reset bandwidth to fullband
	d.bandwidth = CELTFullband

	// Reset channel transition tracking
	d.prevStreamChannels = 0

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
}
