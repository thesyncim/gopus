//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func (d *Decoder) explicitDREDResultForDecode(dred *DRED) internaldred.Result {
	if d == nil || dred == nil || !dred.Processed() || d.sampleRate <= 0 {
		return internaldred.Result{}
	}
	maxDREDSamples := d.maxPacketSamples
	if maxDREDSamples <= 0 {
		maxDREDSamples = internaldred.MaxLatents * internaldred.LatentSpanSamples(d.sampleRate)
	}
	return dred.cache.Parsed.ForRequest(internaldred.Request{
		MaxDREDSamples: maxDREDSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) queueExplicitDREDRecovery(dred *DRED, dredOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	r := d.dredRecoveryState()
	if d == nil || r == nil || dred == nil || !dred.Processed() || frameSizeSamples <= 0 {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredPLC.Blend() == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(
		&r.dredPLC,
		d.explicitDREDResultForDecode(dred),
		&dred.decoded,
		dredOffsetSamples,
		frameSizeSamples,
		initFrames,
	)
}

func (d *Decoder) decodeExplicitDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if d == nil || r == nil || n == nil || dred == nil || !dred.Processed() {
		return 0, ErrInvalidArgument
	}
	if !d.dredNeuralConcealmentReady() {
		return 0, ErrUnsupportedExtension
	}
	if frameSizeSamples <= 0 || frameSizeSamples > d.maxPacketSamples {
		return 0, ErrInvalidArgument
	}
	if d.sampleRate == 48000 {
		lossQuantum := d.sampleRate / 400
		if frameSizeSamples < lossQuantum || frameSizeSamples%lossQuantum != 0 {
			return 0, ErrInvalidArgument
		}
	} else if frameSizeSamples < lpcnetplc.FrameSize || frameSizeSamples%lpcnetplc.FrameSize != 0 {
		return 0, ErrInvalidArgument
	}
	needed := frameSizeSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	d.primeDREDCELTEntryHistory(d.prevMode)
	if d.sampleRate == 48000 {
		d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	}
	if d.sampleRate == 48000 {
		n, err := d.decodePLCChunksInto(pcm[:needed], frameSizeSamples, plcDecodeState{
			packetFrameSize:    d.lastFrameSize,
			mode:               d.prevMode,
			bandwidth:          d.lastBandwidth,
			packetStereo:       d.prevPacketStereo,
			useDecoderPLCState: true,
		})
		if err != nil {
			return 0, err
		}
		frameSizeSamples = n
		if !d.applyDREDNeuralConcealment48kMono(pcm[:frameSizeSamples], frameSizeSamples) {
			return 0, ErrInvalidPacket
		}
		d.applyOutputGain(pcm[:frameSizeSamples])
		d.lastFrameSize = frameSizeSamples
		d.lastPacketDuration = frameSizeSamples
		d.lastDataLen = 0
		return frameSizeSamples, nil
	}
	d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	for offset := 0; offset+lpcnetplc.FrameSize <= frameSizeSamples; offset += lpcnetplc.FrameSize {
		frame := pcm[offset : offset+lpcnetplc.FrameSize]
		if r.dredPLC.Blend() == 0 {
			if !r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame) {
				return 0, ErrInvalidPacket
			}
		} else {
			if !r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame) {
				return 0, ErrInvalidPacket
			}
		}
	}
	d.applyOutputGain(pcm[:needed])
	d.lastFrameSize = frameSizeSamples
	d.lastPacketDuration = frameSizeSamples
	d.lastDataLen = 0
	return frameSizeSamples, nil
}
