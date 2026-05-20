//go:build gopus_dred || gopus_extra_controls
// +build gopus_dred gopus_extra_controls

package gopus

import "github.com/thesyncim/gopus/internal/lpcnetplc"

// applyDREDNeuralConcealment48kMono drives one 48 kHz neural CELT
// concealment frame. libopus is fundamentally mono DRED (single
// LPCNetPLCState in opus_decoder.c; mono-downmix on entry, mono-duplicate on
// exit). For stereo decoders we run the same mono pipeline and rely on the
// CELT wrapper to mirror channel-0 state into channel-1 and to interleave the
// mono PCM across both output channels (celt_decoder.c:1066-1067).
func (d *Decoder) applyDREDNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.channels < 1 || d.channels > 2 || samplesPerChannel <= 0 {
		return false
	}
	if len(pcm) < samplesPerChannel*d.channels {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil || b == nil || d.celtDecoder == nil {
		return false
	}

	generate := func(frame []float32) bool {
		if len(frame) < lpcnetplc.FrameSize {
			return false
		}
		if r.dredPLC.Blend() == 0 {
			return r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
		}
		return r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
	}
	// Mono-downmix-in / mono-duplicate-out: the stereo path runs the mono
	// CELT neural concealment helper against the channel-0 slot of the CELT
	// state and mirrors the channel-0 result into channel-1 for both the
	// retained CELT state and the interleaved output PCM.
	if r.dredPLC.FECFillPos() > r.dredPLC.FECReadPos() {
		return d.celtDecoder.ConcealDRED48kToFloat32(
			pcm[:samplesPerChannel*d.channels],
			samplesPerChannel,
			&b.dredLastNeural,
			b.dredPLCPCM[:],
			&b.dredPLCFill,
			&b.dredPLCPreemphMem,
			generate,
		)
	}
	return d.celtDecoder.ConcealPLCNeural48kToFloat32(
		pcm[:samplesPerChannel*d.channels],
		samplesPerChannel,
		&b.dredLastNeural,
		b.dredPLCPCM[:],
		&b.dredPLCFill,
		&b.dredPLCPreemphMem,
		generate,
	)
}

// applyPLCNeuralConcealment48kMono drives one 48 kHz neural PLC concealment
// frame. The naming preserves the mono-internal contract; stereo decoders
// reach this through the same mono-downmix-in / mono-duplicate-out shape
// described on applyDREDNeuralConcealment48kMono.
func (d *Decoder) applyPLCNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.channels < 1 || d.channels > 2 || samplesPerChannel <= 0 {
		return false
	}
	if len(pcm) < samplesPerChannel*d.channels {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil || b == nil || d.celtDecoder == nil {
		return false
	}

	return d.celtDecoder.ConcealPLCNeural48kToFloat32(
		pcm[:samplesPerChannel*d.channels],
		samplesPerChannel,
		&b.dredLastNeural,
		b.dredPLCPCM[:],
		&b.dredPLCFill,
		&b.dredPLCPreemphMem,
		func(frame []float32) bool {
			if len(frame) < lpcnetplc.FrameSize {
				return false
			}
			if r.dredPLC.Blend() == 0 {
				return r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
			}
			return r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame[:lpcnetplc.FrameSize])
		},
	)
}
