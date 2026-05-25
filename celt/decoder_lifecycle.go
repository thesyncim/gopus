package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/plc"
)

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
		downsample: 1,

		// Allocate energy arrays for all bands and channels
		prevEnergy:       make([]celtGLog, MaxBands*channels),
		prevEnergy2:      make([]celtGLog, MaxBands*channels),
		prevLogE:         make([]celtGLog, MaxBands*channels),
		prevLogE2:        make([]celtGLog, MaxBands*channels),
		backgroundEnergy: make([]celtGLog, MaxBands*channels),

		// Overlap buffer for CELT (full overlap per channel)
		overlapBuffer: make([]celtSig, Overlap*channels),

		// De-emphasis filter state, one per channel
		preemphState: make([]celtSig, channels),

		// Postfilter history buffer for comb filter
		postfilterMem: make([]celtSig, combFilterHistory*channels),
		// PLC decode history sized to libopus DEC_PITCH_BUF_SIZE.
		plcDecodeMem: make([]celtSig, plcDecodeBufferSize*channels),
		plcLPC:       make([]float32, celtPLCLPCOrder*channels),

		// RNG state (libopus initializes to zero)
		rng: 0,

		bandwidth:              CELTFullband,
		phaseInversionDisabled: channels == 1,
		plcState:               plc.NewState(),
	}

	// Match libopus init/reset defaults (oldLogE/oldLogE2 = -28, buffers cleared).
	d.Reset()

	return d
}

// SetPhaseInversionDisabled toggles stereo phase inversion during CELT decoding.
func (d *Decoder) SetPhaseInversionDisabled(disabled bool) {
	d.phaseInversionDisabled = disabled
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (d *Decoder) PhaseInversionDisabled() bool {
	return d.phaseInversionDisabled
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	channels := d.channels
	if channels < 1 {
		channels = 1
	} else if channels > 2 {
		channels = 2
	}
	sampleRate := d.sampleRate
	if sampleRate == 0 {
		sampleRate = 48000
	}
	downsample := d.downsample
	if downsample <= 0 {
		downsample = 1
	}
	phaseInversionDisabled := d.phaseInversionDisabled
	complexity := d.complexity

	*d = Decoder{
		channels:   channels,
		sampleRate: sampleRate,
		downsample: downsample,

		prevEnergy:       make([]celtGLog, MaxBands*channels),
		prevEnergy2:      make([]celtGLog, MaxBands*channels),
		prevLogE:         make([]celtGLog, MaxBands*channels),
		prevLogE2:        make([]celtGLog, MaxBands*channels),
		backgroundEnergy: make([]celtGLog, MaxBands*channels),

		overlapBuffer: make([]celtSig, Overlap*channels),
		preemphState:  make([]celtSig, channels),

		postfilterMem: make([]celtSig, combFilterHistory*channels),
		plcDecodeMem:  make([]celtSig, plcDecodeBufferSize*channels),
		plcLPC:        make([]float32, celtPLCLPCOrder*channels),

		bandwidth:              CELTFullband,
		phaseInversionDisabled: phaseInversionDisabled,
		complexity:             complexity,
		plcState:               plc.NewState(),
		plcSkip:                true,
		plcLastFrameType:       frameNone,
	}

	for i := range d.prevLogE {
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
	}
	if extsupport.QEXT {
		d.clearQEXTState()
	}
}
