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
	if d.osceBWE == nil || !d.osceBWE.osceBWERuntime.Loaded() {
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

	native, fsKHz := d.silkDecoder.LatestNativeMono()
	if native == nil || fsKHz != 16 {
		return false
	}
	if len(native) < in16Per {
		return false
	}

	if packetStereoLocal {
		// Phase 1 wiring runs BWE on the mono SILK lowband only. libopus
		// processes each channel independently with its own BBWE state.
		// Until the stereo state plumbing is wired we keep the standard
		// resampler output for stereo packets.
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
	state.osceBWEFeatures.CalculateFeatures(
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
		state.applyIn16Int[:in16Per],
	)

	if err := state.osceBWERuntime.Process(
		state.applyIn16[:in16Per],
		state.applyOut48[:in48Per],
		state.applyFeatures[:numFrames*osceBWE.FeatureDim],
	); err != nil {
		return false
	}

	// Write BWE output to the mono channel of `out`. For mono channels==1 we
	// overwrite directly; stereo decode was bypassed above so channels==1
	// here (or packet was mono on a stereo decoder, in which case both
	// channels carry the same BWE result).
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
