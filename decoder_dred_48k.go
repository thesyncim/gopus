package gopus

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

const (
	dred48kSincOrder    = 48
	dred48kQueueSamples = 4 * lpcnetplc.FrameSize
	dred48kChunkSamples = 960
)

// Matches libopus celt_decoder.c deep PLC/DRED sinc_filter.
var dred48kSincFilter = [...]float32{
	4.2931e-05, -0.000190293, -0.000816132, -0.000637162, 0.00141662, 0.00354764, 0.00184368, -0.00428274,
	-0.00856105, -0.0034003, 0.00930201, 0.0159616, 0.00489785, -0.0169649, -0.0259484, -0.00596856,
	0.0286551, 0.0405872, 0.00649994, -0.0509284, -0.0716655, -0.00665212, 0.134336, 0.278927,
	0.339995, 0.278927, 0.134336, -0.00665212, -0.0716655, -0.0509284, 0.00649994, 0.0405872,
	0.0286551, -0.00596856, -0.0259484, -0.0169649, 0.00489785, 0.0159616, 0.00930201, -0.0034003,
	-0.00856105, -0.00428274, 0.00184368, 0.00354764, 0.00141662, -0.000637162, -0.000816132, -0.000190293,
	4.2931e-05,
}

func (d *Decoder) applyDREDNeuralConcealment48kMonoChunk(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.sampleRate != 48000 || d.channels != 1 || len(pcm) < samplesPerChannel {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil || b == nil {
		return false
	}
	if samplesPerChannel <= 0 || samplesPerChannel > dred48kChunkSamples || samplesPerChannel%120 != 0 {
		return false
	}

	samplesNeeded16k := (samplesPerChannel + dred48kSincOrder + celt.Overlap) / 3
	if !b.dredLastNeural {
		b.dredPLCFill = 0
	}
	for b.dredPLCFill < samplesNeeded16k {
		if b.dredPLCFill+lpcnetplc.FrameSize > len(b.dredPLCPCM) {
			return false
		}
		frame := b.dredPLCPCM[b.dredPLCFill : b.dredPLCFill+lpcnetplc.FrameSize]
		if r.dredPLC.Blend() == 0 {
			if !r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		} else {
			if !r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		}
		b.dredPLCFill += lpcnetplc.FrameSize
	}

	totalSamples := samplesPerChannel + celt.Overlap
	if totalSamples > len(b.dred48kScratch) {
		return false
	}
	neural := b.dred48kScratch[:totalSamples]
	for i := 0; i < totalSamples/3; i++ {
		var sum float32
		for j := 0; j < 17; j++ {
			sum += 3 * b.dredPLCPCM[i+j] * dred48kSincFilter[3*j]
		}
		neural[3*i] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * b.dredPLCPCM[i+j+1] * dred48kSincFilter[3*j+2]
		}
		neural[3*i+1] = sum
		sum = 0
		for j := 0; j < 16; j++ {
			sum += 3 * b.dredPLCPCM[i+j+1] * dred48kSincFilter[3*j+1]
		}
		neural[3*i+2] = sum
	}

	consumed16k := samplesPerChannel / 3
	copy(b.dredPLCPCM[:], b.dredPLCPCM[consumed16k:b.dredPLCFill])
	b.dredPLCFill -= consumed16k

	preemph := float32(celt.PreemphCoef)
	for i := 0; i < samplesPerChannel; i++ {
		tmp := neural[i]
		neural[i] -= preemph * b.dredPLCPreemphMem
		b.dredPLCPreemphMem = tmp
	}
	overlapMem := b.dredPLCPreemphMem
	for i := 0; i < celt.Overlap; i++ {
		idx := samplesPerChannel + i
		tmp := neural[idx]
		neural[idx] -= preemph * overlapMem
		overlapMem = tmp
	}

	if !b.dredLastNeural {
		window := celt.GetWindowBufferF32(celt.Overlap)
		blend := min(celt.Overlap, samplesPerChannel)
		for i := 0; i < blend; i++ {
			pcm[i] = (1-window[i])*pcm[i] + window[i]*neural[i]
		}
		copy(pcm[blend:samplesPerChannel], neural[blend:samplesPerChannel])
	} else {
		copy(pcm[:samplesPerChannel], neural[:samplesPerChannel])
	}
	if d.celtDecoder != nil {
		d.celtDecoder.CommitDRED48kMonoConcealment(pcm[:samplesPerChannel], neural[samplesPerChannel:totalSamples])
	}
	b.dredLastNeural = true
	return true
}

func (d *Decoder) applyDREDNeuralConcealment48kMono(pcm []float32, samplesPerChannel int) bool {
	if d == nil || d.sampleRate != 48000 || d.channels != 1 || len(pcm) < samplesPerChannel {
		return false
	}
	if samplesPerChannel <= 0 || samplesPerChannel%120 != 0 {
		return false
	}
	for offset := 0; offset < samplesPerChannel; {
		chunk := nextPLCChunkSamples(d.sampleRate, d.prevMode, samplesPerChannel-offset)
		if chunk <= 0 || chunk > dred48kChunkSamples {
			return false
		}
		if !d.applyDREDNeuralConcealment48kMonoChunk(pcm[offset:offset+chunk], chunk) {
			return false
		}
		offset += chunk
	}
	return true
}
