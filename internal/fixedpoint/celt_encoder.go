//go:build gopus_fixedpoint

package fixedpoint

// This file ports the FIXED_POINT (ENABLE_RES24) celt/celt_encoder.c encode
// front-end for the static 48000/960 custom mode: forward pre-emphasis
// (celt_preemphasis), the windowed forward MDCT striping (compute_mdcts) for
// the normal and transient block layouts, then compute_band_energies and
// normalise_bands, producing the interleaved post-MDCT signal (freq), the
// per-band energies (bandE) and the normalised bands (X) exactly as
// celt_encode_with_ec computes them just before quant_all_bands.
//
// Scope of this increment: the input -> normalized bands front-end only. The
// transient/prefilter/allocation/quantisation stages are out of scope here.

// int16ToRes implements libopus INT16TORES(a) for the ENABLE_RES24 build:
// SHL32(EXTEND32(a), RES_SHIFT) == a << 8.
func int16ToRes(a int16) int32 {
	return int32(a) << resShift
}

// res2sig implements libopus RES2SIG(a) for the ENABLE_RES24 build:
// SHL32(a, SIG_SHIFT-RES_SHIFT) == a << 4.
func res2sig(a int32) int32 {
	return shl32(a, sigShift-resShift)
}

// CELTEncoder is the FIXED_POINT integer CELT encoder front-end state for the
// static 48000/960 custom mode. It owns the per-channel pre-emphasis memory and
// the MDCT lookup / window / band tables, mirroring the reset region of the
// libopus OpusCustomEncoder fields the front-end touches.
type CELTEncoder struct {
	channels int

	start int
	end   int

	// preemphMemE mirrors st->preemph_memE: the per-channel pre-emphasis filter
	// state carried between frames.
	preemphMemE []int32

	mdct   *MDCTLookup
	window []int16
	eBands []int16
	logN   []int16
}

// NewCELTEncoder allocates and resets an integer CELT encoder front-end for the
// static 48000/960 mode with the given channel count (1 or 2). All cross-frame
// state (pre-emphasis memory) starts at zero, matching celt_encoder_init.
func NewCELTEncoder(channels int) *CELTEncoder {
	e := &CELTEncoder{
		channels:    channels,
		start:       0,
		end:         celtNbEBands,
		preemphMemE: make([]int32, channels),
		mdct:        NewStaticMDCTLookup48000(),
		window:      staticMDCT48000Window[:],
		eBands:      staticMDCT48000EBands[:],
		logN:        staticMDCT48000LogN[:],
	}
	return e
}

// SetBandRange sets the active band range (st->start / st->end), matching the
// CELT_SET_START_BAND_REQUEST / CELT_SET_END_BAND_REQUEST controls.
func (e *CELTEncoder) SetBandRange(start, end int) {
	e.start = start
	e.end = end
}

// FrontEnd ports the input -> normalized bands stage of celt_encode_with_ec for
// the static 48000/960 mode. pcm is channels*frameSize interleaved int16 PCM,
// frameSize the 48k-core per-channel sample count (shortMdctSize<<LM), and
// isTransient selects the transient MDCT striping (shortBlocks==M) over the
// normal long-block path. It returns the interleaved post-MDCT freq (celt_sig),
// the per-band bandE (celt_ener, channel-major) and the normalised X
// (celt_norm, interleaved), advancing the per-channel pre-emphasis memory.
func (e *CELTEncoder) FrontEnd(pcm []int16, frameSize int, isTransient bool) (freq, bandE, X []int32) {
	nbEBands := celtNbEBands
	overlap := celtOverlap
	shortMdctSize := celtShortMdctSize
	C := e.channels
	CC := e.channels

	LM := 0
	for LM = 0; LM <= celtMaxLM; LM++ {
		if shortMdctSize<<LM == frameSize {
			break
		}
	}
	M := 1 << LM
	N := M * shortMdctSize

	effEnd := e.end
	if effEnd > celtNbEBands {
		effEnd = celtNbEBands
	}

	shortBlocks := 0
	if isTransient {
		shortBlocks = M
	}

	// Build the per-channel in buffer (CC*(N+overlap)): the overlap prefix comes
	// from prefilter_mem (zero on a fresh encoder's first frame) and the body is
	// the pre-emphasised input. celt_preemphasis writes into in[c*(N+overlap)+overlap].
	in := make([]int32, CC*(N+overlap))
	for c := 0; c < CC; c++ {
		e.preemphasis(pcm[c:], in[c*(N+overlap)+overlap:], N, CC, c)
	}

	freq = make([]int32, CC*N)
	e.computeMDCTs(shortBlocks, in, freq, C, CC, LM)

	bandE = make([]int32, nbEBands*CC)
	ComputeBandEnergies(freq, e.eBands, e.logN, bandE, nbEBands, shortMdctSize, effEnd, C, LM)

	X = make([]int32, C*N)
	NormaliseBands(freq, X, bandE, e.eBands, nbEBands, shortMdctSize, effEnd, C, M)
	return freq, bandE, X
}

// preemphasis ports the fast path of libopus celt_preemphasis for the static
// 48000/960 mode (coef[1]==0, upsample==1, !clip): inp[i] = x - m and
// m = MULT16_32_Q15(coef0, x), where x = RES2SIG(INT16TORES(pcmp[CC*i])). It
// advances st->preemph_memE[c].
func (e *CELTEncoder) preemphasis(pcmp []int16, inp []int32, N, CC, c int) {
	coef0 := staticMDCT48000Preemph0
	m := e.preemphMemE[c]
	for i := 0; i < N; i++ {
		x := res2sig(int16ToRes(pcmp[CC*i]))
		inp[i] = x - m
		m = mult16x32q15(coef0, x)
	}
	e.preemphMemE[c] = m
}

// computeMDCTs ports the (static) compute_mdcts from celt_encoder.c for the
// FIXED_POINT non-QEXT, upsample==1 build: it windows/forward-MDCTs every
// sub-frame for each channel into the interleaved out, then for a downmixed
// CC==2,C==1 frame averages the two channels' MDCTs.
func (e *CELTEncoder) computeMDCTs(shortBlocks int, in, out []int32, C, CC, LM int) {
	overlap := celtOverlap
	var N, B, shift int
	if shortBlocks != 0 {
		B = shortBlocks
		N = celtShortMdctSize
		shift = celtMaxLM
	} else {
		B = 1
		N = celtShortMdctSize << LM
		shift = celtMaxLM - LM
	}
	for c := 0; c < CC; c++ {
		for b := 0; b < B; b++ {
			e.mdct.MDCTForward(
				in[c*(B*N+overlap)+b*N:],
				out[b+c*N*B:],
				e.window, overlap, shift, B)
		}
	}
	if CC == 2 && C == 1 {
		for i := 0; i < B*N; i++ {
			out[i] = add32(half32(out[i]), half32(out[B*N+i]))
		}
	}
}
