//go:build gopus_extra_controls
// +build gopus_extra_controls

package gopus

import (
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
	"github.com/thesyncim/gopus/silk"
)

// osceLACEMode picks the decoder-complexity selected OSCE method.
type osceLACEMode int

const (
	osceLACEModeNone   osceLACEMode = 0
	osceLACEModeLACE   osceLACEMode = 1
	osceLACEModeNoLACE osceLACEMode = 2
)

func pickOSCELACEMode(complexity int) osceLACEMode {
	if complexity >= 7 {
		return osceLACEModeNoLACE
	}
	if complexity >= 6 {
		return osceLACEModeLACE
	}
	return osceLACEModeNone
}

// maybeApplyOSCELACEPostSilk runs the OSCE LACE / NoLACE postfilter forward
// pass on the SILK lowband output, before the silk_resampler upsamples to
// 48 kHz and before the OSCE BWE 16 kHz -> 48 kHz forward pass (when both
// are active). The helper mirrors the libopus invocation site:
//
//	silk/decode_frame.c::silk_decode_frame
//	    -> osce_enhance_frame(model, psDec, psDecCtrl, pOut, ...)
//
// which runs after the SILK CNG / glue stage and before the PLC update,
// mutating the int16 lowband samples in `pOut` in place. The gopus
// equivalent reads the latest native int16 lowband from
// `silk.Decoder.LatestNativeMono()` / `LatestNativeStereo()`, runs the
// LACE / NoLACE forward pass, and writes the enhanced lowband back so the
// downstream stages (silk_resampler and optional OSCE BWE) consume the
// postfilter-enhanced signal.
//
// Gates (all must hold for the postfilter to run):
//   - `osceLACEEnabled` (user toggle via SetOSCELACE)
//   - `osceLACEModelLoaded` (blob contains LACE/NoLACE manifests)
//   - `state.Loaded()` (the runtime model was successfully bound)
//   - SILK-only mode and SILK internal sample rate of 16 kHz (libopus
//     `osce_enhance_frame` early-returns when fs_kHz != 16)
//   - frame size of 20 ms (320 native samples per channel; matches
//     libopus `psDec->nb_subfr == 4`)
//
// `out` is the gopus output buffer holding `frameSize * channels` float32
// samples in [-1, 1]. Returns true when the postfilter pass executed and
// the native lowband / `out` buffer was overwritten; returns false when
// conditions are not met (callers keep the standard silk_resampler output
// untouched).
func (d *Decoder) maybeApplyOSCELACEPostSilk(
	out []float32,
	frameSize int,
	mode Mode,
	silkBW silk.Bandwidth,
	packetStereoLocal bool,
) bool {
	if d == nil {
		return false
	}
	if !d.osceLACEEnabled || !d.osceLACEModelLoaded {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}
	state := d.osceLACE
	if state == nil || state.osceLACEModel == nil || !state.osceLACEModel.Loaded() {
		// When the runtime model is not bound (e.g. blob did not carry
		// the LACE/NoLACE manifests), keep the standard silk_resampler
		// output untouched.
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}
	// libopus only runs LACE/NoLACE on SILK-only mode at 16 kHz internal
	// sample rate. Hybrid keeps the postfilter off because the CELT high
	// band would mask the model's spectral shaping.
	if mode != ModeSILK {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}
	pickedMode := pickOSCELACEMode(d.complexity)
	if pickedMode == osceLACEModeNone {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}

	// Stereo packet on a stereo decoder: libopus runs the postfilter
	// independently on each channel (per `silk_channel_state.osce`); the
	// gopus equivalent reads both native lowband channels from
	// `LatestNativeStereo`. When either runtime fails, leave the standard
	// silk_resampler output untouched.
	if packetStereoLocal && d.channels == 2 {
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDecoder.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < osceLACEFrameSamples {
			d.resetOSCELACEPostfilterState(packetStereoLocal)
			return false
		}
		d.prepareOSCELACEPostfilter(pickedMode, 2)
		ran := d.applyOSCELACEMonoChannel(leftNative, pickedMode, 0)
		ran = d.applyOSCELACEMonoChannel(rightNative, pickedMode, 1) || ran
		if !ran {
			d.resetOSCELACEPostfilterState(packetStereoLocal)
			return false
		}
		// `LatestNativeStereo` returns slices aliasing decoder scratch
		// storage, so the downstream OSCE BWE call consumes the enhanced
		// lowband as in libopus.
		d.osceLACE.prevLACEActive = true
		_ = out
		_ = frameSize
		return true
	}

	// Mono SILK packet path (mono decoder or stereo decoder up-mixing a
	// mono packet). Only the slot-0 runtime is used.
	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 || len(native) < osceLACEFrameSamples {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}
	d.prepareOSCELACEPostfilter(pickedMode, 1)
	if !d.applyOSCELACEMonoChannel(native, pickedMode, 0) {
		d.resetOSCELACEPostfilterState(packetStereoLocal)
		return false
	}
	d.osceLACE.prevLACEActive = true
	_ = out
	_ = frameSize
	return true
}

// applyOSCELACEMonoChannel runs the LACE / NoLACE forward pass over one
// native-rate int16 SILK lowband channel and writes the enhanced samples
// back into the same buffer.
func (d *Decoder) applyOSCELACEMonoChannel(native []int16, mode osceLACEMode, channelIdx int) bool {
	if d == nil || d.osceLACE == nil {
		return false
	}
	if channelIdx < 0 || channelIdx > 1 {
		return false
	}
	state := d.osceLACE
	// libopus scales by 1/32768.f at the start of osce_enhance_frame; mirror
	// that so the forward pass receives float32 input. Capture the
	// pre-enhancement int16 view in applyIn16 so the cross-fade has the raw
	// input available even after the enhancement overwrites the native lowband.
	for i := 0; i < osceLACEFrameSamples; i++ {
		state.applyIn16[i] = native[i]
		state.applyInFloat[i] = float32(native[i]) * (1.0 / 32768.0)
	}
	// Populate features via the libopus-parity OSCE feature extractor.
	// libopus calls `osce_calculate_features(psDec, psDecCtrl, features,
	// numbits, periods, xq, num_bits)` at the top of `osce_enhance_frame`;
	// we mirror that by reading the cached `silk_decoder_control` for the
	// channel (decoded in the latest finalizeDecodedChannelFrame call) and
	// running the gopus port of the feature extractor on the int16 lowband
	// captured in `applyIn16`.
	//
	// When no decoder control has been cached yet (e.g. the latest decode
	// was a PLC frame which bypasses `finalizeDecodedChannelFrame`), we
	// fall back to zero features + OSCE_NO_PITCH_VALUE periods so the
	// forward pass still runs but the AdaComb stages degenerate to a no-op
	// pitch lag instead of over-reading their history buffers.
	for i := range state.applyFeatures {
		state.applyFeatures[i] = 0
	}
	for i := range state.applyNumBits {
		state.applyNumBits[i] = 0
	}
	for i := range state.applyPeriods {
		state.applyPeriods[i] = 7
	}
	if ctrl, ok := d.silkDecoder.LatestDecoderControl(channelIdx); ok && ctrl.FsKHz == 16 && ctrl.NbSubfr == osceLACESubframesPerFrame {
		var fc osceLACE.FeatureControl
		fc.LPCOrder = ctrl.LPCOrder
		fc.PredCoefQ12[0] = ctrl.PredCoefQ12[0]
		fc.PredCoefQ12[1] = ctrl.PredCoefQ12[1]
		fc.LTPCoefQ14 = ctrl.LTPCoefQ14
		for sf := 0; sf < osceLACESubframesPerFrame; sf++ {
			fc.GainsQ16[sf] = ctrl.GainsQ16[sf]
			fc.PitchL[sf] = ctrl.PitchL[sf]
		}
		fc.SignalType = ctrl.SignalType
		numBits := ctrl.NumBits
		if numBits < 0 {
			numBits = 0
		}
		state.osceLACEFeatures[channelIdx].CalculateFeatures(
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
			state.applyIn16[:osceLACEFrameSamples],
			&fc,
			int32(numBits),
		)
	}
	switch mode {
	case osceLACEModeNoLACE:
		if err := state.osceNoLACERuntime[channelIdx].Process(
			state.applyInFloat[:osceLACEFrameSamples],
			state.applyOutFloat[:osceLACEFrameSamples],
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
		); err != nil {
			return false
		}
	default:
		if err := state.osceLACERuntime[channelIdx].Process(
			state.applyInFloat[:osceLACEFrameSamples],
			state.applyOutFloat[:osceLACEFrameSamples],
			state.applyFeatures[:],
			state.applyNumBits[:],
			state.applyPeriods[:],
		); err != nil {
			return false
		}
	}
	state.applyOSCELACEOutputReset(channelIdx)
	// Requantise to int16 and write back into the native lowband so the
	// downstream silk_resampler / OSCE BWE consumer reads the postfilter
	// output. libopus mutates psDec->outBuf in place; we mirror that by
	// overwriting the scratch slice returned by LatestNativeMono /
	// LatestNativeStereo.
	for i := 0; i < osceLACEFrameSamples; i++ {
		q := osceFloatToInt16(state.applyOutFloat[i])
		state.applyOutInt16[i] = q
		native[i] = q
	}
	return true
}

func (d *Decoder) prepareOSCELACEPostfilter(mode osceLACEMode, channels int) {
	if d == nil || d.osceLACE == nil {
		return
	}
	state := d.osceLACE
	if channels < 1 {
		channels = 1
	}
	if channels > len(state.laceResetFrames) {
		channels = len(state.laceResetFrames)
	}
	if !state.prevLACEActive || state.laceMethod != mode {
		for ch := 0; ch < channels; ch++ {
			state.osceLACEFeatures[ch].Reset()
			switch mode {
			case osceLACEModeLACE:
				state.osceLACERuntime[ch].Reset()
			case osceLACEModeNoLACE:
				state.osceNoLACERuntime[ch].Reset()
			default:
				state.osceLACERuntime[ch].Reset()
				state.osceNoLACERuntime[ch].Reset()
			}
			state.laceResetFrames[ch] = 2
		}
	}
	state.laceMethod = mode
	state.prevLACEActive = true
}

func (state *decoderOSCELACEState) applyOSCELACEOutputReset(channelIdx int) {
	if state == nil || channelIdx < 0 || channelIdx >= len(state.laceResetFrames) {
		return
	}
	switch state.laceResetFrames[channelIdx] {
	case 0:
		return
	case 1:
		osceLACECrossFade10ms(state.applyOutFloat[:osceLACEFrameSamples], state.applyInFloat[:osceLACEFrameSamples], osceLACEFrameSamples)
		state.laceResetFrames[channelIdx] = 0
	default:
		copy(state.applyOutFloat[:osceLACEFrameSamples], state.applyInFloat[:osceLACEFrameSamples])
		state.laceResetFrames[channelIdx]--
	}
}

// osceLACEMarkInactiveIfModeIneligible clears the LACE-active flag when the
// current packet's mode/bandwidth does not satisfy the LACE/NoLACE gate
// (SILK-only at 16 kHz internal). This catches Hybrid / CELT / SILK-NB
// packets where maybeApplyOSCELACEPostSilk is not invoked but the
// `prevLACEActive` transition tracking still needs to be updated.
//
// Without this clearing the next SILK WB/MB packet would incorrectly skip
// the LACE fade-in cross-fade because `prevLACEActive` could still be true
// from many packets ago.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(mode Mode, bandwidth Bandwidth) {
	if d == nil || d.osceLACE == nil {
		return
	}
	// LACE/NoLACE runs in SILK-only mode at WB or MB (the LACE NB mode
	// covers NB but with the same `prevLACEActive` flag); Hybrid and CELT
	// always bypass the postfilter so we must clear the prev flag.
	if mode == ModeSILK && (bandwidth == BandwidthWideband || bandwidth == BandwidthMediumband || bandwidth == BandwidthNarrowband) {
		// SILK packet with a LACE-eligible bandwidth: the SILK-only post-
		// decode hook handles the flag itself based on the actual SILK
		// internal bandwidth.
		return
	}
	d.osceLACE.prevLACEActive = false
}

func (d *Decoder) resetOSCELACEPostfilterState(packetStereoLocal bool) {
	if d == nil || d.osceLACE == nil {
		return
	}
	channels := 1
	if packetStereoLocal && d.channels == 2 {
		channels = 2
	}
	for ch := 0; ch < channels; ch++ {
		d.osceLACE.osceLACEFeatures[ch].Reset()
		d.osceLACE.osceLACERuntime[ch].Reset()
		d.osceLACE.osceNoLACERuntime[ch].Reset()
		d.osceLACE.laceResetFrames[ch] = 0
	}
	d.osceLACE.prevLACEActive = false
	d.osceLACE.laceMethod = osceLACEModeNone
}
