package celt

import (
	"github.com/thesyncim/gopus/internal/arena"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// encoderScratch holds pre-allocated scratch buffers for the encoder hot path.
// These buffers are reused across frames to eliminate heap allocations during encoding.
type encoderScratch struct {
	customMDCTState
	// f32 backs the frameSize-dependent float-family scratch (the many []float32 /
	// []celtSig / []celtGLog / []celtEner / []celtNorm fields below) with one
	// contiguous allocation instead of ~40 separate ones. See ensureEncodeFloatArena;
	// the per-field ensure* sizing lines reslice/clear within their cap-pinned slot.
	f32 arena.Bump[float32]
	i32 arena.Bump[int32]

	// LSB-depth quantized input buffer
	quantizedInputF32 []float32

	// DC rejection output buffer
	dcRejectedF32 []float32

	// Combined delay buffer + PCM
	combinedBufF32 []float32

	// Sub-48 kHz API-rate zero-stuffed core input (frameSize*upsample).
	upsampleStuff []float32

	// celt_encode_with_ec's planar in buffer: per channel, the overlap head
	// followed by the pre-emphasized frame.
	planarIn []float32

	// Prefilter (comb filter) scratch buffers
	prefilterPre      []celtSig
	prefilterOut      []celtSig
	prefilterPitchBuf []float32
	prefilterXLP4     []float32
	prefilterYLP4     []float32
	prefilterXcorr    []float32
	prefilterYYLookup []float32

	// MDCT coefficient buffer
	mdctCoeffsF32 []float32

	// Band energy buffers
	energies  []celtGLog
	bandLogE2 []celtGLog
	bandE     []celtEner
	bandAmp   []celtEner
	bandEL    []celtEner
	bandER    []celtEner

	// Range encoder buffer
	reBuf []byte

	// Quantized energies
	quantizedEnergies []celtGLog
	coarseError       []celtGLog
	coarseDecisionE   []celtGLog
	analysisEnergies  []celtGLog
	prev1LogE         []celtGLog
	dynallocOldBandE  []celtGLog

	// Normalized coefficient buffers (normL/normR alias the two halves of normStereo).
	normL      []celtNorm
	normR      []celtNorm
	normStereo []celtNorm

	// Allocation-related buffers
	caps    []int32
	offsets []int32
	logN    []int16

	// TF analysis buffers
	tfRes []int32

	// MDCT forward transform scratch (float32)
	mdctF           []float32
	mdctCpx         []complex64 // Backing storage for mdctFFTIn and mdctFFTOut
	mdctFFTIn       []complex64
	mdctFFTOut      []complex64
	mdctFFTTmp      []float32
	mdctBlockCoeffs []float32 // Per-block coefficients for short MDCT

	// Transient analysis scratch
	transientEnergy    []float32
	transientEnergyR   []float32
	transientX         []float32
	transientSpreadOld []celtGLog

	// ComputeAllocation scratch
	allocBits     []int32
	allocFineBits []int32
	allocFinePrio []int32
	allocThresh   []int32
	allocTrim     []int32
	allocCaps     []int32
	allocResult   AllocationResult // Pre-allocated result struct
	encoderQEXTScratchFields

	// Range encoder (reused between frames)
	rangeEncoder rangecoding.Encoder

	// Coarse-energy two-pass scratch
	coarseStartState rangecoding.EncoderState
	coarseOldStart   []celtGLog
	coarseIntraState rangecoding.EncoderState

	// Inline backing storage for fixed-size range-encoder and logN scratch.
	reBufStorage    [256]byte
	logNStorage     [MaxBands]int16
	coarseIntraOldE []celtGLog
	coarseIntraErr  []celtGLog

	// Per-mode allocation work buffer (non-standard custom modes only).
	allocWork []int32
}

// allocationScratch returns the per-mode allocation work buffer, sized for the
// active band count. Used only by the non-standard custom-mode encode path.
func (e *Encoder) allocationScratch() []int32 {
	nb := MaxBands
	if e.perMode != nil {
		nb = e.perMode.nbEBands
	}
	return ensureInt32Slice(&e.scratch.allocWork, nb*4)
}

// combScale matches celt_encoder.c opus_custom_encoder_init_arch: QEXT doubles
// pitch history for 96 kHz modes with 180- or 240-sample short transforms.
func (e *Encoder) combScale() int {
	if extsupport.QEXT && e.sampleRate == 96000 {
		if e.customScaleBase == 180 || e.customScaleBase == 240 || e.customScaleBase == 0 && e.hd96kOverlap == 240 {
			return 2
		}
	}
	return 1
}

// combMaxPeriod returns the comb-filter max period for the active mode
// (QEXT_SCALE(COMBFILTER_MAXPERIOD)).
func (e *Encoder) combMaxPeriod() int { return combFilterMaxPeriod * e.combScale() }

// combMinPeriod returns the comb-filter min period for the active mode
// (QEXT_SCALE(COMBFILTER_MINPERIOD)).
func (e *Encoder) combMinPeriod() int { return combFilterMinPeriod * e.combScale() }

// EnsureScratch ensures all scratch buffers are properly sized for the given frame size.
// Call this before using the encoder's scratch-aware methods from an external path
// (e.g., hybrid encoding) that does not go through EncodeFrame.
func (e *Encoder) EnsureScratch(frameSize int) {
	e.ensureScratch(frameSize)
}

// ensureEncodeFloatArena backs the frameSize-dependent float-family scratch with
// one contiguous arena, carving a cap-pinned slot per field. The carved length is
// 0; the per-field ensure* sizing lines in ensureScratch then set each field's
// visible length and clearing within its own slot. Re-carves only when the
// frame's total exceeds the backing capacity (i.e. a larger frameSize than seen
// before); otherwise it is a cheap early return. Layout only — bit-exact.
func (s *encoderScratch) ensureEncodeFloatArena(frameSize, channels, overlap, maxPeriod, maxPitch, modeBands int) {
	expectedLen := frameSize * channels
	combinedLen := DelayCompensation*channels + expectedLen
	transientLen := (overlap + frameSize) * channels
	prefilterLen := (maxPeriod + frameSize) * channels
	pitchBufLen := max((maxPeriod+frameSize)>>1, 1)
	xcorrLen := maxPitch >> 1
	xlp4Len := max(frameSize>>2, 1)
	ylp4Len := max((frameSize+maxPitch)>>2, 1)
	yyLookupLen := max((maxPeriod>>1)+1, 1)
	bandCount := modeBands * channels
	sp2 := (frameSize + overlap) / 2
	spc := frameSize + overlap

	total := expectedLen*2 + combinedLen + transientLen + prefilterLen*2 +
		pitchBufLen + xcorrLen + xlp4Len + ylp4Len + yyLookupLen +
		frameSize*channels + bandCount*12 + modeBands*2 +
		frameSize*channels + frameSize + frameSize/2 +
		sp2*2 + spc
	if s.f32.Cap() >= total {
		return
	}
	s.f32.Ensure(total)
	s.quantizedInputF32 = s.f32.Alloc(expectedLen)
	s.dcRejectedF32 = s.f32.Alloc(expectedLen)
	s.combinedBufF32 = s.f32.Alloc(combinedLen)
	s.planarIn = s.f32.Alloc(transientLen)
	s.prefilterPre = s.f32.Alloc(prefilterLen)
	s.prefilterOut = s.f32.Alloc(prefilterLen)
	s.prefilterPitchBuf = s.f32.Alloc(pitchBufLen)
	s.prefilterXcorr = s.f32.Alloc(xcorrLen)
	s.prefilterXLP4 = s.f32.Alloc(xlp4Len)
	s.prefilterYLP4 = s.f32.Alloc(ylp4Len)
	s.prefilterYYLookup = s.f32.Alloc(yyLookupLen)
	s.mdctCoeffsF32 = s.f32.Alloc(frameSize * channels)
	s.energies = s.f32.Alloc(bandCount)
	s.bandLogE2 = s.f32.Alloc(bandCount)
	s.bandE = s.f32.Alloc(bandCount)
	s.bandAmp = s.f32.Alloc(bandCount)
	s.coarseError = s.f32.Alloc(bandCount)
	s.quantizedEnergies = s.f32.Alloc(bandCount)
	s.prev1LogE = s.f32.Alloc(bandCount)
	s.coarseDecisionE = s.f32.Alloc(bandCount)
	s.analysisEnergies = s.f32.Alloc(bandCount)
	s.dynallocOldBandE = s.f32.Alloc(bandCount)
	s.transientSpreadOld = s.f32.Alloc(bandCount)
	s.coarseOldStart = s.f32.Alloc(bandCount)
	s.bandEL = s.f32.Alloc(modeBands)
	s.bandER = s.f32.Alloc(modeBands)
	s.normStereo = s.f32.Alloc(frameSize * channels)
	s.normL = nil
	s.normR = nil
	s.mdctF = s.f32.Alloc(frameSize)
	s.mdctBlockCoeffs = s.f32.Alloc(frameSize / 2)
	s.transientEnergy = s.f32.Alloc(sp2)
	s.transientEnergyR = s.f32.Alloc(sp2)
	s.transientX = s.f32.Alloc(spc)
}

// ensureScratch ensures all scratch buffers are properly sized for the given frame parameters.
// Call this at the start of EncodeFrame to prepare buffers for reuse.
func (e *Encoder) ensureScratch(frameSize int) {
	shape := encodeScratchShape{
		frameSize: frameSize,
		channels:  int(e.channels),
		overlap:   e.analysisOverlap(),
		combScale: e.combScale(),
		perMode:   e.perMode,
		reserve:   e.complexity >= 4,
		qext:      extsupport.QEXT && e.qextActive(),
	}
	if e.scratchShapeValid && shape == e.scratchShape {
		e.resetScratchLengths(frameSize)
		return
	}
	// QEXT frames size extra buffers and reassign some of the energy slices, so
	// they always take the full sizing path.
	e.scratchShapeValid = !shape.qext
	e.scratchShape = shape
	channels := int(e.channels)
	modeBands := e.predStride()
	expectedLen := frameSize * channels
	overlap := min(e.analysisOverlap(), frameSize)

	s := &e.scratch
	if e.complexity >= 4 {
		// quant_coarse_energy() in celt/quant_bands.c saves the trial's
		// packet bytes. Reserve the base-packet bound so varying VBR budgets
		// reuse the snapshot storage throughout the encoder's lifetime.
		s.coarseIntraState.ReserveBufferCapacity(celtPacketSizeCap)
	}

	// Carve the frameSize-dependent float-family scratch from one contiguous
	// arena first; the per-field sizing below reslices/clears within each slot.
	maxPeriod := max(e.combMaxPeriod(), e.combMinPeriod())
	maxPitch := max(maxPeriod-3*e.combMinPeriod(), 1)
	s.ensureEncodeFloatArena(frameSize, channels, overlap, maxPeriod, maxPitch, modeBands)

	// DC rejection and LSB-depth quantization output
	s.quantizedInputF32 = ensureFloat32Slice(&s.quantizedInputF32, expectedLen)
	s.dcRejectedF32 = ensureFloat32Slice(&s.dcRejectedF32, expectedLen)

	// Combined delay buffer
	delayComp := DelayCompensation * channels
	combinedLen := delayComp + expectedLen
	s.combinedBufF32 = ensureFloat32Slice(&s.combinedBufF32, combinedLen)

	// Transient analysis input (overlap + frameSize) * channels
	transientLen := (overlap + frameSize) * channels
	s.planarIn = ensureFloat32Slice(&s.planarIn, transientLen)

	// Prefilter scratch buffers
	prefilterLen := (maxPeriod + frameSize) * channels
	s.prefilterPre = ensureSigSliceNoClear(&s.prefilterPre, prefilterLen)
	s.prefilterOut = ensureSigSliceNoClear(&s.prefilterOut, prefilterLen)
	pitchBufLen := max((maxPeriod+frameSize)>>1, 1)
	s.prefilterPitchBuf = ensureFloat32Slice(&s.prefilterPitchBuf, pitchBufLen)
	s.prefilterXcorr = ensureFloat32Slice(&s.prefilterXcorr, maxPitch>>1)
	xlp4Len := max(frameSize>>2, 1)
	s.prefilterXLP4 = ensureFloat32Slice(&s.prefilterXLP4, xlp4Len)
	lag := frameSize + maxPitch
	ylp4Len := max(lag>>2, 1)
	s.prefilterYLP4 = ensureFloat32Slice(&s.prefilterYLP4, ylp4Len)
	yyLookupLen := max((maxPeriod>>1)+1, 1)
	s.prefilterYYLookup = ensureFloat32Slice(&s.prefilterYYLookup, yyLookupLen)

	// MDCT coefficients
	s.mdctCoeffsF32 = ensureFloat32Slice(&s.mdctCoeffsF32, frameSize*channels)

	// Band energies
	bandCount := modeBands * channels
	s.energies = ensureGLogSlice(&s.energies, bandCount)
	s.bandLogE2 = ensureGLogSlice(&s.bandLogE2, bandCount)
	s.bandE = ensureEnerSlice(&s.bandE, bandCount)
	s.bandAmp = ensureEnerSlice(&s.bandAmp, bandCount)
	s.coarseError = ensureGLogSlice(&s.coarseError, bandCount)
	s.bandEL = ensureEnerSlice(&s.bandEL, modeBands)
	s.bandER = ensureEnerSlice(&s.bandER, modeBands)

	// History buffers

	// Range encoder buffer
	bufSize := 256
	if len(s.reBuf) < bufSize {
		s.reBuf = s.reBufStorage[:]
	}
	if extsupport.QEXT && e.qextActive() {
		qs := s.ensureQEXTScratch()
		qs.buf = ensureByteSlice(&qs.buf, qextPacketSizeCap)
	}

	// Quantized energies
	s.quantizedEnergies = ensureGLogSlice(&s.quantizedEnergies, bandCount)
	s.prev1LogE = ensureGLogSlice(&s.prev1LogE, bandCount)
	s.coarseDecisionE = ensureGLogSlice(&s.coarseDecisionE, bandCount)

	// Normalized coefficients: normL/normR alias the contiguous halves of
	// normStereo so spreading_decision() needs no per-frame copy.
	s.normStereo = ensureNormSliceNoClear(&s.normStereo, frameSize*channels)
	s.normL = s.normStereo[:frameSize:frameSize]
	if channels == 2 {
		s.normR = s.normStereo[frameSize : frameSize*2 : frameSize*2]
	} else {
		s.normR = nil
	}

	// Allocation and TF int32 scratch carved from one contiguous arena.
	needI32 := 9 * modeBands
	if s.i32.Cap() < needI32 {
		s.i32.Ensure(needI32)
		s.caps = s.i32.AllocN(modeBands)
		s.offsets = s.i32.AllocN(modeBands)
		s.allocBits = s.i32.AllocN(modeBands)
		s.allocFineBits = s.i32.AllocN(modeBands)
		s.allocFinePrio = s.i32.AllocN(modeBands)
		s.allocCaps = s.i32.AllocN(modeBands)
		s.tfRes = s.i32.AllocN(modeBands)
		s.allocThresh = s.i32.AllocN(modeBands)
		s.allocTrim = s.i32.AllocN(modeBands)
	} else {
		s.caps = s.caps[:modeBands]
		s.offsets = s.offsets[:modeBands]
		s.allocBits = s.allocBits[:modeBands]
		s.allocFineBits = s.allocFineBits[:modeBands]
		s.allocFinePrio = s.allocFinePrio[:modeBands]
		s.allocCaps = s.allocCaps[:modeBands]
		s.tfRes = s.tfRes[:modeBands]
		s.allocThresh = s.allocThresh[:modeBands]
		s.allocTrim = s.allocTrim[:modeBands]
	}
	if len(s.logN) < modeBands {
		if modeBands <= len(s.logNStorage) {
			s.logN = s.logNStorage[:modeBands]
		} else {
			s.logN = make([]int16, modeBands)
		}
	}
	// Initialize AllocationResult with pre-allocated slices
	s.allocResult.BandBits = s.allocBits
	s.allocResult.FineBits = s.allocFineBits
	s.allocResult.FinePriority = s.allocFinePrio
	s.allocResult.Caps = s.allocCaps

	// MDCT forward transform scratch (float32)
	n4 := frameSize / 2 // n4 = frameSize/2 for N=2*frameSize MDCT
	s.mdctF = ensureFloat32Slice(&s.mdctF, frameSize)
	if cap(s.mdctCpx) < 2*n4 {
		s.mdctCpx = make([]complex64, 2*n4)
	}
	s.mdctFFTIn = s.mdctCpx[:n4:n4]
	s.mdctFFTOut = s.mdctCpx[n4 : 2*n4 : 2*n4]
	s.mdctFFTTmp = ensureFloat32Slice(&s.mdctFFTTmp, 2*n4)
	// For short MDCT: max short size is frameSize/8 (for 8 short blocks)
	s.mdctBlockCoeffs = ensureFloat32Slice(&s.mdctBlockCoeffs, frameSize/2)

	// Transient analysis scratch
	samplesPerChannel := frameSize + overlap
	s.transientEnergy = ensureFloat32Slice(&s.transientEnergy, samplesPerChannel/2)
	s.transientEnergyR = ensureFloat32Slice(&s.transientEnergyR, samplesPerChannel/2)
	s.transientX = ensureFloat32Slice(&s.transientX, samplesPerChannel)

	if extsupport.QEXT && e.qextActive() {
		qs := s.ensureQEXTScratch()
		qs.extraBits = ensureInt32Slice(&qs.extraBits, modeBands+nbQEXTBands)
		qs.fineBits = ensureInt32Slice(&qs.fineBits, modeBands+nbQEXTBands)
		qs.bandE = ensureEnerSlice(&qs.bandE, nbQEXTBands*channels)
		qs.bandLogE = ensureGLogSlice(&qs.bandLogE, nbQEXTBands*channels)
		// Coarse-energy trial passes use the full predictor stride even when
		// only a few QEXT bands are coded. Keep their compact output views on
		// this same backing storage through the trial and final passes.
		qs.quantized = ensureGLogSlice(&qs.quantized, modeBands*channels)
		qs.qerr = ensureGLogSlice(&qs.qerr, modeBands*channels)
		// QEXT oldBandE is persistent CELT encoder state. In libopus it
		// occupies the extra state storage after energyError and survives
		// ordinary frame preparation until OPUS_RESET_STATE.
		e.ensureQEXTOldBandE(channels)
		qs.normL = ensureNormSliceNoClear(&qs.normL, frameSize)
		qs.normR = ensureNormSliceNoClear(&qs.normR, frameSize)
	}

	// Band encode scratch. Carve the float-family fields (norm, lowbandScratch,
	// hadamardTmpNorm, pvqY/pvqAbsX/pvqX, theta-RDO slots) from one contiguous
	// arena first; the sizing/getters below reslice within their cap-pinned slots.
	const maxPVQN = maxBandWidth * 2 // Max band width with stereo doubling
	bandScratch := &e.bandEncScratch
	bandScratch.ensureFloatScratch(channels)
	bandScratch.collapse = ensureByteSlice(&bandScratch.collapse, channels*modeBands)
	normLen := 8 * e.modeEdges()[modeBands-1] // M=8 for 20ms frames
	bandScratch.norm = ensureNormSliceNoClear(&bandScratch.norm, channels*normLen)
	maxBand := 8 * (e.modeEdges()[modeBands] - e.modeEdges()[modeBands-1])
	bandScratch.lowbandScratch = ensureNormSliceNoClear(&bandScratch.lowbandScratch, maxBand)
	bandScratch.pvqSignx = ensureByteSlice(&bandScratch.pvqSignx, maxPVQN)
	bandScratch.pvqY = ensureFloat32Slice(&bandScratch.pvqY, maxPVQN)
	bandScratch.pvqAbsX = ensureFloat32Slice(&bandScratch.pvqAbsX, maxPVQN)
	bandScratch.pvqIy = ensureInt32Slice(&bandScratch.pvqIy, maxPVQN)
	if extsupport.QEXT && e.qextActive() {
		bandScratch.pvqUpIy = ensureInt32Slice(&bandScratch.pvqUpIy, maxPVQN)
		bandScratch.pvqRefine = ensureInt32Slice(&bandScratch.pvqRefine, maxPVQN)
		if channels == 2 && e.complexity >= 8 {
			// libopus celt/bands.c allocates bytes_save to 1275 and
			// ext_bytes_save to QEXT_PACKET_SIZE_CAP when theta RDO is active.
			// Reserve both coder snapshots up front because their storage varies
			// by frame and otherwise grows them during a later high-rate encode.
			bandScratch.ecSave.ReserveBufferCapacity(celtPacketSizeCap)
			bandScratch.ecSave0.ReserveBufferCapacity(celtPacketSizeCap)
			bandScratch.extEcSave.ReserveBufferCapacity(qextPacketSizeCap)
			bandScratch.extEcSave0.ReserveBufferCapacity(qextPacketSizeCap)
		}
	}
	bandScratch.qextIy = ensureInt32Slice(&bandScratch.qextIy, maxPVQN)
	bandScratch.cwrsU = ensureUint32Slice(&bandScratch.cwrsU, 256)
	bandScratch.hadamardTmpNorm = ensureNormSliceNoClear(&bandScratch.hadamardTmpNorm, max(maxBand, maxBandWidth))
	e.tfScratch.EnsureTFAnalysisScratch(modeBands, maxBandWidth, 3)
}

// encodeScratchShape is what ensureScratch sizes the per-frame scratch from.
type encodeScratchShape struct {
	frameSize, channels, overlap, combScale int
	perMode                                 *perModeTables
	reserve                                 bool
	// qext frames size extra buffers, so a QEXT toggle never reuses a shape.
	qext bool
}

// resetScratchLengths is ensureScratch for an unchanged encodeScratchShape:
// every buffer already has its capacity, so only the frame-start lengths and
// the zero fills of the clearing ensure helpers are restored. It must mirror
// the sizing statements of ensureScratch; TestEncoderScratchResetMatchesSizing
// checks that it does.
func (e *Encoder) resetScratchLengths(frameSize int) {
	channels := int(e.channels)
	modeBands := e.predStride()
	expectedLen := frameSize * channels
	overlap := min(e.analysisOverlap(), frameSize)
	s := &e.scratch
	maxPeriod := max(e.combMaxPeriod(), e.combMinPeriod())
	maxPitch := max(maxPeriod-3*e.combMinPeriod(), 1)

	s.quantizedInputF32 = s.quantizedInputF32[:expectedLen]
	s.dcRejectedF32 = s.dcRejectedF32[:expectedLen]
	s.combinedBufF32 = s.combinedBufF32[:DelayCompensation*channels+expectedLen]
	s.planarIn = s.planarIn[:(overlap+frameSize)*channels]
	prefilterLen := (maxPeriod + frameSize) * channels
	s.prefilterPre = s.prefilterPre[:prefilterLen]
	s.prefilterOut = s.prefilterOut[:prefilterLen]
	s.prefilterPitchBuf = s.prefilterPitchBuf[:max((maxPeriod+frameSize)>>1, 1)]
	s.prefilterXcorr = s.prefilterXcorr[:maxPitch>>1]
	s.prefilterXLP4 = s.prefilterXLP4[:max(frameSize>>2, 1)]
	s.prefilterYLP4 = s.prefilterYLP4[:max((frameSize+maxPitch)>>2, 1)]
	s.prefilterYYLookup = s.prefilterYYLookup[:max((maxPeriod>>1)+1, 1)]
	s.mdctCoeffsF32 = s.mdctCoeffsF32[:frameSize*channels]

	bandCount := modeBands * channels
	s.energies = clearedSlice(s.energies, bandCount)
	s.bandLogE2 = clearedSlice(s.bandLogE2, bandCount)
	s.bandE = clearedSlice(s.bandE, bandCount)
	s.bandAmp = clearedSlice(s.bandAmp, bandCount)
	s.coarseError = clearedSlice(s.coarseError, bandCount)
	s.bandEL = clearedSlice(s.bandEL, modeBands)
	s.bandER = clearedSlice(s.bandER, modeBands)
	s.quantizedEnergies = clearedSlice(s.quantizedEnergies, bandCount)
	s.prev1LogE = clearedSlice(s.prev1LogE, bandCount)
	s.coarseDecisionE = clearedSlice(s.coarseDecisionE, bandCount)
	s.normStereo = s.normStereo[:frameSize*channels]
	s.normL = s.normStereo[:frameSize:frameSize]
	if channels == 2 {
		s.normR = s.normStereo[frameSize : frameSize*2 : frameSize*2]
	} else {
		s.normR = nil
	}

	s.caps = s.caps[:modeBands]
	s.offsets = s.offsets[:modeBands]
	s.allocBits = s.allocBits[:modeBands]
	s.allocFineBits = s.allocFineBits[:modeBands]
	s.allocFinePrio = s.allocFinePrio[:modeBands]
	s.allocCaps = s.allocCaps[:modeBands]
	s.allocResult.BandBits = s.allocBits
	s.allocResult.FineBits = s.allocFineBits
	s.allocResult.FinePriority = s.allocFinePrio
	s.allocResult.Caps = s.allocCaps
	s.tfRes = s.tfRes[:modeBands]

	n4 := frameSize / 2
	s.mdctF = s.mdctF[:frameSize]
	s.mdctFFTIn = s.mdctFFTIn[:n4]
	s.mdctFFTOut = s.mdctFFTOut[:n4]
	s.mdctFFTTmp = s.mdctFFTTmp[:2*n4]
	s.mdctBlockCoeffs = s.mdctBlockCoeffs[:frameSize/2]
	samplesPerChannel := frameSize + overlap
	s.transientEnergy = s.transientEnergy[:samplesPerChannel/2]
	s.transientEnergyR = s.transientEnergyR[:samplesPerChannel/2]
	s.transientX = s.transientX[:samplesPerChannel]
	s.allocThresh = s.allocThresh[:modeBands]
	s.allocTrim = s.allocTrim[:modeBands]

	const maxPVQN = maxBandWidth * 2
	bandScratch := &e.bandEncScratch
	edges := e.modeEdges()
	bandScratch.collapse = bandScratch.collapse[:channels*modeBands]
	bandScratch.norm = bandScratch.norm[:channels*8*edges[modeBands-1]]
	maxBand := 8 * (edges[modeBands] - edges[modeBands-1])
	bandScratch.lowbandScratch = bandScratch.lowbandScratch[:maxBand]
	bandScratch.pvqSignx = bandScratch.pvqSignx[:maxPVQN]
	bandScratch.pvqY = bandScratch.pvqY[:maxPVQN]
	bandScratch.pvqAbsX = bandScratch.pvqAbsX[:maxPVQN]
	bandScratch.pvqIy = bandScratch.pvqIy[:maxPVQN]
	bandScratch.qextIy = bandScratch.qextIy[:maxPVQN]
	bandScratch.cwrsU = bandScratch.cwrsU[:256]
	bandScratch.hadamardTmpNorm = bandScratch.hadamardTmpNorm[:max(maxBand, maxBandWidth)]
	e.tfScratch.EnsureTFAnalysisScratch(modeBands, maxBandWidth, 3)
}

// clearedSlice returns buf resliced to n and zero filled, the effect of the
// clearing ensure helpers when buf already has the capacity.
func clearedSlice(buf []float32, n int) []float32 {
	buf = buf[:n]
	clear(buf)
	return buf
}

// computeAllocationScratch computes bit allocation using scratch buffers (zero-alloc).
// This is the zero-allocation version of ComputeAllocationWithEncoder.
func (e *Encoder) computeAllocationScratch(re *rangecoding.Encoder, totalBitsQ3, start, nbBands int, cap, offsets []int32, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	maxNb := MaxBands
	if e.perMode != nil {
		maxNb = e.perMode.nbEBands
	}
	if nbBands > maxNb {
		nbBands = maxNb
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := min(max(e.codedChannels(), 1), 2)
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	// Use pre-allocated result from scratch
	result := &e.scratch.allocResult
	result.BandBits = e.scratch.allocBits[:nbBands]
	result.FineBits = e.scratch.allocFineBits[:nbBands]
	result.FinePriority = e.scratch.allocFinePrio[:nbBands]
	result.Caps = e.scratch.allocCaps[:nbBands]
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	// Zero the slices
	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		if e.perMode != nil {
			cap = ensureInt32Slice(&e.scratch.allocCaps, nbBands)[:nbBands]
			initCapsIntoMode(cap, nbBands, lm, channels, e.perMode)
		} else {
			cap = initCaps(nbBands, lm, channels)
		}
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	var codedBands int
	if e.perMode != nil {
		codedBands = cltComputeAllocationWithScratchModeEncode(re, start, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
			totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth, e.allocationScratch(), e.perMode)
	} else {
		codedBands = cltComputeAllocationEncode(re, start, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
			totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)
	}

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}

// LastCodedBands returns the last coded band count used for allocation skip decisions.
func (e *Encoder) LastCodedBands() int {
	return int(e.lastCodedBands)
}

// ConsecTransient returns the number of consecutive transient frames.
func (e *Encoder) ConsecTransient() int {
	return int(e.consecTransient)
}
