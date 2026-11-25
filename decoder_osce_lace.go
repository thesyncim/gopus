//go:build gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
	"github.com/thesyncim/gopus/internal/silk"
)

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

func (d *Decoder) osceLACEEnabledForComplexity() bool {
	if d == nil {
		return false
	}
	if d.osceLACEOverrideSet {
		return d.osceLACEEnabled
	}
	return d.complexity >= 6
}

func (d *Decoder) installOSCELACESilkPostfilterHook(mode Mode, _ silk.Bandwidth, packetStereoLocal bool) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetNativePostfilterHook(nil)
	if mode != ModeSILK && mode != ModeHybrid {
		return
	}
	if d.osceLACE == nil {
		d.osceLACE = &decoderOSCELACEState{}
	}
	pickedMode := osceLACEModeNone
	if d.osceLACEEnabledForComplexity() {
		pickedMode = pickOSCELACEMode(int(d.complexity))
	}

	channels := 1
	if packetStereoLocal && d.channels == 2 {
		channels = 2
	}
	d.prepareOSCELACEPostfilter(pickedMode, channels)
	d.osceLACEHookChannels = channels
	d.osceLACEHookStereo = packetStereoLocal
	d.osceLACEHookMode = pickedMode
	if d.osceLACEHook == nil {
		d.osceLACEHook = d.processOSCELACESilkPostfilter
	}
	d.silkDecoder.SetNativePostfilterHook(d.osceLACEHook)
}

func (d *Decoder) clearOSCELACESilkPostfilterHook() {
	if d != nil && d.silkDecoder != nil {
		d.silkDecoder.SetNativePostfilterHook(nil)
	}
}

func (d *Decoder) processOSCELACESilkPostfilter(channel int, samples []int16, ctrl silk.LatestDecoderControl) bool {
	if channel < 0 || channel >= d.osceLACEHookChannels {
		return false
	}
	if ctrl.FsKHz != 16 || ctrl.NbSubfr != osceLACESubframesPerFrame || len(samples) < osceLACEFrameSamples {
		d.resetOSCELACEChannel(channel, d.osceLACEHookMode)
		return false
	}
	if !d.applyOSCELACEMonoChannelWithControl(samples, d.osceLACEHookMode, channel, ctrl, true) {
		d.resetOSCELACEChannel(channel, d.osceLACEHookMode)
		return false
	}
	return true
}

func (d *Decoder) applyOSCELACEMonoChannelWithControl(native []int16, mode osceLACEMode, channelIdx int, ctrl silk.LatestDecoderControl, ctrlOK bool) bool {
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
	if ctrlOK && ctrl.FsKHz == 16 && ctrl.NbSubfr == osceLACESubframesPerFrame {
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
			numBits,
		)
	}
	modelLoaded := d.osceLACEModelLoaded && state.osceLACEModel != nil && state.osceLACEModel.Loaded()
	if !modelLoaded {
		mode = osceLACEModeNone
	}
	switch mode {
	case osceLACEModeNone:
		copy(state.applyOutFloat[:osceLACEFrameSamples], state.applyInFloat[:osceLACEFrameSamples])
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
// packets where the native SILK postfilter hook is not installed but the
// `prevLACEActive` transition tracking still needs to be updated.
//
// Without this clearing the next SILK WB packet would incorrectly skip
// the LACE fade-in cross-fade because `prevLACEActive` could still be true
// from many packets ago.
func (d *Decoder) osceLACEMarkInactiveIfModeIneligible(_ Mode, _ Bandwidth) {
	// SILK invokes the native OSCE hook for every decoded frame; the hook
	// resets its channel state when the internal rate or frame duration is
	// unsupported. Hybrid uses the same native 16 kHz post-SILK hook. CELT
	// frames do not enter SILK and leave its OSCE state untouched.
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

func (d *Decoder) resetOSCELACEChannel(channel int, mode osceLACEMode) {
	state := d.osceLACE
	if state == nil || channel < 0 || channel >= len(state.laceResetFrames) {
		return
	}
	state.osceLACEFeatures[channel].Reset()
	switch mode {
	case osceLACEModeLACE:
		state.osceLACERuntime[channel].Reset()
	case osceLACEModeNoLACE:
		state.osceNoLACERuntime[channel].Reset()
	}
	state.laceMethod = mode
	state.prevLACEActive = true
	state.laceResetFrames[channel] = 2
}

func osceLACECrossFade10ms(xEnhanced, xIn []float32, length int) {
	if length < 160 {
		return
	}
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := osceWindow[i]
		raw := float32((1.0 - w) * xIn[i])
		xEnhanced[i] = w*xEnhanced[i] + raw
	}
}

func validateOSCELACEModel(blob *dnnblob.Blob, supported bool) error {
	if blob == nil || !supported {
		return nil
	}
	_, err := osceLACE.Load(blob)
	return err
}

func (d *Decoder) bindOSCELACEModel(blob *dnnblob.Blob, supported bool) error {
	if d == nil {
		return nil
	}
	if blob == nil || !supported {
		if d.osceLACE != nil {
			for ch := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[ch].SetModelPreservingState(nil)
				_ = d.osceLACE.osceNoLACERuntime[ch].SetModelPreservingState(nil)
			}
			d.osceLACE.osceLACEModel = nil
		}
		return nil
	}
	model, err := osceLACE.Load(blob)
	if err != nil {
		// Keep d.osceModelsLoaded as the blob-level signal (still true) but
		// drop any prior runtime binding so callers see Loaded()==false.
		if d.osceLACE != nil {
			for ch := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[ch].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[ch].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
		}
		return err
	}
	if d.osceLACE == nil {
		d.osceLACE = &decoderOSCELACEState{}
	}
	d.osceLACE.osceLACEModel = model
	// Mirror the OSCE BWE binding: keep all per-channel runtime states in
	// sync with the loaded model so the forward pass can dispatch on a
	// per-channel slot without extra plumbing. libopus does the same with a
	// shared `OSCEModel` and one `LACEState`/`NoLACEState` per channel.
	for ch := range d.osceLACE.osceLACERuntime {
		if err := d.osceLACE.osceLACERuntime[ch].SetModelPreservingState(model); err != nil {
			for j := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[j].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[j].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
			return err
		}
		if err := d.osceLACE.osceNoLACERuntime[ch].SetModelPreservingState(model); err != nil {
			for j := range d.osceLACE.osceLACERuntime {
				_ = d.osceLACE.osceLACERuntime[j].SetModel(nil)
				_ = d.osceLACE.osceNoLACERuntime[j].SetModel(nil)
			}
			d.osceLACE.osceLACEModel = nil
			d.osceLACE = nil
			return err
		}
	}
	return nil
}

// osceLACEModelLoadedRuntime reports whether the decoder currently has a
// bound OSCE LACE/NoLACE model that the runtime can use. The bool mirrors
// the OSCE BWE `osceBWEModelLoadedRuntime` accessor and is intended for
// test parity assertions.
func (d *Decoder) osceLACEModelLoadedRuntime() bool {
	if d == nil || d.osceLACE == nil {
		return false
	}
	return d.osceLACE.osceLACEModel != nil && d.osceLACE.osceLACEModel.Loaded()
}

// Compile-time sanity: keep the model alias visible so we don't drop the
// dependency when refactoring.
var _ = (*osceLACE.Model)(nil)

const (
	// osceLACEFrameSamples is the int16 sample count one libopus
	// `osce_enhance_frame` invocation consumes / produces (20 ms @ 16
	// kHz). Both LACE and NoLACE share this footprint.
	osceLACEFrameSamples = 320
	// osceLACEFeatureDim mirrors libopus dnn/osce_config.h
	// `OSCE_FEATURE_DIM = 93`. Features are emitted at the SILK
	// sub-frame cadence (4 sub-frames per 20 ms frame).
	osceLACEFeatureDim = 93
	// osceLACESubframesPerFrame matches libopus `psDec->nb_subfr` when
	// the postfilter is enabled (the LACE/NoLACE path is only valid
	// for 4-subframe frames at 16 kHz; see dnn/osce.c::osce_enhance_frame).
	osceLACESubframesPerFrame = 4
)

type decoderOSCELACEState struct {
	osceLACEModel *osceLACE.Model

	// Per-channel forward-pass runtime state mirroring libopus
	// `silk_channel_state.osce.state.{lace,nolace}`. The decoder keeps both
	// LACE and NoLACE state slots per channel because `pickOSCELACEMode`
	// can switch between the two modes from one packet to the next (e.g.
	// SILK NB -> SILK WB transition). libopus calls `osce_reset` on a mode
	// change; here we keep the inactive runtime's history zeroed via
	// Reset() so a return to that mode starts from a clean state.
	//
	// Slot 0 carries the mid/left channel, slot 1 the side/right channel
	// when a stereo packet is decoded on a stereo decoder. Mono decode
	// paths only use slot 0.
	osceLACERuntime   [2]osceLACE.LACEState
	osceNoLACERuntime [2]osceLACE.NoLACEState

	// Per-channel OSCE feature extractor state mirroring libopus
	// `silk_channel_state.osce.features` (the `OSCEFeatureState` struct):
	// the rolling 350-sample signal history, smoothed bit count and
	// pitch-hangover bookkeeping. Both LACE and NoLACE share one feature
	// extractor per channel because libopus emits a single 4 * 93 feature
	// vector per 20 ms frame independent of the postfilter method.
	osceLACEFeatures [2]osceLACE.FeatureState

	// Pre-allocated working buffers for the post-SILK LACE/NoLACE forward
	// pass so the decoder hot path does not allocate per-frame. The buffers
	// are sized for one channel; stereo runs the forward pass sequentially
	// on each channel re-using the same scratch.
	//
	// applyIn16 holds the int16 SILK lowband samples read from
	// `silk.Decoder.LatestNativeMono()` / `LatestNativeStereo()` before any
	// scaling. applyInFloat is the float32 view consumed by the LACE /
	// NoLACE forward pass (libopus scales by 1/32768.f at the start of
	// `osce_enhance_frame`). applyOutFloat is the enhanced float32 output
	// the network writes. applyOutInt16 is the requantised int16 view that
	// downstream consumers (e.g. the silk_resampler PCM path or OSCE BWE
	// when both are active) read from.
	applyIn16     [osceLACEFrameSamples]int16
	applyInFloat  [osceLACEFrameSamples]float32
	applyOutFloat [osceLACEFrameSamples]float32
	applyOutInt16 [osceLACEFrameSamples]int16

	// Per-frame conditioning features consumed by the LACE / NoLACE
	// pitch / feature embedding net. Sized for the maximum 4-subframe
	// invocation libopus supports.
	applyFeatures [osceLACESubframesPerFrame * osceLACEFeatureDim]float32
	applyNumBits  [2]float32
	applyPeriods  [osceLACESubframesPerFrame]int

	// prevLACEActive tracks whether the previous decoded frame ran LACE /
	// NoLACE. laceMethod and laceResetFrames mirror libopus osce.method and
	// features.reset: after a reset, the first eligible frame passes raw and
	// the second eligible frame cross-fades before steady enhancement resumes.
	prevLACEActive  bool
	laceMethod      osceLACEMode
	laceResetFrames [2]int
}

func (d *Decoder) initOSCELossHook() {
	d.silkDecoder.SetNativeLossHook(d.resetOSCELACELostChannel)
}

// resetOSCELACELostChannel mirrors osce_reset in silk/decode_frame.c. A lost
// channel resets its selected filter and two-frame fade without changing the
// other channel's state or the selected method.
func (d *Decoder) resetOSCELACELostChannel(channel int) {
	s := d.osceLACE
	if s == nil || channel < 0 || channel >= len(s.laceResetFrames) {
		return
	}
	s.osceLACEFeatures[channel].Reset()
	switch s.laceMethod {
	case osceLACEModeLACE:
		s.osceLACERuntime[channel].Reset()
	case osceLACEModeNoLACE:
		s.osceNoLACERuntime[channel].Reset()
	}
	s.laceResetFrames[channel] = 2
}

type decoderOSCEFields struct {
	// The callback is bound once; per-packet context stays decoder-owned.
	osceLACEHook         silk.NativePostfilterHook
	osceLACEHookChannels int
	osceLACEHookStereo   bool
	osceLACEHookMode     osceLACEMode

	osceModelsLoaded    bool
	osceLACEModelLoaded bool
	osceBWEModelLoaded  bool
	osceBWEEnabled      bool
	osceBWE             *decoderOSCEBWEState
	osceLACEEnabled     bool
	osceLACEOverrideSet bool
	osceLACE            *decoderOSCELACEState
}

func (d *Decoder) setOSCEModelState(models dnnblob.DecoderModelState) {
	d.osceModelsLoaded = models.OSCE
	d.osceLACEModelLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

func (d *Decoder) osceBWEActive() bool {
	return d != nil && d.osceBWEEnabled
}
