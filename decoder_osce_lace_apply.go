//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"github.com/thesyncim/gopus/silk"
)

// osceLACEMode picks between LACE and NoLACE for a given SILK internal
// bandwidth. libopus selects via DecControl.osce_method (driven by encoder
// complexity); the gopus wiring honours the task spec which is mode-aware
// over the SILK bandwidth:
//
//   - SILK NB (8 kHz internal) -> LACE
//   - SILK MB / WB / Hybrid (12-16 kHz internal) -> NoLACE
//
// In libopus the LACE / NoLACE postfilter only runs at fs_kHz == 16 (see
// `dnn/osce.c::osce_enhance_frame` early return); the gate downstream of
// this helper enforces the same restriction.
type osceLACEMode int

const (
	osceLACEModeNone   osceLACEMode = 0
	osceLACEModeLACE   osceLACEMode = 1
	osceLACEModeNoLACE osceLACEMode = 2
)

// pickOSCELACEMode mirrors the libopus complexity-based mode selection,
// projected onto the SILK internal bandwidth. The Phase 1 forward pass
// stub treats both modes identically; the selection is recorded so a
// Phase 2 forward pass can dispatch to the correct sub-model.
func pickOSCELACEMode(silkBW silk.Bandwidth) osceLACEMode {
	switch silkBW {
	case silk.BandwidthNarrowband:
		return osceLACEModeLACE
	case silk.BandwidthMediumband, silk.BandwidthWideband:
		return osceLACEModeNoLACE
	default:
		return osceLACEModeNone
	}
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
// LACE / NoLACE forward pass (Phase 1 = identity), and writes the
// enhanced lowband back so the downstream stages (silk_resampler and
// optional OSCE BWE) consume the postfilter-enhanced signal.
//
// Phase 1 keeps the forward pass as a no-op when the runtime is not
// loaded; the wiring is still useful as a compile-time hook that callers
// of the decoder can rely on. Once the per-channel LACEState / NoLACEState
// runtime lands in `internal/osce/lace`, the helper can dispatch to it
// without changes to the call site in `decoder_opus_frame.go`.
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
	if d == nil || !d.osceLACEEnabled || !d.osceLACEModelLoaded {
		return false
	}
	state := d.osceLACE
	if state == nil || state.osceLACEModel == nil || !state.osceLACEModel.Loaded() {
		// Phase 1: when the runtime model is not bound (e.g. blob did
		// not carry the LACE/NoLACE manifests, or a parallel agent has
		// not yet ported the forward pass), keep the standard
		// silk_resampler output untouched.
		return false
	}
	// libopus only runs LACE/NoLACE on SILK-only mode at 16 kHz internal
	// sample rate. Hybrid keeps the postfilter off because the CELT high
	// band would mask the model's spectral shaping.
	if mode != ModeSILK {
		d.osceLACE.prevLACEActive = false
		return false
	}
	pickedMode := pickOSCELACEMode(silkBW)
	if pickedMode == osceLACEModeNone {
		d.osceLACE.prevLACEActive = false
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
			d.osceLACE.prevLACEActive = false
			return false
		}
		ran := d.applyOSCELACEMonoChannel(leftNative, pickedMode)
		ran = d.applyOSCELACEMonoChannel(rightNative, pickedMode) || ran
		if !ran {
			d.osceLACE.prevLACEActive = false
			return false
		}
		// Phase 1 forward pass is identity, so the native lowband is
		// unchanged and the standard silk_resampler output already in
		// `out` remains correct. When Phase 2 lands, the native int16
		// buffer mutation will propagate naturally because
		// `LatestNativeStereo` returns slices aliasing decoder scratch
		// storage (so the downstream OSCE BWE call will consume the
		// enhanced lowband as in libopus).
		d.osceLACE.prevLACEActive = true
		_ = out
		_ = frameSize
		return true
	}

	// Mono SILK packet path (mono decoder or stereo decoder up-mixing a
	// mono packet). Only the slot-0 runtime is used.
	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 || len(native) < osceLACEFrameSamples {
		d.osceLACE.prevLACEActive = false
		return false
	}
	if !d.applyOSCELACEMonoChannel(native, pickedMode) {
		d.osceLACE.prevLACEActive = false
		return false
	}
	d.osceLACE.prevLACEActive = true
	_ = out
	_ = frameSize
	return true
}

// applyOSCELACEMonoChannel runs the LACE / NoLACE forward pass over one
// native-rate int16 SILK lowband channel and writes the enhanced samples
// back into the same buffer. The Phase 1 implementation is an identity
// pass that exercises the scratch arena (so the per-frame allocation
// guard catches future regressions) without modifying the audio; a
// follow-up Phase 2 port of `lace_process_20ms_frame` /
// `nolace_process_20ms_frame` will replace the identity copy.
func (d *Decoder) applyOSCELACEMonoChannel(native []int16, _ osceLACEMode) bool {
	if d == nil || d.osceLACE == nil {
		return false
	}
	state := d.osceLACE
	// libopus scales by 1/32768.f at the start of osce_enhance_frame; mirror
	// that so a Phase 2 forward pass can drop into applyInFloat without
	// re-scanning the int16 buffer.
	for i := 0; i < osceLACEFrameSamples; i++ {
		state.applyIn16[i] = native[i]
		state.applyInFloat[i] = float32(native[i]) * (1.0 / 32768.0)
	}
	// Phase 1: identity copy. Phase 2 will dispatch on `mode` and run
	// either `lace_process_20ms_frame` or `nolace_process_20ms_frame`
	// using the model layers in `state.osceLACEModel.LACE` /
	// `state.osceLACEModel.NoLACE` and the per-channel ring-buffer state
	// that ships alongside the forward pass.
	copy(state.applyOutFloat[:], state.applyInFloat[:])
	// Requantise to int16 and write back into the native lowband so the
	// downstream silk_resampler / OSCE BWE consumer reads the postfilter
	// output. libopus mutates psDec->outBuf in place; we mirror that by
	// overwriting the scratch slice returned by LatestNativeMono /
	// LatestNativeStereo.
	for i := 0; i < osceLACEFrameSamples; i++ {
		v := state.applyOutFloat[i] * 32768.0
		if v > 32767.0 {
			v = 32767.0
		} else if v < -32768.0 {
			v = -32768.0
		}
		state.applyOutInt16[i] = int16(v)
		native[i] = state.applyOutInt16[i]
	}
	return true
}
