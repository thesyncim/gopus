//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	qextCELTDecodeBufferSize48 = 2048
	qextCELTDecodeBufferSize96 = 4096
	qextCELTMaxQEXTBands       = 14
)

// QEXTCELTDecoder is the fixed-point ENABLE_QEXT CELT decoder state for the
// native 48 kHz and 96 kHz modes. Its Q31 synthesis and QEXT energy history are
// separate from CELTDecoder because those types follow the non-QEXT Q15 mode.
type QEXTCELTDecoder struct {
	channels            int
	sampleRate          int
	shortMDCTSize       int
	overlap             int
	decodeBufSize       int
	start               int
	end                 int
	maxFrameSize        int
	qextMaxBands        int
	mdct                *QEXTMDCTLookup
	window              []int32
	eBands              []int16
	qextEdges           []int16
	qextLogN            []int16
	decodeMem           []int32
	oldBandE            []int32
	oldLogE             []int32
	oldLogE2            []int32
	backgroundLogE      []int32
	qextOldBandE        []int32
	preemphMem          []int32
	mdctScratch         QEXTMDCTScratch
	freq                []int32
	baseBands           celtDecodeBandsScratch
	qextBands           celtDecodeBandsScratch
	extraPulses         [celt.MaxBands + qextCELTMaxQEXTBands]int32
	extraQuant          [celt.MaxBands + qextCELTMaxQEXTBands]int32
	tfZero              [qextCELTMaxQEXTBands]int32
	postfilterPeriod    int32
	postfilterPeriodOld int32
	postfilterGain      int16
	postfilterGainOld   int16
	postfilterTapset    int32
	postfilterTapsetOld int32
	disableInv          bool
	rng                 uint32
	lastRes             []int32
	decodeRows          [2][]int32
	synthesisRows       [2][]int32
	extDec              rangecoding.Decoder
	dummyDec            rangecoding.Decoder
}

// NewQEXTCELTDecoder allocates a fixed-point QEXT decoder for a native Opus
// 48 kHz or 96 kHz mode and one or two output channels.
func NewQEXTCELTDecoder(channels, sampleRate int) (*QEXTCELTDecoder, error) {
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("QEXT CELT channels must be 1 or 2, got %d", channels)
	}
	var shortMDCTSize, overlap, decodeBufSize int
	var mdct *QEXTMDCTLookup
	var window []int32
	switch sampleRate {
	case 48000:
		shortMDCTSize, overlap, decodeBufSize = 120, 120, qextCELTDecodeBufferSize48
		mdct, window = NewStaticQEXTMDCTLookup48000(), staticQEXTMDCT48000Window[:]
	case 96000:
		shortMDCTSize, overlap, decodeBufSize = 240, 240, qextCELTDecodeBufferSize96
		mdct, window = NewStaticQEXTMDCTLookup96000(), staticQEXTMDCT96000Window[:]
	default:
		return nil, fmt.Errorf("unsupported native QEXT CELT sample rate %d", sampleRate)
	}
	_, qextEdges, qextLogN, qextMaxBands, ok := fixedQEXTBandMode(sampleRate, shortMDCTSize)
	if !ok {
		return nil, fmt.Errorf("unsupported native QEXT CELT mode %d/%d", sampleRate, shortMDCTSize)
	}
	d := &QEXTCELTDecoder{
		channels:      channels,
		sampleRate:    sampleRate,
		shortMDCTSize: shortMDCTSize,
		overlap:       overlap,
		decodeBufSize: decodeBufSize,
		start:         0,
		end:           celt.MaxBands,
		maxFrameSize:  shortMDCTSize << celtMaxLM,
		qextMaxBands:  qextMaxBands,
		disableInv:    channels == 1,
		mdct:          mdct,
		window:        window,
		eBands:        staticMDCT48000EBands[:],
		qextEdges:     qextEdges,
		qextLogN:      qextLogN,
	}
	memSize := decodeBufSize + overlap
	d.decodeMem = make([]int32, channels*memSize)
	d.oldBandE = make([]int32, 2*celt.MaxBands)
	d.oldLogE = make([]int32, 2*celt.MaxBands)
	d.oldLogE2 = make([]int32, 2*celt.MaxBands)
	d.backgroundLogE = make([]int32, 2*celt.MaxBands)
	d.qextOldBandE = make([]int32, 2*qextCELTMaxQEXTBands)
	d.preemphMem = make([]int32, channels)
	d.freq = make([]int32, d.maxFrameSize)
	d.mdctScratch.fft = make([]FFTCpx, mdct.n/4)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	// Both band workspaces are reusable across the standard and QEXT geometries.
	for _, bands := range []*celtDecodeBandsScratch{&d.baseBands, &d.qextBands} {
		// CELT may code stereo and downmix into a mono output decoder. Keep both
		// coded channels ready from construction so the first such frame does not
		// grow the reusable band scratch.
		bands.x = make([]int32, 2*d.maxFrameSize)
		bands.norm = make([]int32, 2*(d.maxFrameSize+d.maxFrameSize/2))
		bands.lowband = make([]int32, celtMaxBandWidth)
	}
	d.dummyDec.Init(nil)
	return d, nil
}

// SetBandRange selects the main-mode start/end band range used by the next
// received CELT frame.
func (d *QEXTCELTDecoder) SetBandRange(start, end int) {
	d.start = start
	d.end = end
}

// SetPhaseInversionDisabled controls the stereo phase inversion in CELT band
// decoding. Mono decoders start with phase inversion disabled, matching
// celt_decoder.c; Reset preserves this control.
func (d *QEXTCELTDecoder) SetPhaseInversionDisabled(disabled bool) {
	d.disableInv = disabled
}

// LastRes returns the raw int24 opus_res output from the last successful frame.
// It aliases the caller's output buffer and remains valid until the next call.
func (d *QEXTCELTDecoder) LastRes() []int32 { return d.lastRes }

// FinalRange returns the main range XOR side range when a QEXT payload is
// present, matching celt_decode_with_ec_dred's final range state.
func (d *QEXTCELTDecoder) FinalRange() uint32 { return d.rng }

// Reset clears decoder history while retaining allocated scratch and mode.
func (d *QEXTCELTDecoder) Reset() {
	clear(d.decodeMem)
	clear(d.oldBandE)
	clear(d.backgroundLogE)
	clear(d.qextOldBandE)
	clear(d.preemphMem)
	for i := range d.oldLogE {
		d.oldLogE[i] = -gconst(28)
		d.oldLogE2[i] = -gconst(28)
	}
	d.postfilterPeriod = 0
	d.postfilterPeriodOld = 0
	d.postfilterGain = 0
	d.postfilterGainOld = 0
	d.postfilterTapset = 0
	d.postfilterTapsetOld = 0
	d.rng = 0
	d.lastRes = nil
}

// DecodeFrameWithEC decodes one received CELT frame from a main range coder
// that the caller has already initialized and positioned after outer packet
// parsing. dataLen is the effective main CELT payload size used for tell
// validation. qextPayload contains only the side range-coded bytes, without its
// packet extension identifier. out is caller-owned interleaved raw int24
// opus_res storage. The return value is the per-channel sample count, or a
// negative Opus status (-1 bad argument, -2 short output, -3 coder overrun).
func (d *QEXTCELTDecoder) DecodeFrameWithEC(main *rangecoding.Decoder, dataLen, frameSize, codedChannels int, qextPayload []byte, out []int32) int {
	if main == nil || dataLen <= 1 || codedChannels < 1 || codedChannels > 2 ||
		d.start < 0 || d.start >= d.end || d.end > celt.MaxBands {
		return -1
	}
	lm := -1
	for candidate := 0; candidate <= celtMaxLM; candidate++ {
		if d.shortMDCTSize<<candidate == frameSize {
			lm = candidate
			break
		}
	}
	if lm < 0 || len(out) < d.channels*frameSize {
		return -2
	}

	cc := d.channels
	N := frameSize
	M := 1 << lm
	memSize := d.decodeBufSize + d.overlap
	decodeMem := d.decodeRows[:cc]
	outSyn := d.synthesisRows[:cc]
	base := d.decodeBufSize - N
	for c := 0; c < cc; c++ {
		decodeMem[c] = d.decodeMem[c*memSize : (c+1)*memSize]
		outSyn[c] = decodeMem[c][base:]
	}

	totalBits := dataLen * 8
	tell := main.Tell()
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = main.DecodeBit(15) == 1
	}
	if silence {
		tell = totalBits
		main.SkipToTell(totalBits)
	}

	postfilterPitch := 0
	postfilterTapset := 0
	var postfilterGain int16
	if d.start == 0 && tell+16 <= totalBits {
		if main.DecodeBit(1) == 1 {
			octave := int(main.DecodeUniform(6))
			postfilterPitch = (16 << octave) + int(main.DecodeRawBits(uint(4+octave))) - 1
			qg := int(main.DecodeRawBits(3))
			if main.Tell()+2 <= totalBits {
				postfilterTapset = main.DecodeICDF(tapsetICDF, 2)
			}
			postfilterGain = int16(3072 * (qg + 1))
		}
		tell = main.Tell()
	}
	transient := false
	if lm > 0 && tell+3 <= totalBits {
		transient = main.DecodeBit(3) == 1
		tell = main.Tell()
	}
	shortBlocks := 0
	if transient {
		shortBlocks = M
	}
	intraEnergy := false
	if tell+3 <= totalBits {
		intraEnergy = main.DecodeBit(3) == 1
	}

	UnquantCoarseEnergy(main, d.oldBandE, d.start, d.end, celt.MaxBands, codedChannels, lm, intraEnergy)
	alloc := celt.DecodeCELTAllocation(main, totalBits, d.start, d.end, lm, codedChannels, transient)
	fineQuant := alloc.FineQuant[:celt.MaxBands]
	finePriority := alloc.FinePriority[:celt.MaxBands]
	UnquantFineEnergy(main, d.oldBandE, d.start, d.end, celt.MaxBands, codedChannels, nil, fineQuant)

	// The side coder exists in every QEXT build, including when runtime QEXT is
	// absent. Its presence selects Q31 angle gains in the QEXT band kernels.
	d.extDec.Init(qextPayload)
	qextTotalBits := len(qextPayload) * (8 << bitRes)
	qextEnd := 0
	qextIntensity := 0
	qextDualStereo := 0
	qextActive := len(qextPayload) != 0 && d.end == celt.MaxBands
	if qextActive {
		header := celt.QEXTDecodeHeaderExport(&d.extDec, codedChannels, len(qextPayload))
		qextEnd = imin(header.EndBands, d.qextMaxBands)
		qextIntensity = imin(header.Intensity, qextEnd)
		if codedChannels == 2 && header.DualStereo && qextIntensity != 0 {
			qextDualStereo = 1
		}
		qextIntraEnergy := false
		if d.extDec.Tell()+3 <= d.extDec.StorageBits() {
			qextIntraEnergy = d.extDec.DecodeBit(3) == 1
		}
		UnquantCoarseEnergy(&d.extDec, d.qextOldBandE, 0, qextEnd, qextCELTMaxQEXTBands, codedChannels, lm, qextIntraEnergy)
	}
	qextBitsQ3 := qextTotalBits - main.TellFrac() - 1
	if qextBitsQ3 < 0 {
		qextBitsQ3 = 0
	}
	if !celt.QEXTDecodeExtraAllocationExport(d.start, d.end, qextEnd, qextBitsQ3,
		codedChannels, lm, &d.extDec, d.extraPulses[:], d.extraQuant[:], d.sampleRate, d.shortMDCTSize) {
		return -1
	}
	if len(qextPayload) != 0 {
		UnquantFineEnergy(&d.extDec, d.oldBandE, d.start, d.end, celt.MaxBands,
			codedChannels, fineQuant, d.extraQuant[:])
	}

	moveLen := d.decodeBufSize - N + d.overlap
	for c := 0; c < cc; c++ {
		copy(decodeMem[c][:moveLen], decodeMem[c][N:N+moveLen])
	}

	seed := d.rng
	totalBitsQ3 := dataLen*(8<<bitRes) - alloc.AntiCollapseRsv
	_, _, collapse := QuantAllBandsDecodeQEXT(main, codedChannels, N, lm, d.start, d.end,
		alloc.Pulses[:], alloc.TFRes[:], shortBlocks, alloc.Spread, alloc.DualStereo, alloc.Intensity,
		totalBitsQ3, alloc.Balance, alloc.CodedBands, d.disableInv, &seed,
		QEXTBandDecodeState{Decoder: &d.extDec, ExtraPulses: d.extraPulses[:], TotalBitsQ3: qextTotalBits, Caps: alloc.Caps[:]}, &d.baseBands)
	X := d.baseBands.x[:codedChannels*N]

	if qextEnd > 0 {
		extBalance := qextTotalBits - d.extDec.TellFrac()
		fineQ3 := 0
		if qextEnd > 1 {
			fineQ3 = codedChannels * int(d.extraQuant[celt.MaxBands+1]<<bitRes)
		}
		for i := 0; i < qextEnd; i++ {
			extBalance -= int(d.extraPulses[celt.MaxBands+i]) + fineQ3
		}
		UnquantFineEnergy(&d.extDec, d.qextOldBandE, 0, qextEnd, qextCELTMaxQEXTBands,
			codedChannels, nil, d.extraQuant[celt.MaxBands:])
		for i := 0; i < qextEnd; i++ {
			d.tfZero[i] = 0
		}
		_, _, _ = quantAllQEXTExtraBandsDecode(&d.extDec, codedChannels, N, lm, qextEnd,
			d.extraPulses[celt.MaxBands:], d.tfZero[:], shortBlocks, alloc.Spread,
			qextDualStereo, qextIntensity, qextTotalBits, extBalance, d.disableInv, &seed,
			d.qextEdges, d.qextLogN, &d.qextBands)
		first := int(d.qextEdges[0]) * M
		last := int(d.qextEdges[qextEnd]) * M
		for c := 0; c < codedChannels; c++ {
			copy(X[c*N+first:c*N+last], d.qextBands.x[c*N+first:c*N+last])
		}
	}

	antiCollapseOn := false
	if alloc.AntiCollapseRsv > 0 {
		antiCollapseOn = main.DecodeRawBits(1) == 1
	}
	finalBandE := d.oldBandE
	if len(qextPayload) != 0 {
		finalBandE = nil
	}
	UnquantEnergyFinalise(main, finalBandE, d.start, d.end, celt.MaxBands,
		codedChannels, fineQuant, finePriority, totalBits-main.Tell())
	if antiCollapseOn {
		AntiCollapse(X, collapse, lm, codedChannels, N, d.start, d.end,
			d.oldBandE, d.oldLogE, d.oldLogE2, alloc.Pulses[:], d.eBands, celt.MaxBands, seed, false)
	}
	if silence {
		for i := 0; i < codedChannels*celt.MaxBands; i++ {
			d.oldBandE[i] = -gconst(28)
		}
	}

	d.synthesisQEXT(X, N, codedChannels, cc, lm, transient, silence, qextEnd, outSyn)
	d.applyQEXTCombFilter(decodeMem, N, lm, postfilterPitch, postfilterGain, postfilterTapset)
	d.postfilterPeriodOld = d.postfilterPeriod
	d.postfilterGainOld = d.postfilterGain
	d.postfilterTapsetOld = d.postfilterTapset
	d.postfilterPeriod = int32(postfilterPitch)
	d.postfilterGain = postfilterGain
	d.postfilterTapset = int32(postfilterTapset)
	if lm != 0 {
		d.postfilterPeriodOld = d.postfilterPeriod
		d.postfilterGainOld = d.postfilterGain
		d.postfilterTapsetOld = d.postfilterTapset
	}
	if codedChannels == 1 {
		copy(d.oldBandE[celt.MaxBands:2*celt.MaxBands], d.oldBandE[:celt.MaxBands])
	}
	if !transient {
		copy(d.oldLogE2, d.oldLogE[:2*celt.MaxBands])
		copy(d.oldLogE, d.oldBandE[:2*celt.MaxBands])
	} else {
		for i := range d.oldLogE {
			d.oldLogE[i] = min32(d.oldLogE[i], d.oldBandE[i])
		}
	}
	maxBackground := gconst001 * int32(imin(160, M))
	for i := range d.backgroundLogE {
		d.backgroundLogE[i] = min32(d.backgroundLogE[i]+maxBackground, d.oldBandE[i])
	}
	for c := 0; c < 2; c++ {
		for i := 0; i < d.start; i++ {
			d.oldBandE[c*celt.MaxBands+i] = 0
			d.oldLogE[c*celt.MaxBands+i] = -gconst(28)
			d.oldLogE2[c*celt.MaxBands+i] = -gconst(28)
		}
		for i := d.end; i < celt.MaxBands; i++ {
			d.oldBandE[c*celt.MaxBands+i] = 0
			d.oldLogE[c*celt.MaxBands+i] = -gconst(28)
			d.oldLogE2[c*celt.MaxBands+i] = -gconst(28)
		}
	}
	if len(qextPayload) != 0 {
		d.rng = main.Range() ^ d.extDec.Range()
	} else {
		d.rng = main.Range()
	}
	d.lastRes = out[:cc*frameSize]
	deemphasisQEXT(outSyn, d.lastRes, frameSize, cc, d.sampleRate, d.preemphMem)
	if main.Tell() > totalBits || (len(qextPayload) != 0 && d.extDec.Tell() > qextTotalBits/(1<<bitRes)) {
		return -3
	}
	return frameSize
}
