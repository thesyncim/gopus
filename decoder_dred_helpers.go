package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func (d *Decoder) dredState() *decoderDREDState {
	if d == nil {
		return nil
	}
	return d.dred
}

func (d *Decoder) ensureDREDState() *decoderDREDState {
	if d == nil {
		return nil
	}
	if d.dred == nil {
		d.dred = &decoderDREDState{}
	}
	return d.dred
}

func (d *Decoder) dredPayloadState() *decoderDREDPayloadState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDPayloadState
	}
	return nil
}

func (d *Decoder) dredRecoveryState() *decoderDREDRecoveryState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDRecoveryState
	}
	return nil
}

func (d *Decoder) dredNeuralState() *decoderDREDNeuralState {
	if s := d.dredState(); s != nil {
		return s.decoderDREDNeuralState
	}
	return nil
}

func (d *Decoder) dred48kBridgeState() *decoderDRED48kBridgeState {
	if s := d.dredState(); s != nil {
		return s.decoderDRED48kBridgeState
	}
	return nil
}

func (d *Decoder) ensureDREDPayloadState() *decoderDREDPayloadState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDPayloadState == nil {
		s.decoderDREDPayloadState = &decoderDREDPayloadState{}
	}
	return s.decoderDREDPayloadState
}

func (d *Decoder) ensureDREDRecoveryState() *decoderDREDRecoveryState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDRecoveryState == nil {
		s.decoderDREDRecoveryState = &decoderDREDRecoveryState{}
	}
	return s.decoderDREDRecoveryState
}

func (d *Decoder) ensureDREDNeuralState() *decoderDREDNeuralState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDREDNeuralState == nil {
		s.decoderDREDNeuralState = &decoderDREDNeuralState{}
	}
	return s.decoderDREDNeuralState
}

func (d *Decoder) ensureDRED48kBridgeState() *decoderDRED48kBridgeState {
	s := d.ensureDREDState()
	if s == nil {
		return nil
	}
	if s.decoderDRED48kBridgeState == nil {
		s.decoderDRED48kBridgeState = &decoderDRED48kBridgeState{}
	}
	return s.decoderDRED48kBridgeState
}

func (d *Decoder) dredNeuralModelsLoaded() bool {
	n := d.dredNeuralState()
	return n != nil && (n.pitchDNNLoaded || n.plcModelLoaded || n.farganModelLoaded)
}

func (d *Decoder) dropDREDPayloadStateIfDormant() {
	if d == nil {
		return
	}
	p := d.dredPayloadState()
	if p == nil {
		return
	}
	if p.dredDNNBlob == nil && !p.dredModelLoaded {
		d.dred.decoderDREDPayloadState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) dropDREDRecoveryStateIfDormant() {
	if d == nil {
		return
	}
	r := d.dredRecoveryState()
	if r == nil {
		return
	}
	if (d.dredPayloadState() == nil || !d.dredPayloadState().dredModelLoaded) && !d.dredNeuralModelsLoaded() {
		d.dred.decoderDREDRecoveryState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) dropDREDNeuralStateIfDormant() {
	if d == nil {
		return
	}
	n := d.dredNeuralState()
	if n == nil {
		return
	}
	if !n.pitchDNNLoaded && !n.plcModelLoaded && !n.farganModelLoaded {
		d.dred.decoderDREDNeuralState = nil
	}
	d.dropDRED48kBridgeStateIfDormant()
	d.dropDREDRecoveryStateIfDormant()
	d.maybeDropDREDState()
}

func (d *Decoder) dropDRED48kBridgeStateIfDormant() {
	if d == nil {
		return
	}
	b := d.dred48kBridgeState()
	if b == nil {
		return
	}
	if !d.dredNeuralModelsLoaded() {
		d.dred.decoderDRED48kBridgeState = nil
	}
	d.maybeDropDREDState()
}

func (d *Decoder) maybeDropDREDState() {
	if d == nil || d.dred == nil {
		return
	}
	if d.dred.decoderDREDPayloadState == nil &&
		d.dred.decoderDREDRecoveryState == nil &&
		d.dred.decoderDREDNeuralState == nil &&
		d.dred.decoderDRED48kBridgeState == nil {
		d.dred = nil
	}
}

func (d *Decoder) dredNeuralConfigEligible() bool {
	if d == nil || d.channels != 1 {
		return false
	}
	return d.sampleRate == 16000 || d.sampleRate == 48000
}

// setDNNBlob mirrors the main libopus decoder OPUS_SET_DNN_BLOB surface. The
// standalone DRED decoder keeps its own model lifetime and is managed
// separately through setDREDDecoderBlob.
func (d *Decoder) setDNNBlob(blob *dnnblob.Blob) error {
	var (
		models    dnnblob.DecoderModelState
		analysis  lpcnetplc.Analysis
		predictor lpcnetplc.Predictor
		fargan    lpcnetplc.FARGAN
	)
	if blob != nil {
		models = blob.DecoderModels()
		if models.PitchDNN {
			if err := analysis.SetModel(blob); err != nil {
				return err
			}
		}
		if models.PLC {
			if err := predictor.SetModel(blob); err != nil {
				return err
			}
		}
		if models.FARGAN {
			if err := fargan.SetModel(blob); err != nil {
				return err
			}
		}
	}

	d.dnnBlob = blob
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE

	n := d.dredNeuralState()
	if !models.PitchDNN && !models.PLC && !models.FARGAN {
		if n != nil {
			n.pitchDNNLoaded = false
			n.plcModelLoaded = false
			n.farganModelLoaded = false
			n.dredAnalysis.Reset()
			n.dredPredictor.Reset()
			n.dredFARGAN.Reset()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	if !d.dredNeuralConfigEligible() {
		if n != nil {
			n.pitchDNNLoaded = false
			n.plcModelLoaded = false
			n.farganModelLoaded = false
			n.dredAnalysis.Reset()
			n.dredPredictor.Reset()
			n.dredFARGAN.Reset()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	n = d.ensureDREDNeuralState()
	n.pitchDNNLoaded = models.PitchDNN && analysis.Loaded()
	n.plcModelLoaded = models.PLC && predictor.Loaded()
	n.farganModelLoaded = models.FARGAN && fargan.Loaded()
	n.dredAnalysis = analysis
	n.dredPredictor = predictor
	n.dredFARGAN = fargan
	d.resetDRED48kNeuralBridge()
	return nil
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	if d == nil {
		return
	}

	p := d.dredPayloadState()
	if blob == nil {
		if p != nil {
			p.dredDNNBlob = nil
			p.dredModel = nil
			p.dredModelLoaded = false
			p.dredProcess = rdovae.Processor{}
			p.dredData = nil
			d.clearDREDPayloadState()
			d.resetDRED48kNeuralBridge()
			d.dropDREDPayloadStateIfDormant()
			d.dropDREDRecoveryStateIfDormant()
		}
		return
	}

	p = d.ensureDREDPayloadState()
	p.dredDNNBlob = blob
	p.dredModel = nil
	p.dredModelLoaded = false
	if blob.SupportsDREDDecoder() {
		if model, err := rdovae.LoadDecoder(blob); err == nil {
			p.dredModel = model
			p.dredModelLoaded = true
		}
	}
	if !p.dredModelLoaded {
		d.clearDREDPayloadState()
		p.dredData = nil
		p.dredProcess = rdovae.Processor{}
		d.resetDRED48kNeuralBridge()
		d.dropDREDPayloadStateIfDormant()
		d.dropDREDRecoveryStateIfDormant()
	}
}

func (d *Decoder) ensureDREDPayloadBuffer() {
	p := d.dredPayloadState()
	if p == nil || len(p.dredData) >= internaldred.MaxDataSize {
		return
	}
	p.dredData = make([]byte, internaldred.MaxDataSize)
}

func (d *Decoder) clearDREDPayloadState() {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil && r == nil {
		return
	}
	if p != nil {
		p.dredCache.Clear()
		p.dredDecoded.Clear()
	}
	if r != nil {
		r.dredPLC.FECClear()
		r.dredBlend = r.dredPLC.Blend()
		r.dredRecovery = 0
	}
	d.resetDRED48kNeuralBridge()
}

func (d *Decoder) invalidateDREDPayloadState() {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil && r == nil {
		return
	}
	if p != nil {
		p.dredCache.Invalidate()
		p.dredDecoded.Invalidate()
	}
	if r != nil {
		r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
		r.dredRecovery = 0
	}
}

func (d *Decoder) resetDRED48kNeuralBridge() {
	b := d.dred48kBridgeState()
	if b == nil {
		return
	}
	b.dredPLCFill = 0
	b.dredPLCPreemphMem = 0
	b.dredLastNeural = false
}

func (d *Decoder) dredSidecarActive() bool {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil && r == nil && !d.dredNeuralModelsLoaded() {
		return false
	}
	return (p != nil && p.dredModelLoaded) || d.dredNeuralModelsLoaded()
}

func (d *Decoder) dredNeuralConcealmentReady() bool {
	return d.ensureDREDNeuralConcealmentRuntime()
}

func (d *Decoder) dredNeuralConcealmentAvailable() bool {
	n := d.dredNeuralState()
	if n == nil {
		return false
	}
	if !d.dredNeuralConfigEligible() {
		return false
	}
	return n.pitchDNNLoaded && n.plcModelLoaded && n.farganModelLoaded
}

func (d *Decoder) ensureDREDNeuralConcealmentRuntime() bool {
	if !d.dredNeuralConcealmentAvailable() {
		return false
	}
	r := d.ensureDREDRecoveryState()
	if r == nil {
		return false
	}
	if d.sampleRate == 48000 && d.ensureDRED48kBridgeState() == nil {
		return false
	}
	return true
}

func (d *Decoder) maybeCacheDREDPayload(packet []byte) {
	p := d.dredPayloadState()
	if p == nil || !p.dredModelLoaded || d.ignoreExtensions || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok {
		return
	}
	d.ensureDREDPayloadBuffer()
	if len(payload) > len(p.dredData) {
		return
	}
	r := d.ensureDREDRecoveryState()
	if r != nil {
		r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	}
	if err := p.dredCache.Store(p.dredData, payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := p.dredDecoded.Decode(payload, frameOffset, minFeatureFrames); err != nil {
		d.invalidateDREDPayloadState()
		return
	}
	p.dredModel.DecodeAllWithProcessor(&p.dredProcess, p.dredDecoded.Features[:], p.dredDecoded.State[:], p.dredDecoded.Latents[:], p.dredDecoded.NbLatents)
}

func (d *Decoder) cachedDREDMaxAvailableSamples(maxDredSamples int) int {
	return d.cachedDREDResult(maxDredSamples).MaxAvailableSamples()
}

func (d *Decoder) cachedDREDAvailability(maxDredSamples int) internaldred.Availability {
	return d.cachedDREDResult(maxDredSamples).Availability
}

func (d *Decoder) fillCachedDREDQuantizerLevels(dst []int, maxDredSamples int) int {
	return d.cachedDREDResult(maxDredSamples).FillQuantizerLevels(dst)
}

func (d *Decoder) cachedDREDResult(maxDredSamples int) internaldred.Result {
	p := d.dredPayloadState()
	if p == nil || p.dredCache.Empty() || !p.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	return p.dredCache.Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	p := d.dredPayloadState()
	if p == nil {
		return internaldred.FeatureWindow{}
	}
	result := d.cachedDREDResult(maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &p.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	r := d.dredRecoveryState()
	if r == nil {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredBlend == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil || r == nil {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if r.dredBlend == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&r.dredPLC, d.cachedDREDResult(maxDredSamples), &p.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueActiveDREDRecovery(frameSizeSamples int) internaldred.FeatureWindow {
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if p == nil || r == nil || frameSizeSamples <= 0 {
		return internaldred.FeatureWindow{}
	}
	decodeOffsetSamples := frameSizeSamples + r.dredRecovery
	initFrames := 0
	if r.dredPLC.Blend() == 0 && r.dredRecovery == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(
		&r.dredPLC,
		d.cachedDREDResult(decodeOffsetSamples),
		&p.dredDecoded,
		decodeOffsetSamples,
		frameSizeSamples,
		initFrames,
	)
}

func (d *Decoder) shouldTrackDREDPCMHistory() bool {
	return d.dredNeuralModelsLoaded() && d.sampleRate == 16000 && d.dredNeuralConfigEligible()
}

func (d *Decoder) markDREDConcealed() {
	r := d.dredRecoveryState()
	if r == nil || !d.dredSidecarActive() {
		return
	}
	d.resetDRED48kNeuralBridge()
	r.dredBlend = 1
}

func (d *Decoder) primeDREDCELTEntryHistory(mode Mode) int {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return 0
	}
	r := d.dredRecoveryState()
	neural := d.dredNeuralState()
	if r == nil || neural == nil || r.dredPLC.Blend() != 0 || d.celtDecoder == nil {
		return 0
	}
	if mode != ModeCELT && mode != ModeHybrid {
		return 0
	}
	var samples int
	if d.sampleRate == 48000 {
		b := d.ensureDRED48kBridgeState()
		var preemphMem float32
		samples, preemphMem = d.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(neural.dredPLCUpdate[:])
		b.dredPLCPreemphMem = preemphMem
	} else {
		samples = d.celtDecoder.FillPLCUpdate16kMono(neural.dredPLCUpdate[:])
	}
	if samples == 0 {
		return 0
	}
	if got := r.dredPLC.ReplaceHistoryFromFramesFloat(neural.dredPLCUpdate[:samples]); got != samples {
		return got
	}
	neural.dredAnalysis.Reset()
	if d.sampleRate != 48000 {
		if got := neural.dredAnalysis.PrimeHistoryFramesFloat(neural.dredPLCUpdate[:samples]); got != samples {
			return got
		}
	}
	return samples
}

func (d *Decoder) prepareDRED48kNeuralEntry(frameSize int, mode Mode) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	b := d.dred48kBridgeState()
	if d == nil || r == nil || b == nil || d.sampleRate != 48000 || d.channels != 1 || (mode != ModeCELT && mode != ModeHybrid) {
		return
	}
	if p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty() {
		d.queueActiveDREDRecovery(frameSize)
	} else if !b.dredLastNeural && b.dredPLCFill == 0 && r.dredPLC.FECFillPos() == 0 && r.dredPLC.FECSkip() == 0 {
		d.prepareCachedDREDNeuralConcealment(frameSize)
	}
	if d.celtDecoder == nil || d.celtDecoder.LastPLCFrameWasNeural() {
		return
	}
	d.primeDREDCELTEntryHistory(mode)
}

func (d *Decoder) prepareCachedDREDNeuralConcealment(frameSizeSamples int) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	if r == nil || frameSizeSamples <= 0 {
		return
	}
	if p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty() {
		d.queueActiveDREDRecovery(frameSizeSamples)
		return
	}
	r.dredPLC.FECClear()
}

func (d *Decoder) applyDREDNeuralConcealment(pcm []float32, samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	p := d.dredPayloadState()
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	b := d.dred48kBridgeState()
	if r == nil || n == nil {
		return false
	}
	if len(pcm) < samplesPerChannel {
		return false
	}
	if d.sampleRate == 48000 {
		if b == nil {
			return false
		}
		if !b.dredLastNeural && b.dredPLCFill == 0 && r.dredPLC.FECFillPos() == 0 && r.dredPLC.FECSkip() == 0 {
			d.prepareCachedDREDNeuralConcealment(samplesPerChannel)
		}
		if !d.applyDREDNeuralConcealment48kMono(pcm, samplesPerChannel) {
			return false
		}
		if p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty() {
			r.dredRecovery += samplesPerChannel
		}
		return true
	}
	d.prepareCachedDREDNeuralConcealment(samplesPerChannel)
	if samplesPerChannel < lpcnetplc.FrameSize || samplesPerChannel%lpcnetplc.FrameSize != 0 {
		return false
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= samplesPerChannel; offset += lpcnetplc.FrameSize {
		frame := pcm[offset : offset+lpcnetplc.FrameSize]
		if r.dredPLC.Blend() == 0 {
			if !r.dredPLC.GenerateConcealedFrameFloatWithAnalysis(&n.dredAnalysis, &n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		} else {
			if !r.dredPLC.GenerateConcealedFrameFloat(&n.dredPredictor, &n.dredFARGAN, frame) {
				return false
			}
		}
	}
	if p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty() {
		r.dredRecovery += samplesPerChannel
	}
	return true
}

func (d *Decoder) markDREDUpdatedPCM(pcm []float32, samplesPerChannel int) {
	if !d.dredSidecarActive() {
		return
	}
	r := d.dredRecoveryState()
	if d.shouldTrackDREDPCMHistory() {
		r = d.ensureDREDRecoveryState()
	}
	if r == nil {
		return
	}
	d.resetDRED48kNeuralBridge()
	if !d.shouldTrackDREDPCMHistory() {
		r.dredPLC.MarkUpdated()
		return
	}
	if samplesPerChannel < lpcnetplc.FrameSize || len(pcm) < samplesPerChannel {
		r.dredPLC.MarkUpdated()
		return
	}
	updated := false
	for offset := 0; offset+lpcnetplc.FrameSize <= samplesPerChannel; offset += lpcnetplc.FrameSize {
		if r.dredPLC.MarkUpdatedFrameFloat(pcm[offset:offset+lpcnetplc.FrameSize]) == lpcnetplc.FrameSize {
			updated = true
		}
	}
	if !updated {
		r.dredPLC.MarkUpdated()
	}
}
