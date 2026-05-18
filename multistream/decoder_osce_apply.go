//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package multistream

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	"github.com/thesyncim/gopus/silk"
)

// applyOSCEPostSilk runs the libopus OSCE LACE/NoLACE postfilter and the OSCE
// BWE 16 kHz -> 48 kHz forward pass on the SILK lowband output of this stream
// decoder. Mirrors `maybeApplyOSCELACEPostSilk` + `maybeApplyOSCEBWEPostSilk`
// in package gopus. `out` is the float32 PCM buffer the SILK decoder has just
// written (frameSize * channels samples @ 48 kHz, interleaved when stereo).
// `out` is overwritten in place by the BWE forward pass when its gates fire.
//
// Both postfilters are SILK-only at 16 kHz internal sample rate. The helper
// is a no-op outside of `gopus_unsupported_controls`.
func (d *streamState) applyOSCEPostSilk(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) {
	if d == nil || d.osceState == nil {
		return
	}
	// LACE / NoLACE runs first so the BWE pass can consume the postfilter-
	// enhanced native lowband. The helper short-circuits when the user
	// toggle is disabled or no model is bound.
	d.applyOSCELACE(out, frameSize, silkBW, packetStereo)
	d.applyOSCEBWE(out, frameSize, silkBW, packetStereo)
}

func (d *streamState) applyOSCELACE(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) bool {
	_ = out
	_ = frameSize
	if d == nil || !d.osceLACEEnabled || d.osceState == nil {
		return false
	}
	state := d.osceState
	if state.laceModel == nil || !state.laceModel.Loaded() {
		return false
	}

	mode := pickStreamOSCELACEMode(silkBW)
	if mode == streamOSCELACEModeNone {
		state.prevLACEActive = false
		return false
	}
	transition := !state.prevLACEActive

	if packetStereo && d.channels == 2 {
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDec.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < streamOSCELACEFrameSamples {
			state.prevLACEActive = false
			return false
		}
		ran := d.runOSCELACEChannel(leftNative, mode, transition, 0)
		ran = d.runOSCELACEChannel(rightNative, mode, transition, 1) || ran
		if !ran {
			state.prevLACEActive = false
			return false
		}
		state.prevLACEActive = true
		return true
	}

	native, fsKHz := d.silkDec.LatestNativeMono()
	if native == nil || fsKHz != 16 || len(native) < streamOSCELACEFrameSamples {
		state.prevLACEActive = false
		return false
	}
	if !d.runOSCELACEChannel(native, mode, transition, 0) {
		state.prevLACEActive = false
		return false
	}
	state.prevLACEActive = true
	return true
}

type streamOSCELACEMode int

const (
	streamOSCELACEModeNone   streamOSCELACEMode = 0
	streamOSCELACEModeLACE   streamOSCELACEMode = 1
	streamOSCELACEModeNoLACE streamOSCELACEMode = 2
)

// pickStreamOSCELACEMode mirrors `pickOSCELACEMode` in package gopus.
func pickStreamOSCELACEMode(silkBW silk.Bandwidth) streamOSCELACEMode {
	switch silkBW {
	case silk.BandwidthNarrowband:
		return streamOSCELACEModeLACE
	case silk.BandwidthMediumband, silk.BandwidthWideband:
		return streamOSCELACEModeNoLACE
	default:
		return streamOSCELACEModeNone
	}
}

func (d *streamState) runOSCELACEChannel(native []int16, mode streamOSCELACEMode, transition bool, channelIdx int) bool {
	if d == nil || d.osceState == nil {
		return false
	}
	if channelIdx < 0 || channelIdx > 1 {
		return false
	}
	state := d.osceState
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		state.laceApplyIn16[i] = native[i]
		state.laceApplyInF[i] = float32(native[i]) * (1.0 / 32768.0)
	}
	for i := range state.laceFeatures {
		state.laceFeatures[i] = 0
	}
	for i := range state.laceNumBits {
		state.laceNumBits[i] = 0
	}
	for i := range state.lacePeriods {
		state.lacePeriods[i] = 7
	}
	switch mode {
	case streamOSCELACEModeNoLACE:
		if err := state.noLACERuntime[channelIdx].Process(
			state.laceApplyInF[:streamOSCELACEFrameSamples],
			state.laceApplyOutF[:streamOSCELACEFrameSamples],
			state.laceFeatures[:],
			state.laceNumBits[:],
			state.lacePeriods[:],
		); err != nil {
			return false
		}
	default:
		if err := state.laceRuntime[channelIdx].Process(
			state.laceApplyInF[:streamOSCELACEFrameSamples],
			state.laceApplyOutF[:streamOSCELACEFrameSamples],
			state.laceFeatures[:],
			state.laceNumBits[:],
			state.lacePeriods[:],
		); err != nil {
			return false
		}
	}
	for i := 0; i < streamOSCELACEFrameSamples; i++ {
		v := state.laceApplyOutF[i] * 32768.0
		if v > 32767.0 {
			v = 32767.0
		} else if v < -32768.0 {
			v = -32768.0
		}
		state.laceApplyOutI16[i] = int16(v)
		native[i] = state.laceApplyOutI16[i]
	}
	if transition {
		streamOSCELACECrossFade10msInt16(native[:streamOSCELACEFrameSamples], state.laceApplyIn16[:streamOSCELACEFrameSamples])
	}
	return true
}

func (d *streamState) applyOSCEBWE(out []float32, frameSize int, silkBW silk.Bandwidth, packetStereo bool) bool {
	if d == nil || !d.osceBWEEnabled || d.osceState == nil {
		return false
	}
	state := d.osceState
	if state.bweModel == nil {
		return false
	}
	if d.sampleRate != 48000 || silkBW != silk.BandwidthWideband {
		state.prevBWEActive = false
		return false
	}
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		state.prevBWEActive = false
		return false
	}
	in16Per := in48Per / 3
	if !state.prevBWEActive {
		for i := range state.bweRuntime {
			state.bweRuntime[i].Reset()
			state.bweFeatures[i].Reset()
		}
	}
	if packetStereo && d.channels == 2 {
		if !state.bweRuntime[0].Loaded() || !state.bweRuntime[1].Loaded() {
			return false
		}
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDec.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return false
		}
		numFrames := in16Per / 160

		for i := 0; i < in16Per; i++ {
			state.bweIn16Int[i] = leftNative[i]
			state.bweIn16[i] = float32(leftNative[i]) / 32768.0
		}
		state.bweFeatures[0].CalculateFeatures(
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
			state.bweIn16Int[:in16Per],
		)
		if err := state.bweRuntime[0].ProcessDelayed(
			state.bweIn16[:in16Per],
			state.bweOut48[:in48Per],
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			return false
		}
		for i := 0; i < in48Per; i++ {
			out[2*i] = state.bweOut48[i]
		}
		for i := 0; i < in16Per; i++ {
			state.bweIn16Int[i] = rightNative[i]
			state.bweIn16[i] = float32(rightNative[i]) / 32768.0
		}
		state.bweFeatures[1].CalculateFeatures(
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
			state.bweIn16Int[:in16Per],
		)
		if err := state.bweRuntime[1].ProcessDelayed(
			state.bweIn16[:in16Per],
			state.bweOut48[:in48Per],
			state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = out[2*i]
			}
			state.prevBWEActive = true
			return true
		}
		for i := 0; i < in48Per; i++ {
			out[2*i+1] = state.bweOut48[i]
		}
		state.prevBWEActive = true
		return true
	}

	if !state.bweRuntime[0].Loaded() {
		return false
	}
	native, fsKHz := d.silkDec.LatestNativeMono()
	if native == nil || fsKHz != 16 || len(native) < in16Per {
		state.prevBWEActive = false
		return false
	}
	for i := 0; i < in16Per; i++ {
		state.bweIn16Int[i] = native[i]
		state.bweIn16[i] = float32(native[i]) / 32768.0
	}
	numFrames := in16Per / 160
	state.bweFeatures[0].CalculateFeatures(
		state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
		state.bweIn16Int[:in16Per],
	)
	if err := state.bweRuntime[0].ProcessDelayed(
		state.bweIn16[:in16Per],
		state.bweOut48[:in48Per],
		state.bweFeatBuf[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		state.prevBWEActive = false
		return false
	}
	if d.channels == 1 {
		copy(out[:in48Per], state.bweOut48[:in48Per])
	} else {
		for i := 0; i < in48Per; i++ {
			v := state.bweOut48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
	state.prevBWEActive = true
	return true
}

// streamOSCELACECrossFade10msInt16 mirrors `osceLACECrossFade10msInt16` in
// package gopus: 10 ms (160 sample) cross-fade between the postfilter output
// (`xEnhanced`) and the raw pre-enhancement input (`xIn`), written back into
// `xEnhanced`. Re-uses the libopus `osce_window[]` half-window weights.
func streamOSCELACECrossFade10msInt16(xEnhanced, xIn []int16) {
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := streamOSCEWindow[i]
		enh := float32(xEnhanced[i]) * (1.0 / 32768.0)
		raw := float32(xIn[i]) * (1.0 / 32768.0)
		mix := w*enh + (1.0-w)*raw
		v := mix * 32768.0
		if v > 32767.0 {
			v = 32767.0
		} else if v < -32768.0 {
			v = -32768.0
		}
		xEnhanced[i] = int16(v)
	}
}
