//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	osceLACE "github.com/thesyncim/gopus/internal/osce/lace"
)

// decoderOSCELACEState carries decoder-side OSCE LACE/NoLACE runtime
// bookkeeping under the explicit quarantine build. The `osceLACEModel`
// field follows the same pattern as the OSCE BWE binding: it is non-nil
// once `SetDNNBlob` has successfully bound an OSCE LACE-capable weights
// blob.
//
// libopus keeps a single shared OSCEModel inside `OpusDecoder` (see
// `dnn/osce.c`: `osce_init`) carrying both LACE and NoLACE postfilter
// weights; the per-channel postfilter state (LACEState / NoLACEState)
// lives in the silk decoder structs. Phase 1 wires the typed model
// pointer and per-channel scratch buffers; the per-channel runtime
// state machine (LACEState / NoLACEState ring buffers) arrives with the
// Phase 2 forward pass.
//
// Both LACE and NoLACE operate on a 20 ms @ 16 kHz int16 frame (320
// samples) in libopus; the scratch buffers below are sized accordingly.
// Stereo decode runs the forward pass sequentially on each channel,
// re-using the same scratch arena.
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
	// invocation libopus supports. The Phase 1 stub leaves these zeroed
	// because the upstream feature pipeline (`osce_calculate_features`)
	// has not been ported yet.
	applyFeatures [osceLACESubframesPerFrame * osceLACEFeatureDim]float32
	applyNumBits  [2]float32
	applyPeriods  [osceLACESubframesPerFrame]int

	// prevLACEActive mirrors libopus DecControl.prev_osce_extended_mode
	// for the LACE/NoLACE bit. The Phase 1 wiring tracks the flag so a
	// future cross-fade helper (analogous to osceBWECrossFade10ms) has the
	// state it needs to decide whether to fade between the postfilter and
	// raw SILK output. The current stub does not run the cross-fade.
	prevLACEActive bool

}
