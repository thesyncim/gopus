//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/silk"
)

func (d *Decoder) explicitDREDResultForDecode(dred *DRED) internaldred.Result {
	sampleRate := d.dredRuntimeSampleRate()
	if d == nil || dred == nil || !dred.Processed() || sampleRate <= 0 {
		return internaldred.Result{}
	}
	maxDREDSamples := d.maxPacketSamples
	if maxDREDSamples <= 0 {
		maxDREDSamples = internaldred.MaxLatents * internaldred.LatentSpanSamples(sampleRate)
	}
	return dred.cache.Parsed.ForRequest(internaldred.Request{
		MaxDREDSamples: maxDREDSamples,
		SampleRate:     sampleRate,
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
	if d == nil || dred == nil || !dred.Processed() {
		return 0, ErrInvalidArgument
	}
	if !d.dredNeuralConcealmentReady() {
		return 0, ErrUnsupportedExtension
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil {
		return 0, ErrInvalidArgument
	}
	if frameSizeSamples <= 0 || frameSizeSamples > d.maxPacketSamples {
		return 0, ErrInvalidArgument
	}
	lossQuantum := d.sampleRate / 400
	if lossQuantum <= 0 || frameSizeSamples < lossQuantum || frameSizeSamples%lossQuantum != 0 {
		return 0, ErrInvalidArgument
	}
	needed := frameSizeSamples * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	primeAnalysis := d.sampleRate == 16000
	if d.sampleRate == 48000 {
		if d.prevMode == ModeHybrid {
			return d.decodeExplicitHybridDREDFloat(dred, dredOffsetSamples, pcm[:needed], frameSizeSamples)
		}
		d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
		d.prepareDRED48kNeuralEntry(frameSizeSamples, d.prevMode, primeAnalysis)
		if !d.applyDREDNeuralConcealment48kMono(pcm[:needed], frameSizeSamples) {
			return 0, ErrInvalidPacket
		}
		d.applyOutputGain(pcm[:needed])
		d.lastFrameSize = frameSizeSamples
		d.lastPacketDuration = frameSizeSamples
		d.lastDataLen = 0
		return frameSizeSamples, nil
	}
	d.prepareDRED48kNeuralEntry(frameSizeSamples, d.prevMode, primeAnalysis)
	if !d.applyDREDNeuralConcealment48kMono(pcm[:needed], frameSizeSamples) {
		return 0, ErrInvalidPacket
	}
	d.applyOutputGain(pcm[:needed])
	d.lastFrameSize = frameSizeSamples
	d.lastPacketDuration = frameSizeSamples
	d.lastDataLen = 0
	return frameSizeSamples, nil
}

func (d *Decoder) decodeExplicitHybridDREDFloat(dred *DRED, dredOffsetSamples int, pcm []float32, frameSizeSamples int) (int, error) {
	if d == nil {
		return 0, ErrInvalidArgument
	}
	queued := d.queueExplicitDREDRecovery(dred, dredOffsetSamples, frameSizeSamples)
	var lowbandSnapshot *silk.DeepPLCLowbandSnapshot
	cleanupHook := func() {}
	usedHook := func() bool { return false }
	if d.dredNeuralConcealmentAvailable() && d.silkDecoder != nil {
		lowbandSnapshot = d.silkDecoder.SnapshotDeepPLCLowbandMono()
		cleanupHook, usedHook = d.beginHybridDREDLowbandHook()
	}
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(pcm, frameSizeSamples, plcDecodeState{
		packetFrameSize:    frameSizeSamples,
		mode:               d.prevMode,
		bandwidth:          d.lastBandwidth,
		packetStereo:       d.prevPacketStereo,
		useDecoderPLCState: true,
	})
	if err != nil {
		return 0, err
	}
	if usedHook() {
		d.finishActiveDREDRecovery(n)
	} else if queued.NeededFeatureFrames > 0 || d.dredRecoveryState() != nil {
		d.advanceHybridDREDLowbandState(n, lowbandSnapshot)
	}
	d.applyOutputGain(pcm[:n*d.channels])
	d.lastFrameSize = n
	d.lastPacketDuration = n
	d.lastDataLen = 0
	return n, nil
}
