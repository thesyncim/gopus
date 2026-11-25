//go:build gopus_osce

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/opusmath"
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) maybeApplyOSCEBWEPostSilk(
	out []float32,
	frameSize int,
	mode Mode,
	silkBW silk.Bandwidth,
	packetStereoLocal bool,
) bool {
	if d == nil || d.osceBWE == nil {
		return false
	}
	if !d.osceBWEEnabled || !d.osceBWEModelLoaded {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		// This SILK frame fell back to the standard resampler -> libopus
		// osce_extended_mode == OSCE_MODE_SILK_ONLY, which arms the fade-in for
		// the next BWE frame.
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	// libopus only enables OSCE_MODE_SILK_BBWE for SILK-only mode at 48 kHz
	// API and 16 kHz internal sample rate. Hybrid mode keeps the standard
	// silk_resampler path even when BWE is requested. See opus_decoder.c
	// around `OSCE_MODE_SILK_BBWE`. PLC re-uses this same gate (the caller
	// passes the previous packet's mode/bandwidth, which is how libopus
	// derives the BWE eligibility on `data == NULL`).
	if d.complexity < 4 || mode != ModeSILK || d.sampleRate != 48000 || silkBW != silk.BandwidthWideband {
		// BWE is inactive this frame. If the previous frame ran BWE we still
		// need a cross-fade so the resampler/BWE boundary is not audible.
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		// SILK-only fallback to the standard resampler (mode == SILK_ONLY).
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	// The runtime only supports 10 ms (160 sample) and 20 ms (320 sample)
	// frames at 16 kHz, which map to 480 / 960 samples per channel at 48 kHz.
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	in16Per := in48Per / 3
	state := d.osceBWE
	if !state.prevBWEActive {
		// libopus calls osce_bwe_reset when prev_osce_extended_mode is not
		// OSCE_MODE_SILK_BBWE (silk/dec_API.c).
		for i := range state.osceBWERuntime {
			state.osceBWERuntime[i].Reset()
			state.osceBWEFeatures[i].Reset()
		}
		state.monoPrevNativeLast = 0
	}
	// libopus only cross-fades the BWE output against a fresh standard-resampler
	// pass when the preceding frame was OSCE_MODE_SILK_ONLY or OSCE_MODE_HYBRID
	// (silk/dec_API.c). A cold start (bweModeNone) or a CELT->SILK_BBWE
	// transition (bweModeCeltOnly) emits the BWE output directly with no
	// fade-in, so the first BWE frame matches libopus sample-for-sample.
	fadeInIntoBWE := state.prevExtendedMode == bweModeSilkOnly || state.prevExtendedMode == bweModeHybrid

	// Stereo packet on a stereo decoder: run the per-channel forward pass
	// for the mid/left and side/right lowband signals independently and
	// interleave the result. This mirrors libopus where the
	// `for ( n = 0; n < ... nChannelsInternal; n++ )` loop calls
	// `osce_bwe(...)` once per channel using `channel_state[n].osce_bwe`.
	if packetStereoLocal && d.channels == 2 {
		if !d.osceBWE.osceBWERuntime[0].Loaded() || !d.osceBWE.osceBWERuntime[1].Loaded() {
			return false
		}
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDecoder.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return false
		}
		numFrames := in16Per / 160

		// Left/mid channel forward pass. Stage the int16 lowband for the
		// per-channel feature extractor and normalise to float32 [-1, 1]
		// for the BBWENet forward pass. libopus computes features
		// independently for each channel using channel_state[n].osce_bwe.
		for i := 0; i < in16Per; i++ {
			state.applyIn16Int[i] = leftNative[i]
			state.applyIn16[i] = float32(leftNative[i]) / 32768.0
		}
		state.osceBWEFeatures[0].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		if err := state.osceBWERuntime[0].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			return false
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
		// Interleave left channel into out[2*i].
		for i := 0; i < in48Per; i++ {
			out[2*i] = state.applyOut48[i]
		}

		// Right/side channel forward pass. Compute per-channel features
		// from the right-channel int16 lowband.
		for i := 0; i < in16Per; i++ {
			state.applyIn16Int[i] = rightNative[i]
			state.applyIn16[i] = float32(rightNative[i]) / 32768.0
		}
		state.osceBWEFeatures[1].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		if err := state.osceBWERuntime[1].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			// Left channel was already overwritten with the BWE output; we
			// cannot cleanly roll back to the standard resampler output
			// here. Returning false would leave a mixed left=BWE /
			// right=resampler buffer which is worse than committing fully
			// to the resampler path on failure. Because the right-channel
			// state shares the same model as the left, a failure on the
			// second pass implies a transient runtime issue (e.g. NaN);
			// surface the mid result by copying it to the right channel so
			// the output is at least coherent.
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = out[2*i]
			}
			state.prevBWEActive = true
			state.prevExtendedMode = bweModeSilkBBWE
			return true
		}
		if fadeInIntoBWE {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i+1]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
		for i := 0; i < in48Per; i++ {
			out[2*i+1] = state.applyOut48[i]
		}
		state.prevBWEActive = true
		state.prevExtendedMode = bweModeSilkBBWE
		return true
	}

	// Mono SILK packet path (mono decoder or stereo decoder up-mixing a
	// mono packet). Only the slot-0 runtime is used.
	if !d.osceBWE.osceBWERuntime[0].Loaded() {
		return false
	}
	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}
	if len(native) < in16Per {
		if d.osceBWE.prevBWEActive {
			d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
		}
		d.osceBWE.prevBWEActive = false
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
		return false
	}

	// Stage the int16 lowband for the feature extractor and normalise to
	// float32 [-1, 1] for the BBWENet forward pass. libopus performs both
	// conversions internally; keeping them side-by-side avoids re-scanning
	// the input on the hot path.
	//
	// libopus drives osce_bwe (both osce_bwe_calculate_features and the
	// BBWENet forward pass) from &samplesOut1_tmp[n][1]: the 16 kHz lowband
	// shifted one sample earlier than the freshly decoded frame, with index 0
	// carrying the previous frame's final sample (the SILK sStereo.sMid[1]
	// history). LatestNativeMono returns the raw decoded frame at [0], so the
	// previous frame's last sample is prepended and the final decoded sample
	// is carried into the next call -- reproducing libopus' one-sample input
	// delay that otherwise leaves the BWE output misaligned.
	state.applyIn16Int[0] = state.monoPrevNativeLast
	state.applyIn16[0] = float32(state.monoPrevNativeLast) / 32768.0
	for i := 1; i < in16Per; i++ {
		state.applyIn16Int[i] = native[i-1]
		state.applyIn16[i] = float32(native[i-1]) / 32768.0
	}
	state.monoPrevNativeLast = native[in16Per-1]
	numFrames := in16Per / 160

	// Port of libopus `osce_bwe_calculate_features`: produces a 114-float
	// feature vector per 10 ms hop (log-mag spectrogram + instantaneous-
	// frequency cross-power) consumed by the BBWENet feature net.
	state.osceBWEFeatures[0].CalculateFeatures(
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		state.applyIn16Int[:in16Per],
	)

	if err := state.osceBWERuntime[0].ProcessDelayed(
		state.applyIn16[:in16Per],
		state.applyOut48[:in48Per],
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		// Runtime failed -- keep the standard resampler output. Treat as a
		// BWE-inactive frame for transition tracking; if we were previously
		// active a fade-out would have been ideal but we have no usable BWE
		// data to cross-fade with, so just mark prev as inactive.
		state.prevBWEActive = false
		state.prevExtendedMode = bweModeSilkOnly
		return false
	}

	// Fade in after SILK-only or Hybrid output. A mono packet can target
	// either one or two API channels; use the left resampler channel for
	// the fade before duplicating the mono BWE result for stereo output.
	if fadeInIntoBWE {
		if d.channels == 1 {
			osceBWECrossFade10ms(state.applyOut48[:in48Per], out[:in48Per], 480)
		} else {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyOut48[:in48Per], state.applyFadeout48[:in48Per], 480)
		}
	}

	// Write BWE output to the mono channel of `out`. For mono channels==1 we
	// overwrite directly; mono packet on a stereo decoder duplicates the
	// BWE result on both channels.
	if d.channels == 1 {
		copy(out[:in48Per], state.applyOut48[:in48Per])
	} else {
		for i := 0; i < in48Per; i++ {
			v := state.applyOut48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
	state.prevBWEActive = true
	state.prevExtendedMode = bweModeSilkBBWE
	return true
}

// osceBWEMarkInactiveIfModeIneligible clears the BWE-active flag and records the
// libopus osce_extended_mode for packets whose mode/bandwidth does not satisfy
// OSCE_MODE_SILK_BBWE. This catches Hybrid / CELT / SILK-NB-MB packets where
// maybeApplyOSCEBWEPostSilk is not invoked but the transition tracking still
// needs to advance. Recording the precise mode (not just clearing a bool) lets
// the next BWE frame reproduce libopus' fade-in gating: a Hybrid or SILK-only
// predecessor arms the fade-in, while a CELT-only predecessor suppresses it
// (opus_decoder.c forces prev_osce_extended_mode == OSCE_MODE_CELT_ONLY on a
// CELT->SILK transition).
func (d *Decoder) osceBWEMarkInactiveIfModeIneligible(mode Mode, bandwidth Bandwidth, out []float32, frameSize int, packetStereoLocal bool) {
	if d == nil || d.osceBWE == nil {
		return
	}
	if mode == ModeSILK && bandwidth == BandwidthWideband {
		// SILK WB packets go through maybeApplyOSCEBWEPostSilk which manages
		// the flag itself.
		return
	}
	if d.osceBWE.prevBWEActive && len(out) >= frameSize*int(d.channels) {
		d.applyOSCEBWEFadeOut(out, frameSize, packetStereoLocal)
	}
	d.osceBWE.prevBWEActive = false
	switch mode {
	case ModeCELT:
		d.osceBWE.prevExtendedMode = bweModeCeltOnly
	case ModeHybrid:
		d.osceBWE.prevExtendedMode = bweModeHybrid
	default:
		// SILK-only NB/MB: libopus osce_extended_mode == OSCE_MODE_SILK_ONLY.
		d.osceBWE.prevExtendedMode = bweModeSilkOnly
	}
}

func (d *Decoder) resetOSCEBWEPostfilterState() {
	if d == nil || d.osceBWE == nil {
		return
	}
	for ch := range d.osceBWE.osceBWERuntime {
		d.osceBWE.osceBWERuntime[ch].Reset()
		d.osceBWE.osceBWEFeatures[ch].Reset()
	}
	d.osceBWE.prevBWEActive = false
	d.osceBWE.prevExtendedMode = bweModeNone
	d.osceBWE.monoPrevNativeLast = 0
}

// applyOSCEBWEFadeOut runs a fade-out cross-fade when leaving BWE: BWE on the
// previous lowband -> standard upsampled `out`. Mirrors the second branch of
// the libopus dec_API.c BWE handler where, after a BWE-active frame, the new
// frame's standard silk_resampler output is cross-faded against a fresh BWE
// pass on the same native lowband. We approximate that by running BWE on the
// current native lowband (if available) and fading the existing `out` against
// it. When BWE cannot run (e.g. native unavailable), the helper is a no-op.
func (d *Decoder) applyOSCEBWEFadeOut(out []float32, frameSize int, packetStereoLocal bool) {
	if d == nil || d.osceBWE == nil || !d.osceBWE.osceBWERuntime[0].Loaded() {
		return
	}
	if d.sampleRate != 48000 {
		return
	}
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		return
	}
	in16Per := in48Per / 3

	state := d.osceBWE
	numFrames := in16Per / 160
	// stage applies the libopus &samplesOut1_tmp[n][1] one-sample input delay
	// used by osce_bwe for mono. The stereo accessor already returns the
	// [1]-aligned midFrame/sideFrame slices, so stereo passes stage=false.
	runChannel := func(native []int16, channelIdx int, stage bool) bool {
		if channelIdx < 0 || channelIdx > 1 || !state.osceBWERuntime[channelIdx].Loaded() {
			return false
		}
		if len(native) < in16Per {
			return false
		}
		if stage {
			state.applyIn16Int[0] = state.monoPrevNativeLast
			state.applyIn16[0] = float32(state.monoPrevNativeLast) / 32768.0
			for i := 1; i < in16Per; i++ {
				state.applyIn16Int[i] = native[i-1]
				state.applyIn16[i] = float32(native[i-1]) / 32768.0
			}
			state.monoPrevNativeLast = native[in16Per-1]
		} else {
			for i := 0; i < in16Per; i++ {
				state.applyIn16Int[i] = native[i]
				state.applyIn16[i] = float32(native[i]) / 32768.0
			}
		}
		// Port of libopus `osce_bwe_calculate_features` -- compute the same
		// 114-float feature vector per 10 ms hop that the BWE-active path
		// uses, so the fade-out BWE pass produces output matching the per-
		// channel BWE state libopus cross-fades against.
		state.osceBWEFeatures[channelIdx].CalculateFeatures(
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
			state.applyIn16Int[:in16Per],
		)
		return state.osceBWERuntime[channelIdx].ProcessDelayed(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		) == nil
	}

	if packetStereoLocal && d.channels == 2 {
		leftNative, rightNative, samplesPerChannel, fsKHz, ok := d.silkDecoder.LatestNativeStereo()
		if !ok || fsKHz != 16 || samplesPerChannel < in16Per {
			return
		}
		if runChannel(leftNative, 0, false) {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i]
			}
			osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i] = state.applyFadeout48[i]
			}
		}
		if runChannel(rightNative, 1, false) {
			for i := 0; i < in48Per; i++ {
				state.applyFadeout48[i] = out[2*i+1]
			}
			osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
			for i := 0; i < in48Per; i++ {
				out[2*i+1] = state.applyFadeout48[i]
			}
		}
		return
	}

	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 || !runChannel(native, 0, true) {
		return
	}
	if d.channels == 1 {
		// `out` is the standard upsampled output (fadein), `applyOut48` is
		// the BWE output (fadeout). osceBWECrossFade10ms writes the cross-
		// fade into its first argument.
		osceBWECrossFade10ms(out[:in48Per], state.applyOut48[:in48Per], 480)
		return
	}
	if d.channels == 2 {
		for i := 0; i < in48Per; i++ {
			state.applyFadeout48[i] = out[2*i]
		}
		osceBWECrossFade10ms(state.applyFadeout48[:in48Per], state.applyOut48[:in48Per], 480)
		for i := 0; i < in48Per; i++ {
			v := state.applyFadeout48[i]
			out[2*i] = v
			out[2*i+1] = v
		}
	}
}

var osceWindow = [320]float32{
	0.004908718808, 0.014725683311, 0.024541228523, 0.034354408400, 0.044164277127,
	0.053969889210, 0.063770299562, 0.073564563600, 0.083351737332, 0.093130877450,
	0.102901041421, 0.112661287575, 0.122410675199, 0.132148264628, 0.141873117332,
	0.151584296010, 0.161280864678, 0.170961888760, 0.180626435180, 0.190273572448,
	0.199902370753, 0.209511902052, 0.219101240157, 0.228669460829, 0.238215641862,
	0.247738863176, 0.257238206902, 0.266712757475, 0.276161601717, 0.285583828929,
	0.294978530977, 0.304344802381, 0.313681740399, 0.322988445118, 0.332264019538,
	0.341507569661, 0.350718204573, 0.359895036535, 0.369037181064, 0.378143757022,
	0.387213886697, 0.396246695891, 0.405241314005, 0.414196874117, 0.423112513073,
	0.431987371563, 0.440820594212, 0.449611329655, 0.458358730621, 0.467061954019,
	0.475720161014, 0.484332517110, 0.492898192230, 0.501416360796, 0.509886201809,
	0.518306898929, 0.526677640552, 0.534997619887, 0.543266035038, 0.551482089078,
	0.559644990127, 0.567753951426, 0.575808191418, 0.583806933818, 0.591749407690,
	0.599634847523, 0.607462493302, 0.615231590581, 0.622941390558, 0.630591150148,
	0.638180132051, 0.645707604824, 0.653172842954, 0.660575126926, 0.667913743292,
	0.675187984742, 0.682397150168, 0.689540544737, 0.696617479953, 0.703627273726,
	0.710569250438, 0.717442741007, 0.724247082951, 0.730981620454, 0.737645704427,
	0.744238692572, 0.750759949443, 0.757208846506, 0.763584762206, 0.769887082016,
	0.776115198508, 0.782268511401, 0.788346427627, 0.794348361383, 0.800273734191,
	0.806121974951, 0.811892519997, 0.817584813152, 0.823198305781, 0.828732456844,
	0.834186732948, 0.839560608398, 0.844853565250, 0.850065093356, 0.855194690420,
	0.860241862039, 0.865206121757, 0.870086991109, 0.874883999665, 0.879596685080,
	0.884224593137, 0.888767277786, 0.893224301196, 0.897595233788, 0.901879654283,
	0.906077149740, 0.910187315596, 0.914209755704, 0.918144082372, 0.921989916403,
	0.925746887127, 0.929414632439, 0.932992798835, 0.936481041442, 0.939879024058,
	0.943186419177, 0.946402908026, 0.949528180593, 0.952561935658, 0.955503880820,
	0.958353732530, 0.961111216112, 0.963776065795, 0.966348024735, 0.968826845041,
	0.971212287799, 0.973504123096, 0.975702130039, 0.977806096779, 0.979815820533,
	0.981731107599, 0.983551773378, 0.985277642389, 0.986908548290, 0.988444333892,
	0.989884851171, 0.991229961288, 0.992479534599, 0.993633450666, 0.994691598273,
	0.995653875433, 0.996520189401, 0.997290456679, 0.997964603026, 0.998542563469,
	0.999024282300, 0.999409713092, 0.999698818696, 0.999891571247, 0.999987952167,
	0.999987952167, 0.999891571247, 0.999698818696, 0.999409713092, 0.999024282300,
	0.998542563469, 0.997964603026, 0.997290456679, 0.996520189401, 0.995653875433,
	0.994691598273, 0.993633450666, 0.992479534599, 0.991229961288, 0.989884851171,
	0.988444333892, 0.986908548290, 0.985277642389, 0.983551773378, 0.981731107599,
	0.979815820533, 0.977806096779, 0.975702130039, 0.973504123096, 0.971212287799,
	0.968826845041, 0.966348024735, 0.963776065795, 0.961111216112, 0.958353732530,
	0.955503880820, 0.952561935658, 0.949528180593, 0.946402908026, 0.943186419177,
	0.939879024058, 0.936481041442, 0.932992798835, 0.929414632439, 0.925746887127,
	0.921989916403, 0.918144082372, 0.914209755704, 0.910187315596, 0.906077149740,
	0.901879654283, 0.897595233788, 0.893224301196, 0.888767277786, 0.884224593137,
	0.879596685080, 0.874883999665, 0.870086991109, 0.865206121757, 0.860241862039,
	0.855194690420, 0.850065093356, 0.844853565250, 0.839560608398, 0.834186732948,
	0.828732456844, 0.823198305781, 0.817584813152, 0.811892519997, 0.806121974951,
	0.800273734191, 0.794348361383, 0.788346427627, 0.782268511401, 0.776115198508,
	0.769887082016, 0.763584762206, 0.757208846506, 0.750759949443, 0.744238692572,
	0.737645704427, 0.730981620454, 0.724247082951, 0.717442741007, 0.710569250438,
	0.703627273726, 0.696617479953, 0.689540544737, 0.682397150168, 0.675187984742,
	0.667913743292, 0.660575126926, 0.653172842954, 0.645707604824, 0.638180132051,
	0.630591150148, 0.622941390558, 0.615231590581, 0.607462493302, 0.599634847523,
	0.591749407690, 0.583806933818, 0.575808191418, 0.567753951426, 0.559644990127,
	0.551482089078, 0.543266035038, 0.534997619887, 0.526677640552, 0.518306898929,
	0.509886201809, 0.501416360796, 0.492898192230, 0.484332517110, 0.475720161014,
	0.467061954019, 0.458358730621, 0.449611329655, 0.440820594212, 0.431987371563,
	0.423112513073, 0.414196874117, 0.405241314005, 0.396246695891, 0.387213886697,
	0.378143757022, 0.369037181064, 0.359895036535, 0.350718204573, 0.341507569661,
	0.332264019538, 0.322988445118, 0.313681740399, 0.304344802381, 0.294978530977,
	0.285583828929, 0.276161601717, 0.266712757475, 0.257238206902, 0.247738863176,
	0.238215641862, 0.228669460829, 0.219101240157, 0.209511902052, 0.199902370753,
	0.190273572448, 0.180626435180, 0.170961888760, 0.161280864678, 0.151584296010,
	0.141873117332, 0.132148264628, 0.122410675199, 0.112661287575, 0.102901041421,
	0.093130877450, 0.083351737332, 0.073564563600, 0.063770299562, 0.053969889210,
	0.044164277127, 0.034354408400, 0.024541228523, 0.014725683311, 0.004908718808,
}

// osceBWECrossFade10ms mirrors libopus dnn/osce_features.c osce_bwe_cross_fade_10ms.
// It mixes a 10 ms (480-sample @ 48 kHz) fade-in buffer against a fade-out
// buffer using the libopus osce_window weights; the result is written back
// into the fade-in buffer.
//
// libopus operates on opus_int16 PCM and writes each blended sample through
// `(int)(x + 0.5)`. The Go decoder keeps float32 PCM at its API boundary, so
// this helper converts the two inputs to the same int16 domain, applies the
// reference writeback, then normalises the result back to float32.
//
// It leaves both buffers unchanged if length or either buffer is shorter than
// 480 samples. Samples beyond the first 480 are untouched.
func osceBWECrossFade10ms(xFadein, xFadeout []float32, length int) {
	if length < 480 {
		return
	}
	if len(xFadein) < 480 || len(xFadeout) < 480 {
		return
	}
	const oneThird = 1.0 / 3.0
	for i := 0; i < 160; i++ {
		var diff float32
		if i != 159 {
			diff = osceWindow[i+1] - osceWindow[i]
		}
		wCurr := osceWindow[i]
		xFadein[3*i+0] = float32(osceBWECrossFadeSample(wCurr, xFadein[3*i+0], xFadeout[3*i+0])) * (1.0 / 32768.0)
		wCurr += diff * oneThird
		xFadein[3*i+1] = float32(osceBWECrossFadeSample(wCurr, xFadein[3*i+1], xFadeout[3*i+1])) * (1.0 / 32768.0)
		wCurr += diff * oneThird
		xFadein[3*i+2] = float32(osceBWECrossFadeSample(wCurr, xFadein[3*i+2], xFadeout[3*i+2])) * (1.0 / 32768.0)
	}
}

func osceBWECrossFadeSample(weight, fadein, fadeout float32) int16 {
	fi := osceFloatToInt16(fadein)
	fo := osceFloatToInt16(fadeout)
	v := weight*float32(fi) + (1.0-weight)*float32(fo) + 0.5
	return int16(int32(v))
}

func osceFloatToInt16(x float32) int16 {
	return opusmath.Float32ToInt16OSCEOutputScale(x)
}

func validateOSCEBWEModel(blob *dnnblob.Blob, supported bool) error {
	if blob == nil || !supported {
		return nil
	}
	_, err := osceBWE.LoadModel(blob)
	return err
}

func (d *Decoder) bindOSCEBWEModel(blob *dnnblob.Blob, supported bool) error {
	if d == nil {
		return nil
	}
	if blob == nil || !supported {
		if d.osceBWE != nil {
			d.osceBWE.osceBWEModel = nil
			for ch := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[ch].SetModel(nil)
			}
			d.osceBWE = nil
		}
		return nil
	}
	model, err := osceBWE.LoadModel(blob)
	if err != nil {
		// Keep d.osceBWEModelLoaded as the blob-level signal (still true) but
		// drop any prior runtime binding so callers see Loaded()==false.
		if d.osceBWE != nil {
			d.osceBWE.osceBWEModel = nil
			for ch := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[ch].SetModel(nil)
			}
			d.osceBWE = nil
		}
		return err
	}
	if d.osceBWE == nil {
		d.osceBWE = &decoderOSCEBWEState{}
	}
	d.osceBWE.osceBWEModel = model
	// Mirror the LPCNet pattern: keep both runtime states in sync with the
	// loaded model so each channel slot can run the forward pass
	// independently.
	for ch := range d.osceBWE.osceBWERuntime {
		if err := d.osceBWE.osceBWERuntime[ch].SetModelPreservingState(blob); err != nil {
			d.osceBWE.osceBWEModel = nil
			// Clear any sibling slot we may have already bound so the
			// runtime state is fully detached on failure.
			for j := range d.osceBWE.osceBWERuntime {
				_ = d.osceBWE.osceBWERuntime[j].SetModel(nil)
			}
			d.osceBWE = nil
			return err
		}
	}
	return nil
}

// osceBWEModelLoadedRuntime reports whether the decoder currently has a bound
// OSCE BWE model that the runtime can use. The bool mirrors the LPCNet
// `Loaded()` accessors and is intended for test parity assertions.
//
// Stereo SILK WB requires both per-channel runtime slots to be bound; this
// helper returns true when slot 0 (mid/left) is loaded, matching the legacy
// readiness probe. Callers that care specifically about the side-channel
// runtime can introspect `d.osceBWE.osceBWERuntime[1].Loaded()`.
func (d *Decoder) osceBWEModelLoadedRuntime() bool {
	if d == nil || d.osceBWE == nil {
		return false
	}
	return d.osceBWE.osceBWEModel != nil && d.osceBWE.osceBWERuntime[0].Loaded()
}

// Compile-time sanity: keep the runtime alias visible so we don't drop the
// dependency when refactoring the slot count.
var _ = (*osceBWE.State)(nil)

const (
	bweModeNone     = 0
	bweModeSilkOnly = 1000 // OSCE_MODE_SILK_ONLY
	bweModeHybrid   = 1001 // OSCE_MODE_HYBRID
	bweModeCeltOnly = 1002 // OSCE_MODE_CELT_ONLY
	bweModeSilkBBWE = 1003 // OSCE_MODE_SILK_BBWE
)

// decoderOSCEBWEState carries decoder-side OSCE BWE runtime bookkeeping under
// the explicit extra-controls build. The `osceBWEModel` field follows the same
// pattern as the FARGAN / Predictor bindings: it is non-nil once
// `SetDNNBlob` has successfully bound an OSCE BWE-capable weights blob.
//
// libopus keeps a per-channel `silk_OSCE_BWE_struct` in each
// `silk_channel_state` and runs `osce_bwe(...)` independently on each SILK
// lowband channel (see `silk/dec_API.c` around the `OSCE_MODE_SILK_BBWE`
// branch). The gopus runtime mirrors that with one `osceBWE.State` per
// channel; index 0 carries the mid/left channel state, index 1 the side/
// right channel state. Mono decode paths only use slot 0.
type decoderOSCEBWEState struct {
	osceBWEModel    *osceBWE.Model
	osceBWERuntime  [2]osceBWE.State
	osceBWEFeatures [2]osceBWE.FeatureState

	// Pre-allocated working buffers for the post-SILK BWE forward pass so
	// the decoder hot path does not allocate per-frame. The input/output
	// buffers are sized for one channel; stereo runs the forward pass
	// sequentially on each channel re-using the same scratch.
	applyIn16      [320]float32 // 20 ms @ 16 kHz max
	applyIn16Int   [320]int16   // signed-int16 view consumed by the feature extractor
	applyOut48     [3 * 320]float32
	applyFadeout48 [3 * 320]float32
	applyFeatures  [2 * osceBWE.FeatureDim]float32

	// prevBWEActive mirrors libopus DecControl.prev_osce_extended_mode for the
	// OSCE_MODE_SILK_BBWE bit. When the previous frame ran BWE but the current
	// frame does not (or vice versa), maybeApplyOSCEBWEPostSilk applies a 10 ms
	// cross-fade between the BWE output and the standard silk_resampler
	// output. This mirrors osce_bwe_cross_fade_10ms in libopus dec_API.c.
	prevBWEActive bool

	// prevExtendedMode tracks the full libopus DecControl.prev_osce_extended_mode
	// (one of the bweMode* values) across decoded frames. libopus only
	// cross-fades INTO BWE when the preceding frame was OSCE_MODE_SILK_ONLY or
	// OSCE_MODE_HYBRID (silk/dec_API.c): on a cold start (bweModeNone) or a
	// CELT->SILK_BBWE transition (bweModeCeltOnly, set by opus_decoder.c when
	// prev_mode == MODE_CELT_ONLY) the first BWE frame is emitted without a
	// fade-in. prevBWEActive alone cannot distinguish those cases.
	prevExtendedMode int

	// monoPrevNativeLast carries the last native (16 kHz) SILK sample of the
	// previous mono BWE frame. libopus feeds osce_bwe (both the BBWENet
	// forward pass and osce_bwe_calculate_features) from
	// &samplesOut1_tmp[n][1] -- the 16 kHz lowband shifted one sample earlier
	// than the freshly decoded frame, with samplesOut1_tmp[n][1] holding the
	// previous frame's final sample (the SILK sStereo.sMid[1] history). The
	// gopus mono lowband accessor (LatestNativeMono) returns the raw decoded
	// frame at [0]; staging this one-sample history reproduces libopus' BWE
	// input exactly. The stereo path already captures midFrame[1:] (the same
	// [1]-aligned slice) so it needs no separate history.
	monoPrevNativeLast int16
}
