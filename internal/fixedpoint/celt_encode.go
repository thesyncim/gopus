//go:build gopus_fixedpoint

package fixedpoint

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// This file assembles the FIXED_POINT celt_encode_with_ec driver
// (celt/celt_encoder.c) for the static 48000/960 custom mode, orchestrating the
// already-ported integer kernels into a full CBR frame encode that is bit-exact
// with the reference MODE_ENCODE oracle (the produced packet bytes).
//
// Scope of this increment: a fresh (or sequential) CBR encode with signalling
// disabled, the float analysis invalid, surround masking off and LFE off,
// matching a plain celt_encoder_init + OPUS_SET_VBR(0) + CELT_SET_SIGNALLING(0)
// encoder. VBR/CVBR (vbr_rate>0) and QEXT are out of scope.

// spreadICDFEnc / trimICDFEnc mirror celt/celt.c spread_icdf[4] and trim_icdf[11].
var spreadICDFEnc = []uint8{25, 23, 2, 0}
var trimICDFEnc = []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}

// EncodeWithEC ports celt_encode_with_ec for a CBR frame on the static 48000/960
// mode. pcm is channels*frameSize interleaved int16 PCM, frameSize the 48k-core
// per-channel sample count (shortMdctSize<<LM). enc must be initialised against a
// buffer of nbCompressedBytes. It returns the number of packet bytes produced
// (the caller reads enc.Done()). The encoder's cross-frame state is advanced.
func (e *CELTEncoder) EncodeWithEC(pcm []int16, frameSize int, enc *rangecoding.Encoder, nbCompressedBytes int) int {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	eBands := e.eBands
	CC := e.channels
	C := e.channels
	start := e.start
	end := e.end
	hybrid := start != 0

	maxPeriod := combFilterMaxPeriod

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	tell0Frac := enc.TellFrac()
	tell := enc.Tell()
	nbFilledBytes := (tell + 4) >> 3

	if nbCompressedBytes > 1275 {
		nbCompressedBytes = 1275
	}

	// CBR (vbr==0). Recompute nbCompressedBytes from the bitrate.
	tmp := e.bitrate * frameSize
	if tell > 1 {
		tmp += tell * 48000
	}
	if e.bitrate != opusBitrateMax {
		v := (tmp + 4*48000) / (8 * 48000)
		if v < nbCompressedBytes {
			nbCompressedBytes = v
		}
		if nbCompressedBytes < 2 {
			nbCompressedBytes = 2
		}
		enc.Shrink(uint32(nbCompressedBytes))
	}
	effectiveBytes := nbCompressedBytes - nbFilledBytes
	nbAvailableBytes := nbCompressedBytes - nbFilledBytes

	equivRate := nbCompressedBytes*8*50<<(3-LM) - (40*C+20)*((400>>LM)-50)
	if e.bitrate != opusBitrateMax {
		if v := e.bitrate - (40*C+20)*((400>>LM)-50); v < equivRate {
			equivRate = v
		}
	}

	totalBits := nbCompressedBytes * 8

	effEnd := end
	if effEnd > nbEBands {
		effEnd = nbEBands
	}

	// in buffer (CC*(N+overlap)): overlap prefix from prefilter_mem, body from
	// pre-emphasised input.
	in := make([]int32, CC*(N+overlap))

	// sample_max / silence over the res-domain input.
	sampleMax := e.overlapMax
	if v := maxabsRes(pcm, C*(N-overlap)); v > sampleMax {
		sampleMax = v
	}
	e.overlapMax = maxabsRes(pcm[C*(N-overlap):], C*overlap)
	if e.overlapMax > sampleMax {
		sampleMax = e.overlapMax
	}
	silence := sampleMax == 0

	if tell == 1 {
		enc.EncodeBit(boolToInt(silence), 15)
	} else {
		silence = false
	}
	if silence {
		tell = nbCompressedBytes * 8
		enc.SkipToTell(tell)
	}

	for c := 0; c < CC; c++ {
		e.preemphasis(pcm[c:], in[c*(N+overlap)+overlap:], N, CC, c)
		// in[c*(N+overlap) .. +overlap] = prefilter_mem[(1+c)*maxPeriod-overlap ..]
		copy(in[c*(N+overlap):c*(N+overlap)+overlap],
			e.prefilterMem[(1+c)*maxPeriod-overlap:(1+c)*maxPeriod])
	}

	toneFreq, toneishness := ToneDetect(in, CC, N+overlap, 48000)

	isTransient := false
	tfEstimate := int16(0)
	tfChan := 0
	if e.complexity >= 1 {
		ta := TransientAnalysis(in, N+overlap, CC, false, toneFreq, toneishness)
		isTransient = ta.IsTransient
		tfEstimate = ta.TFEstimate
		tfChan = ta.TFChan
	}
	// toneishness = MIN32(toneishness, QCONST32(1,29) - SHL32(tf_estimate,15))
	if v := gconstQ(1, 29) - shl32(int32(tfEstimate), 15); v < toneishness {
		toneishness = v
	}

	// run_prefilter: pitch/gain decision + comb-filter the time-domain in[].
	enabled := nbAvailableBytes > 12*C && !hybrid && !silence && tell+16 <= totalBits
	pfRes := e.runPrefilter(in, CC, N, overlap, enabled,
		toneFreq, toneishness, tfEstimate, nbAvailableBytes)
	pfOn := pfRes.PFOn
	pitchIndex := pfRes.PitchIndex
	gain1 := pfRes.Gain
	prefilterTapset := pfRes.Tapset
	EmitPrefilterParams(enc, pfRes, hybrid, tell, totalBits)

	shortBlocks := 0
	if LM > 0 && enc.Tell()+3 <= totalBits {
		if isTransient {
			shortBlocks = M
		}
	} else {
		isTransient = false
	}
	transientGotDisabled := false
	if !(LM > 0 && enc.Tell()+3 <= totalBits) {
		transientGotDisabled = true
	}

	freq := make([]int32, CC*N)
	bandE := make([]int32, nbEBands*CC)
	bandLogE := make([]int32, nbEBands*CC)

	secondMdct := shortBlocks != 0 && e.complexity >= 8
	bandLogE2 := make([]int32, C*nbEBands)
	if secondMdct {
		e.computeMDCTs(0, in, freq, C, CC, LM)
		ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
		Amp2Log2(bandE, bandLogE2, nbEBands, effEnd, end, C)
		for c := 0; c < C; c++ {
			for i := 0; i < end; i++ {
				bandLogE2[nbEBands*c+i] += half32(shl32(int32(LM), dbShift))
			}
		}
	}

	e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)
	if CC == 2 && C == 1 {
		tfChan = 0
	}
	ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
	Amp2Log2(bandE, bandLogE, nbEBands, effEnd, end, C)

	// surround_dynalloc all zero (no energy_mask), temporal_vbr maintained.
	surroundDynalloc := make([]int32, C*nbEBands)
	e.temporalVBR(bandLogE, start, end, nbEBands, C, shortBlocks, LM)

	if !secondMdct {
		copy(bandLogE2, bandLogE[:C*nbEBands])
	}

	// Last-chance transient detection.
	if LM > 0 && enc.Tell()+3 <= totalBits && !isTransient && e.complexity >= 5 && !hybrid {
		if PatchTransientDecision(bandLogE, e.oldBandE, nbEBands, start, end, C) {
			isTransient = true
			shortBlocks = M
			e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)
			ComputeBandEnergies(freq, eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)
			Amp2Log2(bandE, bandLogE, nbEBands, effEnd, end, C)
			for c := 0; c < C; c++ {
				for i := 0; i < end; i++ {
					bandLogE2[nbEBands*c+i] += half32(shl32(int32(LM), dbShift))
				}
			}
			tfEstimate = 3277 // QCONST16(.2f,14)
		}
	}

	if LM > 0 && enc.Tell()+3 <= totalBits {
		enc.EncodeBit(boolToInt(isTransient), 3)
	}

	X := make([]int32, C*N)
	NormaliseBands(freq, X, bandE, eBands, nbEBands, shortMdctSize, effEnd, C, M)

	enableTFAnalysis := effectiveBytes >= 15*C && !hybrid && e.complexity >= 2 && toneishness < gconstQ(0.98, 29)

	offsets := make([]int, nbEBands)
	importance := make([]int, nbEBands)
	spreadWeight := make([]int, nbEBands)

	var totBoost int
	maxDepth := DynallocAnalysis(bandLogE, bandLogE2, e.oldBandE, nbEBands, start, end, C,
		offsets, e.lsbDepth, e.logN, isTransient, false, true,
		eBands, LM, effectiveBytes, false, surroundDynalloc,
		importance, spreadWeight, toneFreq, toneishness, &totBoost)
	_ = maxDepth

	tfRes := make([]int, nbEBands)
	tfSelect := 0
	if enableTFAnalysis {
		lambda := imax(80, 20480/effectiveBytes+2)
		tfSelect = TFAnalysis(eBands, effEnd, isTransient, tfRes, lambda, X, N, LM, tfEstimate, tfChan, importance)
		for i := effEnd; i < end; i++ {
			tfRes[i] = tfRes[effEnd-1]
		}
	} else {
		for i := 0; i < end; i++ {
			tfRes[i] = boolToInt(isTransient)
		}
		tfSelect = 0
	}

	// Energy-error bias before coarse energy.
	error := make([]int32, C*nbEBands)
	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			if abs32(bandLogE[i+c*nbEBands]-e.oldBandE[i+c*nbEBands]) < gconst(2) {
				bandLogE[i+c*nbEBands] -= mult16x32Q15(8192, e.energyError[i+c*nbEBands]) // QCONST16(0.25,15)=8192
			}
		}
	}
	QuantCoarseEnergy(enc, bandLogE, e.oldBandE, error, start, end, effEnd, nbEBands, C, LM,
		totalBits, nbAvailableBytes, false, e.complexity >= 4, 0, false, &e.delayedIntra)

	TFEncode(start, end, isTransient, tfRes, LM, tfSelect, enc)

	// Spread decision.
	if enc.Tell()+4 <= totalBits {
		switch {
		case hybrid:
			if e.complexity == 0 {
				e.spreadDecision = spreadNone
			} else if isTransient {
				e.spreadDecision = spreadNormal
			} else {
				e.spreadDecision = spreadAggressive
			}
		case shortBlocks != 0 || e.complexity < 3 || nbAvailableBytes < 10*C:
			if e.complexity == 0 {
				e.spreadDecision = spreadNone
			} else {
				e.spreadDecision = spreadNormal
			}
		default:
			e.spreadDecision = SpreadingDecision(X, eBands, nbEBands, e.spreadDecision,
				&e.spreading, boolToInt(pfOn && shortBlocks == 0), effEnd, C, M, spreadWeight)
		}
		enc.EncodeICDF(e.spreadDecision, spreadICDFEnc, 5)
	} else {
		e.spreadDecision = spreadNormal
	}

	cap := celt.InitCaps(nbEBands, LM, C)

	// Dynalloc boost coding.
	dynallocLogp := 6
	totalBitsQ3 := totalBits << bitRes
	totalBoost := 0
	tellFrac := enc.TellFrac()
	for i := start; i < end; i++ {
		width := C * (int(eBands[i+1]) - int(eBands[i])) << LM
		quanta := imin(width<<bitRes, imax(6<<bitRes, width))
		dynallocLoopLogp := dynallocLogp
		boost := 0
		j := 0
		for tellFrac+(dynallocLoopLogp<<bitRes) < totalBitsQ3-totalBoost && boost < int(cap[i]) {
			flag := j < offsets[i]
			enc.EncodeBit(boolToInt(flag), uint(dynallocLoopLogp))
			tellFrac = enc.TellFrac()
			if !flag {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
			j++
		}
		if j != 0 {
			dynallocLogp = imax(2, dynallocLogp-1)
		}
		offsets[i] = boost
	}

	dualStereo := 0
	if C == 2 {
		intensityThresholds := []int{1, 2, 3, 4, 5, 6, 7, 8, 16, 24, 36, 44, 50, 56, 62, 67, 72, 79, 88, 106, 134}
		intensityHisteresis := []int{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 3, 3, 4, 5, 6, 8, 8}
		if LM != 0 {
			dualStereo = boolToInt(StereoAnalysis(eBands, X, LM, N, nbEBands))
		}
		e.intensity = hysteresisDecision(equivRate/1000, intensityThresholds, intensityHisteresis, 21, e.intensity)
		e.intensity = imin(end, imax(start, e.intensity))
	}

	allocTrim := 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3-totalBoost {
		if start > 0 {
			e.stereoSaving = 0
			allocTrim = 5
		} else {
			res := AllocTrimAnalysis(eBands, X, bandLogE, end, LM, C, N, nbEBands,
				e.stereoSaving, tfEstimate, e.intensity, 0, int32(equivRate), false, 0)
			allocTrim = res.TrimIndex
			e.stereoSaving = res.StereoSaving
		}
		enc.EncodeICDF(allocTrim, trimICDFEnc, 7)
		tellFrac = enc.TellFrac()
	}

	// Bit allocation.
	bits := (int32(nbCompressedBytes)*8)<<bitRes - int32(enc.TellFrac()) - 1
	antiCollapseRsv := 0
	if isTransient && LM >= 2 && bits >= int32((LM+2)<<bitRes) {
		antiCollapseRsv = 1 << bitRes
	}
	bits -= int32(antiCollapseRsv)
	signalBandwidth := end - 1

	offsets32 := make([]int32, nbEBands)
	for i := range offsets32 {
		offsets32[i] = int32(offsets[i])
	}
	alloc := celt.ComputeAllocationWithEncoder(enc, int(bits), nbEBands, C, cap, offsets32,
		allocTrim, e.intensity, dualStereo != 0, LM, e.lastCodedBands, signalBandwidth)
	codedBands := alloc.CodedBands
	e.intensity = alloc.Intensity
	dualStereo = boolToInt(alloc.DualStereo)
	if e.lastCodedBands != 0 {
		e.lastCodedBands = imin(e.lastCodedBands+1, imax(e.lastCodedBands-1, codedBands))
	} else {
		e.lastCodedBands = codedBands
	}

	fineQuant := make([]int32, nbEBands)
	finePriority := make([]int32, nbEBands)
	copy(fineQuant, alloc.FineBits)
	copy(finePriority, alloc.FinePriority)
	pulses := make([]int, nbEBands)
	for i := 0; i < nbEBands; i++ {
		pulses[i] = int(alloc.BandBits[i])
	}

	QuantFineEnergy(enc, e.oldBandE, error, start, end, nbEBands, C, nil, fineQuant)
	for i := 0; i < nbEBands*CC; i++ {
		e.energyError[i] = 0
	}

	// Residual quantisation.
	var y []int32
	if C == 2 {
		y = X[N:]
	}
	seed := e.rng
	collapse := QuantAllBandsEncode(enc, C, N, LM, start, end, X, y, bandE,
		pulses, tfRes, shortBlocks, e.spreadDecision, dualStereo, e.intensity,
		nbCompressedBytes*(8<<bitRes)-antiCollapseRsv, alloc.Balance, codedBands,
		e.complexity, false, &seed)
	e.rng = seed
	_ = collapse

	antiCollapseOn := false
	if antiCollapseRsv > 0 {
		antiCollapseOn = e.consecTransient < 2
		enc.EncodeRawBits(uint32(boolToInt(antiCollapseOn)), 1)
	}

	QuantEnergyFinalise(enc, e.oldBandE, error, start, end, nbEBands, C, fineQuant, finePriority, nbCompressedBytes*8-enc.Tell())

	for c := 0; c < C; c++ {
		for i := start; i < end; i++ {
			ee := error[i+c*nbEBands]
			ee = max32(-gconstF(0.5), min32(gconstF(0.5), ee))
			e.energyError[i+c*nbEBands] = ee
		}
	}

	if silence {
		for i := 0; i < C*nbEBands; i++ {
			e.oldBandE[i] = -gconst(28)
		}
	}

	// Cross-frame state updates.
	e.prefilterPeriod = pitchIndex
	e.prefilterGain = gain1
	e.prefilterTapset = prefilterTapset
	_ = tell0Frac

	if CC == 2 && C == 1 {
		copy(e.oldBandE[nbEBands:2*nbEBands], e.oldBandE[:nbEBands])
	}
	if !isTransient {
		copy(e.oldLogE2[:CC*nbEBands], e.oldLogE[:CC*nbEBands])
		copy(e.oldLogE[:CC*nbEBands], e.oldBandE[:CC*nbEBands])
	} else {
		for i := 0; i < CC*nbEBands; i++ {
			e.oldLogE[i] = min32(e.oldLogE[i], e.oldBandE[i])
		}
	}
	for c := 0; c < CC; c++ {
		for i := 0; i < start; i++ {
			e.oldBandE[c*nbEBands+i] = 0
			e.oldLogE[c*nbEBands+i] = -gconst(28)
			e.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
		for i := end; i < nbEBands; i++ {
			e.oldBandE[c*nbEBands+i] = 0
			e.oldLogE[c*nbEBands+i] = -gconst(28)
			e.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
	}
	if isTransient || transientGotDisabled {
		e.consecTransient++
	} else {
		e.consecTransient = 0
	}
	e.rng = enc.Range()

	enc.Done()
	return nbCompressedBytes
}

// runPrefilter ports celt/celt_encoder.c run_prefilter for the FIXED_POINT,
// non-QEXT build: it assembles the per-channel pre[] history buffers, runs the
// pitch/gain analysis (PrefilterAnalysis), comb-filters the time-domain in[] in
// place (matching the encoder's pre-MDCT prefiltering, including the cancel-pitch
// fallback), and updates in_mem / prefilter_mem. It returns the PrefilterResult.
func (e *CELTEncoder) runPrefilter(in []int32, CC, N, overlap int, enabled bool,
	toneFreq int16, toneishness int32, tfEstimate int16, nbAvailableBytes int) PrefilterResult {

	maxPeriod := combFilterMaxPeriod
	offset := celtShortMdctSize - overlap

	pre := make([][]int32, CC)
	for c := 0; c < CC; c++ {
		pre[c] = make([]int32, N+maxPeriod)
		copy(pre[c][:maxPeriod], e.prefilterMem[c*maxPeriod:(c+1)*maxPeriod])
		copy(pre[c][maxPeriod:], in[c*(N+overlap)+overlap:c*(N+overlap)+overlap+N])
	}

	res := PrefilterAnalysis(pre, CC, N, PrefilterParams{
		Enabled:                 enabled,
		Complexity:              e.complexity,
		Toneishness:             toneishness,
		ToneFreq:                toneFreq,
		TFEstimate:              tfEstimate,
		NbAvailableBytes:        nbAvailableBytes,
		PrefilterPeriod:         e.prefilterPeriod,
		PrefilterGain:           e.prefilterGain,
		PrefilterTapset:         e.prefilterTapset,
		PrefilterTapsetDecision: e.spreading.TapsetDecision,
		LossRate:                0,
		AnalysisValid:           false,
		MaxPitchRatio:           0,
	})

	prefilterPeriod := imax(e.prefilterPeriod, combFilterMinPeriod)
	pitchIndex := res.PitchIndex
	gain1 := res.Gain

	before := make([]int32, CC)
	after := make([]int32, CC)
	for c := 0; c < CC; c++ {
		base := c * (N + overlap)
		copy(in[base:base+overlap], e.inMem[c*overlap:(c+1)*overlap])
		for i := 0; i < N; i++ {
			before[c] += abs32(shr32(in[base+overlap+i], 12))
		}
		if offset != 0 {
			combFilterPF(in, base+overlap, pre[c], maxPeriod,
				prefilterPeriod, prefilterPeriod, offset, -e.prefilterGain, -e.prefilterGain,
				e.prefilterTapset, e.prefilterTapset, nil, 0)
		}
		combFilterPF(in, base+overlap+offset, pre[c], maxPeriod+offset,
			prefilterPeriod, pitchIndex, N-offset, -e.prefilterGain, -gain1,
			e.prefilterTapset, res.Tapset, e.window, overlap)
		for i := 0; i < N; i++ {
			after[c] += abs32(shr32(in[base+overlap+i], 12))
		}
	}

	cancelPitch := false
	if CC == 2 {
		thresh0 := mult16x32Q15(mult16x16q15(8192, gain1), before[0]) + mult16x32Q15(328, before[1])
		thresh1 := mult16x32Q15(mult16x16q15(8192, gain1), before[1]) + mult16x32Q15(328, before[0])
		if after[0]-before[0] > thresh0 || after[1]-before[1] > thresh1 {
			cancelPitch = true
		}
		if before[0]-after[0] < thresh0 && before[1]-after[1] < thresh1 {
			cancelPitch = true
		}
	} else {
		if after[0] > before[0] {
			cancelPitch = true
		}
	}
	if cancelPitch {
		for c := 0; c < CC; c++ {
			base := c * (N + overlap)
			copy(in[base+overlap:base+overlap+N], pre[c][maxPeriod:maxPeriod+N])
			combFilterPF(in, base+overlap+offset, pre[c], maxPeriod+offset,
				prefilterPeriod, pitchIndex, overlap, -e.prefilterGain, 0,
				e.prefilterTapset, res.Tapset, e.window, overlap)
		}
		gain1 = 0
		res.PFOn = false
		res.QG = 0
		res.Gain = 0
	}

	for c := 0; c < CC; c++ {
		base := c * (N + overlap)
		copy(e.inMem[c*overlap:(c+1)*overlap], in[base+N:base+N+overlap])
		if N > maxPeriod {
			copy(e.prefilterMem[c*maxPeriod:(c+1)*maxPeriod], pre[c][N:N+maxPeriod])
		} else {
			copy(e.prefilterMem[c*maxPeriod:c*maxPeriod+maxPeriod-N], e.prefilterMem[c*maxPeriod+N:c*maxPeriod+maxPeriod])
			copy(e.prefilterMem[c*maxPeriod+maxPeriod-N:(c+1)*maxPeriod], pre[c][maxPeriod:maxPeriod+N])
		}
	}
	res.PitchIndex = pitchIndex
	return res
}

// temporalVBR ports the temporal-VBR spec_avg update from celt_encode_with_ec
// (the !lfe branch). It maintains st->spec_avg used by compute_vbr; for CBR the
// computed temporal_vbr is discarded but spec_avg must still advance.
func (e *CELTEncoder) temporalVBR(bandLogE []int32, start, end, nbEBands, C, shortBlocks, LM int) {
	follow := -gconstQ(10, dbShift-5)
	frameAvg := int32(0)
	var offset int32
	if shortBlocks != 0 {
		offset = half32(shl32(int32(LM), dbShift-5))
	}
	for i := start; i < end; i++ {
		follow = max32(follow-gconstQ(1, dbShift-5), shr32(bandLogE[i], 5)-offset)
		if C == 2 {
			follow = max32(follow, shr32(bandLogE[i+nbEBands], 5)-offset)
		}
		frameAvg += follow
	}
	frameAvg /= int32(end - start)
	temporalVBR := shl32(frameAvg, 5) - e.specAvg
	temporalVBR = min32(gconstF(3), max32(-gconstF(1.5), temporalVBR))
	e.specAvg += mult16x32Q15(655, temporalVBR) // QCONST16(.02f,15)=655
}

// maxabsRes ports celt_maxabs_res for ENABLE_RES24 int16 input: the maximum
// absolute res-domain value over the first n interleaved samples (res = s<<8).
func maxabsRes(pcm []int16, n int) int32 {
	var maxval, minval int32
	for i := 0; i < n; i++ {
		v := int32(pcm[i]) << resShift
		if v > maxval {
			maxval = v
		}
		if v < minval {
			minval = v
		}
	}
	if -minval > maxval {
		return -minval
	}
	return maxval
}

// hysteresisDecision ports celt/bands.c hysteresis_decision over integer
// thresholds (the equiv_rate/1000 intensity decision uses opus_val16 == int but
// the table values fit; the comparisons are exact).
func hysteresisDecision(val int, thresholds, hysteresis []int, n, prev int) int {
	i := 0
	for ; i < n; i++ {
		if val < thresholds[i] {
			break
		}
	}
	if i > prev && val < thresholds[prev]+hysteresis[prev] {
		i = prev
	}
	if i < prev && val > thresholds[prev-1]-hysteresis[prev-1] {
		i = prev
	}
	return i
}
