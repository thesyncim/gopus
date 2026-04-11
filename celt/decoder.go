package celt

import (
	"errors"
	"fmt"

	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
)

// Decoding errors
var (
	// ErrInvalidFrame indicates the frame data is invalid or corrupted.
	ErrInvalidFrame = errors.New("celt: invalid frame data")

	// ErrInvalidFrameSize indicates an unsupported frame size.
	ErrInvalidFrameSize = errors.New("celt: invalid frame size")

	// ErrOutputTooSmall indicates the caller-provided PCM buffer is too small.
	ErrOutputTooSmall = errors.New("celt: output buffer too small")

	// ErrNilDecoder indicates a nil range decoder was passed.
	ErrNilDecoder = errors.New("celt: nil range decoder")
)

// Decoder decodes CELT frames from an Opus packet.
// It maintains state across frames for proper audio continuity via overlap-add
// synthesis and energy prediction.
//
// CELT is the transform-based layer of Opus, using the Modified Discrete Cosine
// Transform (MDCT) for music and general audio. The decoder reconstructs audio by:
// 1. Decoding energy envelope (coarse + fine quantization)
// 2. Decoding normalized band shapes via PVQ
// 3. Applying denormalization (scaling by energy)
// 4. Performing IMDCT synthesis with overlap-add
// 5. Applying de-emphasis filter
//
// Reference: RFC 6716 Section 4.3
type Decoder struct {
	// Configuration
	channels   int // 1 or 2
	sampleRate int // Output sample rate (typically 48000)

	// Range decoder (set per frame)
	rangeDecoder *rangecoding.Decoder
	// rangeDecoderScratch holds a reusable decoder to avoid per-frame allocations.
	rangeDecoderScratch rangecoding.Decoder

	// Energy state (persists across frames for inter-frame prediction)
	prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []float64 // Two frames ago energies (for anti-collapse)
	prevLogE    []float64 // Previous log energies (for anti-collapse history)
	prevLogE2   []float64 // Two frames ago log energies (for anti-collapse history)
	// Slow background floor estimate (libopus backgroundLogE cadence).
	backgroundEnergy []float64

	// Synthesis state (persists for overlap-add)
	overlapBuffer []float64 // Previous frame overlap tail [Overlap * channels]
	preemphState  []float64 // De-emphasis filter state [channels]

	// Postfilter state (pitch-based comb filter)
	postfilterPeriod int     // Pitch period for comb filter
	postfilterGain   float64 // Comb filter gain
	postfilterTapset int     // Filter tap configuration (0, 1, or 2)
	// Previous postfilter state for overlap cross-fade
	postfilterPeriodOld int
	postfilterGainOld   float64
	postfilterTapsetOld int
	// Postfilter history buffer (per-channel)
	postfilterMem []float64
	// PLC decode history buffer (per-channel), sized to match libopus
	// DECODE_BUFFER_SIZE cadence used by celt_plc_pitch_search().
	plcDecodeMem []float64

	// Error recovery / deterministic randomness
	rng uint32 // RNG state for PLC and folding
	// Frame counter used by debug instrumentation to correlate per-frame traces.
	decodeFrameIndex int
	// Per-decoder debug counters for PVQ/theta diagnostics.
	bandDebug bandDebugState

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State
	// CELT loss duration in libopus LM units (saturates at 10000).
	plcLossDuration int
	// Periodic PLC cadence state (mirrors libopus decode_lost() behavior).
	plcLastPitchPeriod     int
	plcPrevLossWasPeriodic bool
	// Mirrors libopus prefilter_and_fold cadence after periodic PLC.
	plcPrefilterAndFoldPending bool
	// Stored LPC coefficients per channel for periodic PLC continuation.
	plcLPC []float64

	// Band processing state
	collapseMask uint32 // Tracks which bands received pulses (for anti-collapse)

	// Bandwidth (Opus TOC-derived)
	bandwidth CELTBandwidth

	// Channel transition tracking (for mono-to-stereo overlap buffer clearing)
	prevStreamChannels int // Previous packet's channel count (0 = uninitialized)
	directOutPCM       []float32
	pendingQEXTPayload []byte
	qextOldBandE       []float64

	// Scratch buffers to reduce per-frame allocations (decoder is not thread-safe).
	scratchPrevEnergy     []float64
	scratchPrevLogE       []float64
	scratchPrevLogE2      []float64
	scratchEnergies       []float64
	scratchTFRes          []int
	scratchOffsets        []int
	scratchPulses         []int
	scratchFineQuant      []int
	scratchFinePriority   []int
	scratchPrevBandEnergy []float64
	scratchSilenceE       []float64
	scratchCaps           []int
	scratchAllocWork      []int
	scratchBands          bandDecodeScratch
	scratchIMDCT          imdctScratch
	scratchIMDCTF32       imdctScratchF32
	scratchSynth          []float64
	scratchSynthR         []float64
	scratchStereo         []float64
	scratchQEXTEnergies   []float64
	scratchQEXTSpectrumL  []float64
	scratchQEXTSpectrumR  []float64
	scratchShortCoeffs    []float64
	scratchMonoToStereoR  []float64 // For coeffsR in decodeMonoPacketToStereo (must not alias scratchSynthR used by SynthesizeStereo)
	scratchMonoMix        []float64 // For coeffsMono in decodeStereoPacketToMono (must not alias scratchShortCoeffs used by Synthesize)
	postfilterScratch     []float64
	scratchPLC            []float64 // Scratch buffer for PLC concealment samples
	scratchPLCPitchLP     []float64
	scratchPLCPitchSearch encoderScratch
	scratchPLCFIRTmp      []float64
	scratchPLCWindowed    []float64
	scratchPLCIIRMem      []float64
	scratchPLCBuf         []float64
	scratchPLCExc         []float64
	scratchPLCFoldSrc     []float64
	scratchPLCFoldDst     []float64
	scratchPLCHybridNormL []float64
	scratchPLCHybridNormR []float64
	scratchQEXTPulses     []int
	scratchQEXTFineQuant  []int

	qextRangeDecoderScratch rangecoding.Decoder
}

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

// DecodeFrame decodes a complete CELT frame from raw bytes.
// If data is nil or empty, performs Packet Loss Concealment (PLC) instead of decoding.
// data: raw CELT frame bytes (without Opus framing), or nil/empty for PLC
// frameSize: expected output samples (120, 240, 480, or 960)
// Returns: PCM samples as float64 slice, interleaved if stereo
//
// The decoding pipeline:
// 1. Initialize range decoder
// 2. Decode frame header flags (silence, transient, intra)
// 3. Decode energy envelope (coarse + fine)
// 4. Compute bit allocation
// 5. Decode bands via PVQ
// 6. Synthesis: IMDCT + windowing + overlap-add
// 7. Apply de-emphasis filter
//
// Reference: RFC 6716 Section 4.3, libopus celt/celt_decoder.c celt_decode_with_ec()
func (d *Decoder) DecodeFrame(data []byte, frameSize int) ([]float64, error) {
	// Track channel count for transition detection (normal decode uses decoder's channels)
	d.handleChannelTransition(d.channels)
	qextPayload := d.takeQEXTPayload()

	// Handle PLC for nil/empty data (lost packet)
	if data == nil || len(data) == 0 {
		return d.decodePLC(frameSize)
	}

	if !ValidFrameSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	currentFrame := d.decodeFrameIndex
	d.decodeFrameIndex++
	if tmpPVQCallDebugEnabled {
		d.bandDebug.qDbgDecodeFrame = currentFrame
		d.bandDebug.pvqCallSeq = 0
	}

	d.prepareMonoEnergyFromStereo()

	// Initialize range decoder
	rd := &d.rangeDecoderScratch
	rd.Init(data)
	d.SetRangeDecoder(rd)

	// Get mode configuration
	mode := GetModeConfig(frameSize)
	lm := mode.LM
	end := EffectiveBandsForFrameSize(d.bandwidth, frameSize)
	if end > mode.EffBands {
		end = mode.EffBands
	}
	if end < 1 {
		end = 1
	}
	start := 0
	prev1Energy := ensureFloat64Slice(&d.scratchPrevEnergy, len(d.prevEnergy))
	copy(prev1Energy, d.prevEnergy)
	prev1LogE := ensureFloat64Slice(&d.scratchPrevLogE, len(d.prevLogE))
	copy(prev1LogE, d.prevLogE)
	prev2LogE := ensureFloat64Slice(&d.scratchPrevLogE2, len(d.prevLogE2))
	copy(prev2LogE, d.prevLogE2)

	totalBits := len(data) * 8
	tell := rd.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	if silence {
		samples := d.decodeSilenceFrame(frameSize, 0, 0, 0)
		silenceE := ensureFloat64Slice(&d.scratchSilenceE, MaxBands*d.channels)
		for i := range silenceE {
			silenceE[i] = -28.0
		}
		d.updateLogE(silenceE, MaxBands, false)
		d.SetPrevEnergyWithPrev(prev1Energy, silenceE)
		d.updateBackgroundEnergy(lm)
		traceHeader(frameSize, d.channels, lm, 0, 0)
		traceEnergy(0, 0, 0, 0)
		traceLen := len(samples)
		if traceLen > 16 {
			traceLen = 16
		}
		if traceLen > 0 {
			traceSynthesis("final", samples[:traceLen])
		}
		d.resetPLCCadence(frameSize, d.channels)
		d.rng = rd.Range()
		return samples, nil
	}

	postfilterGain := 0.0
	postfilterPeriod := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			postfilterPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				postfilterTapset = rd.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = 0.09375 * float64(qg+1)
		}
		tell = rd.Tell()
	}
	traceRange("postfilter", rd)

	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = rd.DecodeBit(3) == 1
		tell = rd.Tell()
	}
	intra := false
	if tell+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	traceRange("intra", rd)

	// Trace frame header
	traceHeader(frameSize, d.channels, lm, boolToInt(intra), boolToInt(transient))
	d.applyLossEnergySafety(intra, start, end, lm)

	// Determine short blocks for transient mode
	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 1: Decode coarse energy
	energies := d.decodeCoarseEnergyInto(ensureFloat64Slice(&d.scratchEnergies, end*d.channels), end, intra, lm)
	traceRange("coarse", rd)

	tfRes := ensureIntSlice(&d.scratchTFRes, end)
	tfDecode(start, end, transient, tfRes, lm, rd)
	traceRange("tf", rd)

	spread := spreadNormal
	tell = rd.Tell()
	if tell+4 <= totalBits {
		spread = rd.DecodeICDF(spreadICDF, 5)
	}
	traceFlag("spread", spread)
	traceRange("spread", rd)

	cap := ensureIntSlice(&d.scratchCaps, end)
	initCapsInto(cap, end, lm, d.channels)
	offsets := ensureIntSlice(&d.scratchOffsets, end)
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	tellFrac := rd.TellFrac()
	for i := start; i < end; i++ {
		width := d.channels * (EBands[i+1] - EBands[i]) << lm
		quanta := min(width<<bitRes, max(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for ; tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3 && boost < cap[i]; j++ {
			flag := rd.DecodeBit(uint(dynallocLoopLogp))
			tellFrac = rd.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBitsQ3 -= quanta
			dynallocLoopLogp = 1
		}
		offsets[i] = boost
		traceAllocation(i, boost, -1)
		if j > 0 {
			dynallocLogp = max(2, dynallocLogp-1)
		}
	}
	traceRange("dynalloc", rd)

	allocTrim := 5
	encodedTrim := tellFrac+(6<<bitRes) <= totalBitsQ3
	if encodedTrim {
		allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	traceFlag("alloc_trim", allocTrim)
	traceRange("trim", rd)

	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	antiCollapseRsv := 0
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= antiCollapseRsv

	pulses := ensureIntSlice(&d.scratchPulses, end)
	fineQuant := ensureIntSlice(&d.scratchFineQuant, end)
	finePriority := ensureIntSlice(&d.scratchFinePriority, end)
	intensity := 0
	dualStereo := 0
	balance := 0
	allocScratch := d.allocationScratch()
	codedBands := cltComputeAllocationWithScratch(start, end, offsets, cap, allocTrim, &intensity, &dualStereo,
		bitsQ3, &balance, pulses, fineQuant, finePriority, d.channels, lm, rd, allocScratch)
	traceRange("alloc", rd)

	for i := start; i < end; i++ {
		width := 0
		if i+1 < len(EBands) {
			width = (EBands[i+1] - EBands[i]) << lm
		}
		k := 0
		if width > 0 {
			k = bitsToK(pulses[i], width)
		}
		traceAllocation(i, pulses[i], k)
		traceFineBits(i, fineQuant[i])
	}

	d.DecodeFineEnergy(energies, end, fineQuant)
	qext := d.prepareQEXTDecode(qextPayload, rd, end, lm, frameSize)
	if qext != nil {
		d.decodeFineEnergyWithDecoderPrev(qext.dec, energies, end, fineQuant, qext.extraQuant[:end])
		if tmpQEXTHeaderDumpEnabled {
			fmt.Printf("QEXT_MAIN_FINE_DEC channels=%d tell=%d\n", d.channels, qext.dec.TellFrac())
		}
	}
	traceRange("fine", rd)

	coeffsL, coeffsR, collapse := quantAllBandsDecodeWithScratch(rd, d.channels, frameSize, lm, start, end, pulses, shortBlocks, spread,
		dualStereo, intensity, tfRes, (totalBits<<bitRes)-antiCollapseRsv, balance, codedBands, d.channels == 1, &d.rng, &d.scratchBands, &d.bandDebug,
		func() *rangecoding.Decoder {
			if qext == nil {
				return nil
			}
			return qext.dec
		}(), func() []int {
			if qext == nil {
				return nil
			}
			return qext.extraPulses[:end]
		}(), func() int {
			if qext == nil {
				return 0
			}
			return qext.totalBitsQ3
		}())
	if qext != nil {
		d.decodeQEXTBands(frameSize, lm, shortBlocks, spread, d.channels == 1, qext)
	}
	traceRange("pvq", rd)

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = rd.DecodeRawBits(1) == 1
	}
	traceFlag("anticollapse_on", boolToInt(antiCollapseOn))
	traceRange("anticollapse", rd)

	bitsLeft := totalBits - rd.Tell()
	if len(qextPayload) != 0 {
		d.DecodeEnergyFinaliseRange(start, end, nil, fineQuant, finePriority, bitsLeft)
	} else {
		d.DecodeEnergyFinalise(energies, end, fineQuant, finePriority, bitsLeft)
	}
	traceRange("finalise", rd)

	if antiCollapseOn {
		antiCollapse(coeffsL, coeffsR, collapse, lm, d.channels, start, end, energies, prev1LogE, prev2LogE, pulses, d.rng)
	}
	d.applyPendingPLCPrefilterAndFold()

	// Step 6: Synthesis (IMDCT + window + overlap-add)
	var samples []float64
	directStereoFloat32 := d.channels == 2 && len(d.directOutPCM) >= frameSize*2
	directMonoFloat32 := d.channels == 1 &&
		len(d.directOutPCM) >= frameSize &&
		!transient &&
		d.postfilterGainOld == 0 &&
		d.postfilterGain == 0 &&
		postfilterGain == 0

	if d.channels == 2 {
		energiesL := energies[:end]
		energiesR := energies[end:]
		if qext != nil && qext.end > 0 {
			specL := ensureFloat64Slice(&d.scratchQEXTSpectrumL, len(coeffsL))
			specR := ensureFloat64Slice(&d.scratchQEXTSpectrumR, len(coeffsR))
			denormalizeBandsPackedInto(specL, coeffsL, energiesL, 0, end, lm, EBands[:])
			denormalizeBandsPackedInto(specR, coeffsR, energiesR, 0, end, lm, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands)
			}
			if qext.coeffsR != nil {
				denormalizeBandsPackedInto(specR, qext.coeffsR, qext.energies[qext.end:], 0, qext.end, lm, qext.cfg.EBands)
			}
			coeffsL = specL
			coeffsR = specR
		} else {
			denormalizeCoeffs(coeffsL, energiesL, end, frameSize)
			denormalizeCoeffs(coeffsR, energiesR, end, frameSize)
		}
		if directStereoFloat32 {
			samplesL, samplesR := d.synthesizeStereoPlanar(coeffsL, coeffsR, transient, shortBlocks)
			if !tmpDisablePostfilterEnabled {
				d.applyPostfilterStereoPlanar(samplesL, samplesR, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			d.applyDeemphasisAndScaleStereoPlanarToFloat32(d.directOutPCM[:frameSize*2], samplesL, samplesR, 1.0/32768.0)
		} else {
			samples = d.SynthesizeStereo(coeffsL, coeffsR, transient, shortBlocks)
		}
	} else {
		if qext != nil && qext.end > 0 {
			specL := ensureFloat64Slice(&d.scratchQEXTSpectrumL, len(coeffsL))
			denormalizeBandsPackedInto(specL, coeffsL, energies, 0, end, lm, EBands[:])
			if qext.coeffsL != nil {
				denormalizeBandsPackedInto(specL, qext.coeffsL, qext.energies[:qext.end], 0, qext.end, lm, qext.cfg.EBands)
			}
			coeffsL = specL
		} else {
			denormalizeCoeffs(coeffsL, energies, end, frameSize)
		}
		if directMonoFloat32 {
			samplesF32 := d.synthesizeMonoLongToFloat32(coeffsL)
			if !tmpDisablePostfilterEnabled {
				d.applyPostfilterNoGainMonoFromFloat32(samplesF32, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
			}
			d.applyDeemphasisAndScaleMonoFloat32ToFloat32(d.directOutPCM[:frameSize], samplesF32, 1.0/32768.0)
		} else {
			samples = d.Synthesize(coeffsL, transient, shortBlocks)
		}
	}

	if !directStereoFloat32 && !directMonoFloat32 {
		// Trace synthesis output before postfilter/de-emphasis for libopus comparison.
		traceLen := len(samples)
		if traceLen > 16 {
			traceLen = 16
		}
		traceSynthesis("synth_pre", samples[:traceLen])

		if !tmpDisablePostfilterEnabled {
			d.applyPostfilter(samples, frameSize, mode.LM, postfilterPeriod, postfilterGain, postfilterTapset)
		}

		// Step 7: Apply de-emphasis filter
		if len(d.directOutPCM) >= len(samples) {
			d.applyDeemphasisAndScaleToFloat32(d.directOutPCM[:len(samples)], samples, 1.0/32768.0)
		} else {
			d.applyDeemphasisAndScale(samples, 1.0/32768.0)
		}

		// Trace final synthesis output
		traceLen = len(samples)
		if traceLen > 16 {
			traceLen = 16
		}
		traceSynthesis("final", samples[:traceLen])
	}

	// Update energy state for next frame
	d.updateLogE(energies, end, transient)
	d.SetPrevEnergyWithPrev(prev1Energy, energies)
	d.updateBackgroundEnergy(lm)
	// Mirror libopus: clear energies/logs outside [start,end).
	for c := 0; c < d.channels; c++ {
		base := c * MaxBands
		for band := 0; band < start; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
		for band := end; band < MaxBands; band++ {
			d.prevEnergy[base+band] = 0
			d.prevLogE[base+band] = -28.0
			d.prevLogE2[base+band] = -28.0
		}
	}
	if qext != nil && qext.dec.Tell() > qext.dec.StorageBits() {
		return nil, ErrInvalidFrame
	}
	var extDec *rangecoding.Decoder
	if qext != nil {
		extDec = qext.dec
	}
	d.rng = combineFinalRange(rd, extDec)

	// Reset PLC state after successful decode
	d.resetPLCCadence(frameSize, d.channels)

	return samples, nil
}
