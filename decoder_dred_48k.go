//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import "github.com/thesyncim/gopus/internal/lpcnetplc"

func (d *Decoder) applyDREDNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.channels != 1 || len(pcm) < samplesPerChannel {
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
	if r.dredPLC.FECFillPos() > r.dredPLC.FECReadPos() {
		return d.celtDecoder.ConcealDRED48kMonoToFloat32(
			pcm[:samplesPerChannel],
			samplesPerChannel,
			&b.dredLastNeural,
			b.dredPLCPCM[:],
			&b.dredPLCFill,
			&b.dredPLCPreemphMem,
			generate,
		)
	}
	return d.celtDecoder.ConcealPLCNeural48kMonoToFloat32(
		pcm[:samplesPerChannel],
		samplesPerChannel,
		&b.dredLastNeural,
		b.dredPLCPCM[:],
		&b.dredPLCFill,
		&b.dredPLCPreemphMem,
		generate,
	)
}

func (d *Decoder) applyPLCNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.channels != 1 || len(pcm) < samplesPerChannel {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil || b == nil || d.celtDecoder == nil {
		return false
	}

	return d.celtDecoder.ConcealPLCNeural48kMonoToFloat32(
		pcm[:samplesPerChannel],
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
