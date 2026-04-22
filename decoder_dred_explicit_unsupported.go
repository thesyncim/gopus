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
	maxDREDSamples := internaldred.MaxLatents * internaldred.LatentSpanSamples(d.sampleRate)
	return dred.cache.Parsed.ForRequest(internaldred.Request{
		MaxDREDSamples: maxDREDSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) queueExplicitDREDRecovery(dred *DRED, dredOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	if d == nil || dred == nil || !dred.Processed() || frameSizeSamples <= 0 {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if d.dredPLC.Blend() == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(
		&d.dredPLC,
		d.explicitDREDResultForDecode(dred),
		&dred.decoded,
		dredOffsetSamples,
		frameSizeSamples,
		initFrames,
	)
}

func (d *Decoder) decodeExplicitDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil || dred == nil || !dred.Processed() {
		return 0, ErrInvalidArgument
	}
	if !d.dredNeuralConcealmentReady() {
		return 0, ErrUnsupportedExtension
	}
	if frameSizeSamples <= 0 || frameSizeSamples > d.maxPacketSamples {
		return 0, ErrInvalidArgument
	}
	if frameSizeSamples < lpcnetplc.FrameSize || frameSizeSamples%lpcnetplc.FrameSize != 0 {
		return 0, ErrInvalidArgument
	}
	needed := frameSizeSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	d.primeDREDCELTEntryHistory(d.prevMode)
	d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	for offset := 0; offset+lpcnetplc.FrameSize <= frameSizeSamples; offset += lpcnetplc.FrameSize {
		if !d.dredPLC.ConcealFrameFloatWithAnalysis(&d.dredAnalysis, &d.dredPredictor, &d.dredFARGAN, pcm[offset:offset+lpcnetplc.FrameSize]) {
			return 0, ErrInvalidPacket
		}
	}
	d.applyOutputGain(pcm[:needed])
	d.lastFrameSize = frameSizeSamples
	d.lastPacketDuration = frameSizeSamples
	d.lastDataLen = 0
	return frameSizeSamples, nil
}
