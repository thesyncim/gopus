//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	osceBWE "github.com/thesyncim/gopus/internal/osce/bwe"
	"github.com/thesyncim/gopus/silk"
)

// maybeApplyOSCEBWEPostSilk runs the OSCE BWE 16 kHz -> 48 kHz forward pass on
// the SILK lowband output and writes the bandwidth-extended PCM into `out`,
// overwriting the standard silk_resampler output.
//
// The hook is a libopus parity stub for the SILK_BBWE extended mode:
//
//	st->DecControl.osce_extended_mode == OSCE_MODE_SILK_BBWE
//
// which triggers when:
//   - the OSCE BWE control is enabled (SetOSCEBWE(true))
//   - a valid OSCE BWE model was bound via SetDNNBlob
//   - the packet was decoded as SILK-only at WB internal sample rate
//   - the API sample rate is 48 kHz
//
// Phase 3 of the wiring computes the per-10ms BBWENet feature vector from the
// raw int16 SILK lowband samples via the ported `osce_bwe_calculate_features`
// (see internal/osce/bwe/features.go). Errors from the runtime (e.g.
// unsupported frame size) fall through silently so the standard resampler
// output is retained.
//
// `out` is the gopus output buffer holding `frameSize * channels` float32
// samples in [-1, 1]. Returns true when the BWE pass executed and overwrote
// the output; returns false when conditions are not met (callers keep the
// standard resampler output untouched).
//
// Stereo handling: libopus runs `osce_bwe(...)` independently on each SILK
// lowband channel using a per-channel `silk_OSCE_BWE_struct` (see
// `silk/dec_API.c` around `OSCE_MODE_SILK_BBWE`). The gopus runtime mirrors
// that by keeping `[2]osceBWE.State` slots in `decoderOSCEBWEState`. For a
// stereo packet at a stereo decoder both per-channel runtimes are invoked
// sequentially and the result is interleaved into `out`.
func (d *Decoder) maybeApplyOSCEBWEPostSilk(
	out []float32,
	frameSize int,
	mode Mode,
	silkBW silk.Bandwidth,
	packetStereoLocal bool,
) bool {
	if d == nil || !d.osceBWEEnabled || !d.osceBWEModelLoaded {
		return false
	}
	if d.osceBWE == nil {
		return false
	}
	// libopus only enables OSCE_MODE_SILK_BBWE for SILK-only mode at 48 kHz
	// API and 16 kHz internal sample rate. Hybrid mode keeps the standard
	// silk_resampler path even when BWE is requested. See opus_decoder.c
	// around `OSCE_MODE_SILK_BBWE`.
	if mode != ModeSILK || d.sampleRate != 48000 || silkBW != silk.BandwidthWideband {
		return false
	}
	// The runtime only supports 10 ms (160 sample) and 20 ms (320 sample)
	// frames at 16 kHz, which map to 480 / 960 samples per channel at 48 kHz.
	in48Per := frameSize
	if in48Per != 480 && in48Per != 960 {
		return false
	}
	in16Per := in48Per / 3

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
		state := d.osceBWE
		numFrames := in16Per / 160
		// Zero the features vector once -- both channels currently consume
		// the same zero-features stub (Phase 1 forward pass).
		for i := 0; i < numFrames*osceBWE.FeatureDim; i++ {
			state.applyFeatures[i] = 0
		}

		// Left/mid channel forward pass.
		for i := 0; i < in16Per; i++ {
			state.applyIn16[i] = float32(leftNative[i]) / 32768.0
		}
		if err := state.osceBWERuntime[0].Process(
			state.applyIn16[:in16Per],
			state.applyOut48[:in48Per],
			state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		); err != nil {
			return false
		}
		// Interleave left channel into out[2*i].
		for i := 0; i < in48Per; i++ {
			out[2*i] = state.applyOut48[i]
		}

		// Right/side channel forward pass.
		for i := 0; i < in16Per; i++ {
			state.applyIn16[i] = float32(rightNative[i]) / 32768.0
		}
		if err := state.osceBWERuntime[1].Process(
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
			return true
		}
		for i := 0; i < in48Per; i++ {
			out[2*i+1] = state.applyOut48[i]
		}
		return true
	}

	// Mono SILK packet path (mono decoder or stereo decoder up-mixing a
	// mono packet). Only the slot-0 runtime is used.
	if !d.osceBWE.osceBWERuntime[0].Loaded() {
		return false
	}
	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 {
		return false
	}
	if len(native) < in16Per {
		return false
	}

	state := d.osceBWE
	// Stage the int16 lowband for the feature extractor and normalise to
	// float32 [-1, 1] for the BBWENet forward pass. libopus performs both
	// conversions internally; keeping them side-by-side avoids re-scanning
	// the input on the hot path.
	for i := 0; i < in16Per; i++ {
		state.applyIn16Int[i] = native[i]
		state.applyIn16[i] = float32(native[i]) / 32768.0
	}
	numFrames := in16Per / 160

	// Port of libopus `osce_bwe_calculate_features`: produces a 114-float
	// feature vector per 10 ms hop (log-mag spectrogram + instantaneous-
	// frequency cross-power) consumed by the BBWENet feature net.
	state.osceBWEFeatures[0].CalculateFeatures(
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		state.applyIn16Int[:in16Per],
	)

	if err := state.osceBWERuntime[0].Process(
		state.applyIn16[:in16Per],
		state.applyOut48[:in48Per],
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		return false
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
	return true
}
