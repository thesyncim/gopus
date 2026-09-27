//go:build gopus_fixed_point

package encoder

import (
	"math/bits"

	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// fixedDCRejectRes ports src/opus_encoder.c:dc_reject for the selected
// FIXED_POINT+ENABLE_RES24 build. Input and output are opus_res int32 Q8; hpMem
// carries the C opus_val32 state, including its unused second word per channel.
func fixedDCRejectRes(input, output []int32, hpMem *[4]int32, fs, channels, cutoff int) {
	shift := bits.Len32(uint32(fs/(cutoff*4))) - 1
	const saturation = int32((1 << 24) - 1)
	for c := 0; c < channels; c++ {
		for i := c; i < len(input); i += channels {
			x := input[i]
			if x > saturation {
				x = saturation
			} else if x < -saturation {
				x = -saturation
			}
			x <<= 6 // SHL32(x,14-RES_SHIFT), RES_SHIFT=8.
			y := x - hpMem[2*c]
			hpMem[2*c] += (x - hpMem[2*c] + (1 << (shift - 1))) >> shift
			output[i] = (y + (1 << 5)) >> 6
		}
	}
}

// fixedFloatToRes ports FLOAT2RES/FLOAT2INT24 in celt/arch.h and
// celt/float_cast.h for the selected FIXED_POINT+ENABLE_RES24 build.
func fixedFloatToRes(sample float32) int32 {
	if sample > 2 {
		return 1 << 24
	}
	if sample < -2 {
		return -(1 << 24)
	}
	return opusmath.Float32ToInt24(sample)
}

// prepareFixedInputRes retains the integer coding input independently of the
// float32 policy/analysis path. Signed16 short input remains exact after the
// public lattice conversion, while float and signed24 APIs retain Q8 values.
func (e *Encoder) prepareFixedInputRes(pcm []float32) {
	if cap(e.fixedRawRes) < len(pcm) {
		e.fixedRawRes = make([]int32, len(pcm))
	}
	e.fixedRawRes = e.fixedRawRes[:len(pcm)]
	for i, sample := range pcm {
		e.fixedRawRes[i] = fixedFloatToRes(sample)
	}
	e.fixedInputActive = true
	e.fixedFrameReady = false
	e.fixedFrameCursor = 0
}

func (e *Encoder) clearFixedInputRes() {
	e.fixedInputActive = false
	e.fixedFrameReady = false
}

func (e *Encoder) preprocessFixedInputRes(frameSize int) {
	if !e.fixedInputActive || len(e.fixedRawRes) != frameSize*int(e.channels) {
		return
	}
	if cap(e.fixedFiltered) < len(e.fixedRawRes) {
		e.fixedFiltered = make([]int32, len(e.fixedRawRes))
	}
	e.fixedFiltered = e.fixedFiltered[:len(e.fixedRawRes)]
	if !e.voipApp {
		fixedDCRejectRes(e.fixedRawRes, e.fixedFiltered, &e.fixedHPMem, int(e.sampleRate), int(e.channels), 3)
	}
	// The VoIP integer biquad is a separate selected-C stage. The short-path
	// Q8 frame is used only when the non-VoIP filter above has run.
}

func (e *Encoder) prepareFixedCELTPCM(frameSize int) {
	e.fixedFrameReady = false
	channels := int(e.channels)
	frameSamples := frameSize * channels
	if !e.fixedInputActive || e.voipApp || e.fixedFrameCursor+frameSamples > len(e.fixedFiltered) {
		return
	}
	e.fixedFrameSource = e.fixedFiltered[e.fixedFrameCursor : e.fixedFrameCursor+frameSamples]
	if cap(e.fixedDelayed) < frameSamples {
		e.fixedDelayed = make([]int32, frameSamples)
	}
	e.fixedDelayed = e.fixedDelayed[:frameSamples]
	if e.lowDelay {
		copy(e.fixedDelayed, e.fixedFrameSource)
		e.fixedFrameReady = true
		return
	}
	fs := int(e.sampleRate)
	delaySamples := (fs / 250) * channels
	bufferSamples := (fs / 100) * channels
	if len(e.fixedDelayBuffer) != bufferSamples {
		e.fixedDelayBuffer = make([]int32, bufferSamples)
	}
	tail := bufferSamples - delaySamples
	if frameSamples <= delaySamples {
		copy(e.fixedDelayed, e.fixedDelayBuffer[tail:tail+frameSamples])
	} else {
		copy(e.fixedDelayed, e.fixedDelayBuffer[tail:])
		copy(e.fixedDelayed[delaySamples:], e.fixedFrameSource[:frameSamples-delaySamples])
	}
	e.fixedFrameReady = true
}

func (e *Encoder) updateFixedDelayBuffer(frameSize int) {
	frameSamples := frameSize * int(e.channels)
	if !e.fixedFrameReady || len(e.fixedFrameSource) != frameSamples {
		return
	}
	bufferSamples := (int(e.sampleRate) / 100) * int(e.channels)
	if len(e.fixedDelayBuffer) != bufferSamples {
		e.fixedDelayBuffer = make([]int32, bufferSamples)
	}
	if frameSamples >= bufferSamples {
		copy(e.fixedDelayBuffer, e.fixedFrameSource[frameSamples-bufferSamples:])
		e.fixedFrameCursor += frameSamples
		return
	}
	keep := bufferSamples - frameSamples
	copy(e.fixedDelayBuffer[:keep], e.fixedDelayBuffer[frameSamples:])
	copy(e.fixedDelayBuffer[keep:], e.fixedFrameSource)
	e.fixedFrameCursor += frameSamples
}

func (e *Encoder) applyFixedStereoWidth(prevWidthQ14 int16) {
	if !e.fixedFrameReady || e.channels != 2 || len(e.celtEnergyMask) != 0 || e.restrictedSilkApp {
		return
	}
	widthQ14 := e.hybridStereoWidthQ14
	if prevWidthQ14 < 1<<14 || widthQ14 < 1<<14 {
		fixedpoint.StereoFadeRes(e.fixedDelayed, prevWidthQ14, widthQ14, int(e.sampleRate))
	}
}
