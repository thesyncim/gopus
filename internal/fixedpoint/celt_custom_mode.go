//go:build gopus_fixed_point && gopus_custom_modes

package fixedpoint

import "math"

// CELTCustomMode supplies the geometry shared by a custom CELT encoder and
// decoder. The 21-band scaled family uses the static eBands, allocation and
// pulse-cache tables, but generates its own MDCT and overlap window.
type CELTCustomMode struct {
	Fs, FrameSize, ShortMdctSize, Overlap, MaxLM, EffEBands int
}

func customPreemph(fs int) (coef0, coef1, coef2, coef3 int16) {
	// celt/modes.c opus_custom_mode_create(): QCONST16 values in the
	// FIXED_POINT branch. coef2 uses SIG_SHIFT=12, coef3 uses Q13.
	switch {
	case fs < 12000:
		return 11469, -5898, 1114, 30118
	case fs < 24000:
		return 19661, -5898, 1812, 18513
	case fs < 40000:
		return 25559, -3277, 3072, 10923
	default:
		return staticMDCT48000Preemph0, 0, 4096, 8192
	}
}

func customWindowQ15(overlap int) []int16 {
	// celt/modes.c opus_custom_mode_create() FIXED_POINT/!ENABLE_QEXT
	// computes this window with double sin() and floor(.5 + 32768*x).
	window := make([]int16, overlap)
	for i := range window {
		phase := .5 * math.Pi * (float64(i) + .5) / float64(overlap)
		s := math.Sin(phase)
		w := math.Floor(.5 + 32768*math.Sin(.5*math.Pi*s*s))
		if w > 32767 {
			w = 32767
		}
		window[i] = int16(w)
	}
	return window
}

// NewCELTEncoderCustom builds the fixed CELT state for a 21-band scaled-family
// custom mode. A nil result means the MDCT size is not factorable by libopus's
// radix-2/3/5 FFT. The caller has already checked mode creation and band count.
func NewCELTEncoderCustom(channels int, mode CELTCustomMode) *CELTEncoder {
	mdct := NewMDCTLookup(2*mode.FrameSize, mode.MaxLM)
	if mdct == nil {
		return nil
	}
	e := NewCELTEncoder(channels)
	e.modeFs = mode.Fs
	e.shortMdctSize = mode.ShortMdctSize
	e.overlap = mode.Overlap
	e.maxLM = mode.MaxLM
	e.effEBands = mode.EffEBands
	e.end = mode.EffEBands
	e.maxPeriod = combFilterMaxPeriod
	e.qextScale = 1
	e.upsample = 1
	e.preemph0, e.preemph1, e.preemph2, _ = customPreemph(mode.Fs)
	e.mdct = mdct
	e.window = customWindowQ15(mode.Overlap)
	e.inMem = make([]int32, channels*mode.Overlap)
	return e
}

// NewCELTDecoderCustom builds the decoder for the same generated mode.
func NewCELTDecoderCustom(channels int, mode CELTCustomMode) *CELTDecoder {
	mdct := NewMDCTLookup(2*mode.FrameSize, mode.MaxLM)
	if mdct == nil {
		return nil
	}
	d := NewCELTDecoder(channels)
	d.shortMdctSize = mode.ShortMdctSize
	d.overlap = mode.Overlap
	d.maxLM = mode.MaxLM
	d.effEBands = mode.EffEBands
	d.end = mode.EffEBands
	d.preemph0, d.preemph1, _, d.preemph3 = customPreemph(mode.Fs)
	d.mdct = mdct
	d.window = customWindowQ15(mode.Overlap)
	d.decodeMem = make([]int32, channels*(celtDecodeBufferSize+mode.Overlap))
	d.deemphasisScratch = make([]int32, mode.FrameSize)
	d.prefilterFoldScratch = make([]int32, mode.Overlap)
	return d
}
