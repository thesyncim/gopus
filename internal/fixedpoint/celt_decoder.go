//go:build gopus_fixedpoint

package fixedpoint

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// This file ports the FIXED_POINT celt/celt_decoder.c celt_decode_with_ec
// driver for the static 48000/960 custom mode, orchestrating the already-ported
// integer kernels (energy unquantizers, quant_all_bands, anti_collapse,
// celt_synthesis, comb post-filter, deemphasis) into a full mono/stereo
// non-PLC frame decode that is bit-exact with the reference MODE_DECODE oracle.
//
// Scope of this increment: a fresh (loss_duration==0), non-PLC, non-QEXT decode
// of the static 48000/960 mode. PLC (data==NULL || len<=1), sub-48k downsample
// and DRED are out of scope and not driven here.

const (
	// celtDecodeBufferSize mirrors DECODE_BUFFER_SIZE == DEC_PITCH_BUF_SIZE.
	celtDecodeBufferSize = 2048
	// celtCombFilterMinPeriod mirrors COMBFILTER_MINPERIOD.
	celtCombFilterMinPeriod = 15
	// celtNbEBands / celtOverlap / celtShortMdctSize / celtMaxLM describe the
	// static 48000/960 mode.
	celtNbEBands      = 21
	celtOverlap       = 120
	celtShortMdctSize = 120
	celtMaxLM         = 3
)

// CELTDecoder is the FIXED_POINT integer CELT decoder state for the static
// 48000/960 custom mode. It owns the cross-frame decode_mem overlap buffer, the
// energy histories and the post-filter / deemphasis state, matching the reset
// region of libopus OpusCustomDecoder.
type CELTDecoder struct {
	channels int

	start int
	end   int

	rng          uint32
	lossDuration int

	postfilterPeriod    int
	postfilterPeriodOld int
	postfilterGain      int16
	postfilterGainOld   int16
	postfilterTapset    int
	postfilterTapsetOld int

	// decodeMem holds channels*(celtDecodeBufferSize+overlap) celt_sig samples.
	decodeMem []int32
	// oldBandE/oldLogE/oldLogE2/backgroundLogE are 2*nbEBands celt_glog each.
	oldBandE       []int32
	oldLogE        []int32
	oldLogE2       []int32
	backgroundLogE []int32
	preemphMemD    []int32

	mdct   *MDCTLookup
	window []int16
	eBands []int16
}

// NewCELTDecoder allocates and resets an integer CELT decoder for the static
// 48000/960 mode with the given channel count (1 or 2), matching
// celt_decoder_init: oldLogE/oldLogE2 are seeded to -GCONST(28.f).
func NewCELTDecoder(channels int) *CELTDecoder {
	d := &CELTDecoder{
		channels: channels,
		start:    0,
		end:      celtNbEBands,
		mdct:     NewStaticMDCTLookup48000(),
		window:   staticMDCT48000Window[:],
		eBands:   staticMDCT48000EBands[:],
	}
	d.decodeMem = make([]int32, channels*(celtDecodeBufferSize+celtOverlap))
	d.oldBandE = make([]int32, 2*celtNbEBands)
	d.oldLogE = make([]int32, 2*celtNbEBands)
	d.oldLogE2 = make([]int32, 2*celtNbEBands)
	d.backgroundLogE = make([]int32, 2*celtNbEBands)
	d.preemphMemD = make([]int32, channels)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	return d
}

// SetBandRange sets the active band range (st->start / st->end), matching the
// CELT_SET_START_BAND_REQUEST / CELT_SET_END_BAND_REQUEST controls.
func (d *CELTDecoder) SetBandRange(start, end int) {
	d.start = start
	d.end = end
}

// DecodeWithEC ports celt_decode_with_ec for a fresh non-PLC frame on the static
// 48000/960 mode. data is the CELT packet, frameSize the per-channel sample
// count (shortMdctSize<<LM). It writes channels*frameSize interleaved int16 PCM
// into out and returns the number of per-channel samples decoded.
func (d *CELTDecoder) DecodeWithEC(data []byte, frameSize int, out []int16) int {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	start := d.start
	end := d.end
	CC := d.channels
	C := d.channels

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	decodeMemSize := celtDecodeBufferSize + overlap
	decodeMem := make([][]int32, CC)
	outSyn := make([][]int32, CC)
	for c := 0; c < CC; c++ {
		decodeMem[c] = d.decodeMem[c*decodeMemSize : (c+1)*decodeMemSize]
		outSyn[c] = decodeMem[c][celtDecodeBufferSize-N:]
	}

	effEnd := end
	if effEnd > nbEBands {
		effEnd = nbEBands
	}

	dec := &rangecoding.Decoder{}
	dec.Init(data)

	if C == 1 {
		for i := 0; i < nbEBands; i++ {
			d.oldBandE[i] = max32(d.oldBandE[i], d.oldBandE[nbEBands+i])
		}
	}

	totalBits := len(data) * 8
	tell := dec.Tell()

	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = dec.DecodeBit(15) == 1
	}
	if silence {
		// Pretend we've read all the remaining bits.
		tell = totalBits
		dec.SkipToTell(totalBits)
	}

	var postfilterGain int16
	postfilterPitch := 0
	postfilterTapset := 0
	if start == 0 && tell+16 <= totalBits {
		if dec.DecodeBit(1) == 1 {
			octave := int(dec.DecodeUniform(6))
			postfilterPitch = (16 << octave) + int(dec.DecodeRawBits(uint(4+octave))) - 1
			qg := int(dec.DecodeRawBits(3))
			if dec.Tell()+2 <= totalBits {
				postfilterTapset = dec.DecodeICDF(tapsetICDF, 2)
			}
			// QCONST16(.09375f,15)*(qg+1) = 3072*(qg+1).
			postfilterGain = int16(3072 * (qg + 1))
		}
		tell = dec.Tell()
	}

	isTransient := false
	if LM > 0 && tell+3 <= totalBits {
		isTransient = dec.DecodeBit(3) == 1
		tell = dec.Tell()
	}
	shortBlocks := 0
	if isTransient {
		shortBlocks = M
	}

	intraEner := false
	if tell+3 <= totalBits {
		intraEner = dec.DecodeBit(3) == 1
	}
	// loss_duration==0 here, so the loss-energy-safety block is skipped.

	UnquantCoarseEnergy(dec, d.oldBandE, start, end, nbEBands, C, LM, intraEner)

	alloc := celt.DecodeCELTAllocation(dec, totalBits, start, end, LM, C, isTransient)

	tfRes := make([]int, end)
	for i := 0; i < end; i++ {
		tfRes[i] = int(alloc.TFRes[i])
	}
	pulses := make([]int, nbEBands)
	for i := 0; i < end; i++ {
		pulses[i] = int(alloc.Pulses[i])
	}
	fineQuant := make([]int32, nbEBands)
	finePriority := make([]int32, nbEBands)
	copy(fineQuant, alloc.FineQuant)
	copy(finePriority, alloc.FinePriority)

	UnquantFineEnergy(dec, d.oldBandE, start, end, nbEBands, C, nil, fineQuant)

	// OPUS_MOVE(decode_mem[c], decode_mem[c]+N, decode_buffer_size-N+overlap).
	moveLen := celtDecodeBufferSize - N + overlap
	for c := 0; c < CC; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}

	seed := d.rng
	totalBitsQ3 := len(data)*(8<<bitRes) - alloc.AntiCollapseRsv
	left, right, collapse := QuantAllBandsDecode(dec, C, N, LM, start, end,
		pulses, tfRes, shortBlocks, alloc.Spread, alloc.DualStereo, alloc.Intensity,
		totalBitsQ3, alloc.Balance, alloc.CodedBands, false, &seed)

	// X is interleaved [channel0 N][channel1 N].
	X := make([]int32, C*N)
	copy(X[:N], left)
	if C == 2 {
		copy(X[N:], right)
	}

	antiCollapseOn := false
	if alloc.AntiCollapseRsv > 0 {
		antiCollapseOn = dec.DecodeRawBits(1) == 1
	}

	UnquantEnergyFinalise(dec, d.oldBandE, start, end, nbEBands, C, fineQuant, finePriority, totalBits-dec.Tell())

	if antiCollapseOn {
		AntiCollapse(X, collapse, LM, C, N, start, end,
			d.oldBandE, d.oldLogE, d.oldLogE2, pulses, d.eBands, nbEBands, d.rng, false)
	}

	if silence {
		for i := 0; i < C*nbEBands; i++ {
			d.oldBandE[i] = -gconst(28)
		}
	}

	// prefilter_and_fold is 0 on a fresh decode (not set without PLC).

	CeltSynthesis(d.mdct, d.window, d.eBands,
		nbEBands, shortMdctSize, celtMaxLM, overlap,
		X, outSyn, d.oldBandE,
		start, effEnd, C, CC, LM, 1, isTransient, silence)

	for c := 0; c < CC; c++ {
		pp := d.postfilterPeriod
		if pp < celtCombFilterMinPeriod {
			pp = celtCombFilterMinPeriod
		}
		ppOld := d.postfilterPeriodOld
		if ppOld < celtCombFilterMinPeriod {
			ppOld = celtCombFilterMinPeriod
		}
		d.postfilterPeriod = pp
		d.postfilterPeriodOld = ppOld
		// out_syn[c] aliases decodeMem[c] at celtDecodeBufferSize-N; the comb
		// filter reads history before that offset, so operate on decodeMem[c]
		// with base = celtDecodeBufferSize-N.
		base := celtDecodeBufferSize - N
		CombFilter(decodeMem[c], decodeMem[c], base, ppOld, pp, shortMdctSize,
			d.postfilterGainOld, d.postfilterGain, d.postfilterTapsetOld, d.postfilterTapset,
			d.window, overlap)
		if LM != 0 {
			CombFilter(decodeMem[c], decodeMem[c], base+shortMdctSize, pp, postfilterPitch, N-shortMdctSize,
				d.postfilterGain, postfilterGain, d.postfilterTapset, postfilterTapset,
				d.window, overlap)
		}
	}
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = postfilterPitch
	d.postfilterGain = postfilterGain
	d.postfilterTapset = postfilterTapset
	if LM != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}

	if C == 1 {
		copy(d.oldBandE[nbEBands:2*nbEBands], d.oldBandE[:nbEBands])
	}

	if !isTransient {
		copy(d.oldLogE2, d.oldLogE[:2*nbEBands])
		copy(d.oldLogE, d.oldBandE[:2*nbEBands])
	} else {
		for i := 0; i < 2*nbEBands; i++ {
			d.oldLogE[i] = min32(d.oldLogE[i], d.oldBandE[i])
		}
	}
	// max_background_increase = IMIN(160, loss_duration+M)*GCONST(0.001f).
	mbi := d.lossDuration + M
	if mbi > 160 {
		mbi = 160
	}
	maxBackgroundIncrease := int32(mbi) * gconst001
	for i := 0; i < 2*nbEBands; i++ {
		d.backgroundLogE[i] = min32(d.backgroundLogE[i]+maxBackgroundIncrease, d.oldBandE[i])
	}
	for c := 0; c < 2; c++ {
		for i := 0; i < start; i++ {
			d.oldBandE[c*nbEBands+i] = 0
			d.oldLogE[c*nbEBands+i] = -gconst(28)
			d.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
		for i := end; i < nbEBands; i++ {
			d.oldBandE[c*nbEBands+i] = 0
			d.oldLogE[c*nbEBands+i] = -gconst(28)
			d.oldLogE2[c*nbEBands+i] = -gconst(28)
		}
	}
	d.rng = dec.Range()

	// deemphasis(out_syn, pcm, N, CC, downsample=1, preemph, preemph_memD, accum=0).
	resPCM := make([]int32, CC*N)
	Deemphasis(outSyn, resPCM, staticMDCT48000Preemph0, d.preemphMemD, N, 1, false)
	for i := range resPCM {
		out[i] = Res2Int16(resPCM[i])
	}

	d.lossDuration = 0
	return frameSize
}

// gconst001 is GCONST(0.001f) = round(0.5 + 0.001*(1<<DB_SHIFT)).
const gconst001 = int32(16777)

// tapsetICDF mirrors celt/celt.c tapset_icdf, the inverse CDF for the post-filter
// tapset symbol decoded with ec_dec_icdf(dec, tapset_icdf, 2).
var tapsetICDF = []uint8{2, 1, 0}
