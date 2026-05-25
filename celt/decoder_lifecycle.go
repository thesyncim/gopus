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

	prevEnergy := ensureGLogSlice(&d.prevEnergy, MaxBands*channels)
	prevEnergy2 := ensureGLogSlice(&d.prevEnergy2, MaxBands*channels)
	prevLogE := ensureGLogSlice(&d.prevLogE, MaxBands*channels)
	prevLogE2 := ensureGLogSlice(&d.prevLogE2, MaxBands*channels)
	backgroundEnergy := ensureGLogSlice(&d.backgroundEnergy, MaxBands*channels)
	overlapBuffer := ensureSigSlice(&d.overlapBuffer, Overlap*channels)
	preemphState := ensureSigSlice(&d.preemphState, channels)
	postfilterMem := ensureSigSlice(&d.postfilterMem, combFilterHistory*channels)
	plcDecodeMem := ensureSigSlice(&d.plcDecodeMem, plcDecodeBufferSize*channels)
	plcLPC := ensureFloat32Slice(&d.plcLPC, celtPLCLPCOrder*channels)
	clear(prevEnergy)
	clear(prevEnergy2)
	clear(prevLogE)
	clear(prevLogE2)
	clear(backgroundEnergy)
	clear(overlapBuffer)
	clear(preemphState)
	clear(postfilterMem)
	clear(plcDecodeMem)
	clear(plcLPC)
	plcState := d.plcState
	if plcState == nil {
		plcState = plc.NewState()
	} else {
		plcState.Reset()
		plcState.SetLastFrameParams(plc.ModeSILK, 960, 1)
	}
	d.clearDecoderScratchForReset()

	d.prevEnergy = prevEnergy
	d.prevEnergy2 = prevEnergy2
	d.prevLogE = prevLogE
	d.prevLogE2 = prevLogE2
	d.backgroundEnergy = backgroundEnergy
	d.overlapBuffer = overlapBuffer
	d.preemphState = preemphState
	d.postfilterMem = postfilterMem
	d.plcDecodeMem = plcDecodeMem
	d.plcLPC = plcLPC

	d.channels = channels
	d.sampleRate = sampleRate
	d.downsample = downsample
	d.bandwidth = CELTFullband
	d.phaseInversionDisabled = phaseInversionDisabled
	d.complexity = complexity
	d.plcState = plcState
	d.plcSkip = true
	d.plcLastFrameType = frameNone
	d.rangeDecoder = nil
	d.directOutPCM = nil
	d.rng = 0
	d.prevStreamChannels = 0
	d.postfilterMemFromPLC = false
	d.postfilterMemPLCBacked = false
	d.postfilterPeriod = 0
	d.postfilterGain = 0
	d.postfilterTapset = 0
	d.postfilterPeriodOld = 0
	d.postfilterGainOld = 0
	d.postfilterTapsetOld = 0
	d.plcDecodeMemRingActive = false
	d.plcDecodeMemRingStart = 0
	d.plcLastPitchPeriod = 0
	d.plcPrevLossWasPeriodic = false
	d.plcPrefilterAndFoldPending = false
	d.plcLossDuration = 0
	d.plcDuration = 0
	d.collapseMask = 0
	d.redundancyActive = false
	d.redundancyBytes = nil
	d.redundancyRange = 0
	d.redundancyFrameSize = 0

	for i := range d.prevLogE {
		d.prevLogE[i] = -28.0
		d.prevLogE2[i] = -28.0
	}
	if extsupport.QEXT {
		d.clearQEXTState()
	}
}

func (d *Decoder) clearDecoderScratchForReset() {
	clear(d.scratchPrevEnergy)
	clear(d.scratchPrevEnergyGLog)
	clear(d.scratchEnergies)
	clear(d.scratchTFRes)
	clear(d.scratchOffsets)
	clear(d.scratchPulses)
	clear(d.scratchFineQuant)
	clear(d.scratchFinePriority)
	clear(d.scratchPrevBandEnergy)
	clear(d.scratchSilenceE)
	clear(d.scratchCaps)
	clear(d.scratchAllocWork)
	d.scratchBands.clearForReset()
	d.scratchIMDCTF32.clearForReset()
	d.scratchIMDCTF32R.clearForReset()
	clear(d.scratchSynthF32)
	clear(d.scratchSynthRF32)
	clear(d.scratchStereoF32)
	clear(d.scratchShortCoeffsF32)
	clear(d.scratchMonoToStereoRF32)
	clear(d.scratchMonoMixF32)
	clear(d.postfilterScratchF32)
	clear(d.scratchPLC)
	clear(d.scratchPLCF32)
	clear(d.scratchPLCPitchLP)
	d.scratchPLCPitchSearch.clearForReset()
	clear(d.scratchPLCFIRTmp)
	clear(d.scratchPLCWindowed)
	clear(d.scratchPLCIIRY)
	clear(d.scratchPLCBuf)
	clear(d.scratchPLCExc)
	clear(d.scratchPLCFoldSrc)
	clear(d.scratchPLCFoldDst)
	clear(d.scratchPLCHybridNormL)
	clear(d.scratchPLCHybridNormR)
}

func (s *bandDecodeScratch) clearForReset() {
	clear(s.left)
	clear(s.right)
	clear(s.collapse)
	clear(s.norm)
	clear(s.lowband)
	clear(s.coeffs)
	clear(s.bandVectors)
	clear(s.bandVectorsL)
	clear(s.bandVectorsR)
	for i := range s.bandStorage {
		clear(s.bandStorage[i])
		clear(s.bandStorageL[i])
		clear(s.bandStorageR[i])
	}
	clear(s.pvqPulses)
	clear(s.pvqRefine)
	clear(s.pvqNorm)
	clear(s.pvqNorm32)
	clear(s.foldResult)
	clear(s.cwrsU)
	clear(s.hadamardTmpNorm)
	clear(s.quantWork)
}

func (s *imdctScratchF32) clearForReset() {
	clear(s.fftIn)
	clear(s.fftTmp)
	clear(s.buf)
	clear(s.out)
}

func (s *plcPitchSearchScratch) clearForReset() {
	clear(s.xLP4)
	clear(s.yLP4)
	clear(s.xcorr)
}
