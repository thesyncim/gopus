//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
	"github.com/thesyncim/gopus/silk"
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
	if d == nil {
		return false
	}
	return d.pitchDNNLoaded || d.plcModelLoaded || d.farganModelLoaded
}

func (d *Decoder) dredNeuralRuntimeLoaded() bool {
	n := d.dredNeuralState()
	return n != nil &&
		(!d.pitchDNNLoaded || (n.pitchDNNLoaded && n.dredAnalysis.Loaded())) &&
		(!d.plcModelLoaded || (n.plcModelLoaded && n.dredPredictor.Loaded())) &&
		(!d.farganModelLoaded || (n.farganModelLoaded && n.dredFARGAN.Loaded()))
}

func (d *Decoder) resetDREDNeuralRuntime() {
	n := d.dredNeuralState()
	if n == nil {
		return
	}
	n.dredRawHistoryUpdated = false
	n.pitchDNNLoaded = false
	n.plcModelLoaded = false
	n.farganModelLoaded = false
	n.dredAnalysis.Reset()
	n.dredPredictor.Reset()
	n.dredFARGAN.Reset()
}

func (d *Decoder) ensureDREDNeuralRuntimeLoaded() bool {
	if d == nil || !d.dredNeuralModelsLoaded() || d.dnnBlob == nil {
		return false
	}
	if d.dredNeuralRuntimeLoaded() {
		return true
	}
	var (
		analysis  lpcnetplc.Analysis
		predictor lpcnetplc.Predictor
		fargan    lpcnetplc.FARGAN
	)
	if d.pitchDNNLoaded {
		if err := analysis.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	if d.plcModelLoaded {
		if err := predictor.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	if d.farganModelLoaded {
		if err := fargan.SetModel(d.dnnBlob); err != nil {
			return false
		}
	}
	n := d.ensureDREDNeuralState()
	if n == nil {
		return false
	}
	n.pitchDNNLoaded = d.pitchDNNLoaded && analysis.Loaded()
	n.plcModelLoaded = d.plcModelLoaded && predictor.Loaded()
	n.farganModelLoaded = d.farganModelLoaded && fargan.Loaded()
	n.dredAnalysis = analysis
	n.dredPredictor = predictor
	n.dredFARGAN = fargan
	return true
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

func (d *Decoder) resetDREDRuntimeState() {
	if s := d.dredState(); s != nil {
		if p := s.decoderDREDPayloadState; p != nil {
			p.dredData = nil
			p.dredProcess = rdovae.Processor{}
		}
		s.decoderDREDRecoveryState = nil
		s.decoderDREDNeuralState = nil
		s.decoderDRED48kBridgeState = nil
		d.maybeDropDREDState()
	}
}

func (d *Decoder) dredNeuralConfigEligible() bool {
	if d == nil || d.channels != 1 {
		return false
	}
	return d.sampleRate == 16000 || d.sampleRate == 48000
}

// DRED decode/recovery bookkeeping follows Opus's internal 48 kHz frame-size
// domain for the exercised mono decoder path, even when the public decoder was
// created for 16 kHz output.
func (d *Decoder) dredRuntimeSampleRate() int {
	if d == nil {
		return 0
	}
	if d.sampleRate == 16000 && d.dredNeuralConfigEligible() {
		return 48000
	}
	return d.sampleRate
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
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE

	n := d.dredNeuralState()
	if !models.PitchDNN && !models.PLC && !models.FARGAN {
		if n != nil {
			d.resetDREDNeuralRuntime()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	if !d.dredNeuralConfigEligible() {
		if n != nil {
			d.resetDREDNeuralRuntime()
			d.resetDRED48kNeuralBridge()
			d.dropDREDNeuralStateIfDormant()
		}
		return nil
	}

	if n != nil {
		n.pitchDNNLoaded = models.PitchDNN && analysis.Loaded()
		n.plcModelLoaded = models.PLC && predictor.Loaded()
		n.farganModelLoaded = models.FARGAN && fargan.Loaded()
		n.dredAnalysis = analysis
		n.dredPredictor = predictor
		n.dredFARGAN = fargan
	}
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

func (d *Decoder) dredCachedPayloadActive() bool {
	p := d.dredPayloadState()
	return p != nil && p.dredModelLoaded && !d.ignoreExtensions && !p.dredCache.Empty()
}

func (d *Decoder) dredNeedsCELTFloatPath() bool {
	if d == nil || d.channels != 1 {
		return false
	}
	b := d.dred48kBridgeState()
	if b != nil && (b.dredLastNeural || b.dredPLCFill != 0 || b.dredPLCPreemphMem != 0) {
		return true
	}
	if d.celtDecoder != nil && d.celtDecoder.LastPLCFrameWasNeural() {
		return true
	}
	return false
}

func (d *Decoder) dredNeuralConcealmentReady() bool {
	return d.ensureDREDNeuralConcealmentRuntime()
}

func (d *Decoder) dredNeuralConcealmentAvailable() bool {
	if !d.dredNeuralConfigEligible() {
		return false
	}
	return d.pitchDNNLoaded && d.plcModelLoaded && d.farganModelLoaded
}

func (d *Decoder) ensureDREDNeuralConcealmentRuntime() bool {
	if !d.dredNeuralConcealmentAvailable() {
		return false
	}
	if !d.ensureDREDNeuralRuntimeLoaded() {
		return false
	}
	r := d.ensureDREDRecoveryState()
	if r == nil {
		return false
	}
	if (d.sampleRate == 48000 || d.sampleRate == 16000) && d.ensureDRED48kBridgeState() == nil {
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
	sampleRate := d.dredRuntimeSampleRate()
	if sampleRate <= 0 {
		return internaldred.Result{}
	}
	return p.dredCache.Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     sampleRate,
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

func (d *Decoder) finishActiveDREDRecovery(frameSizeSamples int) {
	r := d.dredRecoveryState()
	if r == nil || frameSizeSamples <= 0 {
		return
	}
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	r.dredRecovery += frameSizeSamples
}

func (d *Decoder) beginHybridDREDLowbandHook() (cleanup func(), used func() bool) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return func() {}, func() bool { return false }
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if d == nil || d.silkDecoder == nil || r == nil || n == nil || d.sampleRate != 48000 || d.channels != 1 {
		return func() {}, func() bool { return false }
	}
	directUsed := false
	d.silkDecoder.SetDeepPLCLossMonoHook(func(concealed []float32) (bool, int) {
		if len(concealed) < lpcnetplc.FrameSize || len(concealed)%lpcnetplc.FrameSize != 0 {
			return false, 0
		}
		if !d.generateDREDNeuralFrames16k(concealed, len(concealed)) {
			return false, 0
		}
		directUsed = true
		return true, 0
	})
	return func() {
			d.silkDecoder.SetDeepPLCLossMonoHook(nil)
		}, func() bool {
			return directUsed
		}
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
	r.dredPLC.MarkConcealed()
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
}

func (d *Decoder) updateDREDPCMHistory(frames []float32) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	if r == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		r.dredPLC.MarkUpdatedFrameFloat(frames[offset : offset+lpcnetplc.FrameSize])
	}
}

func (d *Decoder) updateDREDPCMHistoryInt16(frames []int16) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	const scale = float32(1.0 / 32768.0)
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		frame := n.dredPLCUpdate[:lpcnetplc.FrameSize]
		for i := 0; i < lpcnetplc.FrameSize; i++ {
			frame[i] = float32(frames[offset+i]) * scale
		}
		r.dredPLC.MarkUpdatedFrameFloat(frame)
		n.dredRawHistoryUpdated = true
	}
}

func (d *Decoder) primeDREDPCMHistoryInt16(frames []int16) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || len(frames) < lpcnetplc.FrameSize {
		return
	}
	const scale = float32(1.0 / 32768.0)
	for offset := 0; offset+lpcnetplc.FrameSize <= len(frames); offset += lpcnetplc.FrameSize {
		frame := n.dredPLCUpdate[:lpcnetplc.FrameSize]
		for i := 0; i < lpcnetplc.FrameSize; i++ {
			frame[i] = float32(frames[offset+i]) * scale
		}
		r.dredPLC.MarkUpdatedFrameFloat(frame)
		n.dredRawHistoryUpdated = true
	}
}

func (d *Decoder) recordDREDRawMonoGoodFrame(samples []int16) {
	if d == nil || len(samples) < lpcnetplc.FrameSize {
		return
	}
	d.primeDREDPCMHistoryInt16(samples)
}

func (d *Decoder) beginDREDRawMonoGoodFrameCapture(mode Mode) func() {
	if d == nil || d.silkDecoder == nil || d.sampleRate != 48000 || d.channels != 1 {
		return func() {}
	}
	if mode != ModeHybrid && mode != ModeSILK {
		return func() {}
	}
	p := d.dredPayloadState()
	if d.dredRecoveryState() == nil && (p == nil || !p.dredModelLoaded) {
		return func() {}
	}
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return func() {}
	}
	if n := d.dredNeuralState(); n != nil {
		n.dredRawHistoryUpdated = false
	}
	d.silkDecoder.SetRawMonoFrameHook(d.recordDREDRawMonoGoodFrame)
	return func() {
		d.silkDecoder.SetRawMonoFrameHook(nil)
	}
}

func (d *Decoder) refreshDREDHistoryFromHybridDecoder(samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	if d == nil || d.silkDecoder == nil || d.sampleRate != 48000 || d.channels != 1 || samplesPerChannel <= 0 || samplesPerChannel%3 != 0 {
		return false
	}
	n := d.dredNeuralState()
	if n == nil {
		return false
	}
	nativeSamples := samplesPerChannel / 3
	if nativeSamples < lpcnetplc.FrameSize || nativeSamples > len(n.dredPLCUpdate) || nativeSamples%lpcnetplc.FrameSize != 0 {
		return false
	}
	if got := d.silkDecoder.FillMonoOutBufTailFloat(n.dredPLCUpdate[:nativeSamples], nativeSamples); got != nativeSamples {
		return false
	}
	d.updateDREDPCMHistory(n.dredPLCUpdate[:nativeSamples])
	n.dredRawHistoryUpdated = true
	return true
}

func (d *Decoder) primeDREDCELTEntryHistory(mode Mode, primeAnalysis bool) int {
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
	b := d.ensureDRED48kBridgeState()
	var preemphMem float32
	samples, preemphMem := d.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(neural.dredPLCUpdate[:])
	if b != nil {
		b.dredPLCPreemphMem = preemphMem
	}
	if samples == 0 {
		return 0
	}
	total := 0
	for offset := 0; offset+lpcnetplc.FrameSize <= samples; offset += lpcnetplc.FrameSize {
		total += r.dredPLC.MarkUpdatedFrameFloat(neural.dredPLCUpdate[offset : offset+lpcnetplc.FrameSize])
	}
	if primeAnalysis && total > 0 {
		neural.dredAnalysis.Reset()
		if got := neural.dredAnalysis.PrimeHistoryFramesFloat(neural.dredPLCUpdate[:total]); got != total {
			return 0
		}
	}
	return total
}

func (d *Decoder) prepareDRED48kNeuralEntry(frameSize int, mode Mode, primeAnalysis bool) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	b := d.dred48kBridgeState()
	if d == nil || r == nil || b == nil || (d.sampleRate != 48000 && d.sampleRate != 16000) || d.channels != 1 || (mode != ModeCELT && mode != ModeHybrid) {
		return
	}
	if !b.dredLastNeural && b.dredPLCFill == 0 && r.dredPLC.FECFillPos() == 0 && r.dredPLC.FECSkip() == 0 {
		d.prepareCachedDREDNeuralConcealment(frameSize)
	}
	if d.celtDecoder == nil || d.celtDecoder.LastPLCFrameWasNeural() {
		return
	}
	if r.dredPLC.FECFillPos() > r.dredPLC.FECReadPos() {
		primeAnalysis = false
	}
	d.primeDREDCELTEntryHistory(mode, primeAnalysis)
}

func (d *Decoder) prepareCachedDREDNeuralConcealment(frameSizeSamples int) {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return
	}
	r := d.dredRecoveryState()
	if r == nil || frameSizeSamples <= 0 {
		return
	}
	r.dredPLC.FECClear()
}

func (d *Decoder) generateDREDNeuralFrames16k(dst []float32, samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if r == nil || n == nil || samplesPerChannel < lpcnetplc.FrameSize || samplesPerChannel%lpcnetplc.FrameSize != 0 {
		return false
	}
	if dst == nil {
		if samplesPerChannel > len(n.dredPLCUpdate) {
			return false
		}
		dst = n.dredPLCUpdate[:samplesPerChannel]
	} else if len(dst) < samplesPerChannel {
		return false
	}
	for offset := 0; offset+lpcnetplc.FrameSize <= samplesPerChannel; offset += lpcnetplc.FrameSize {
		frame := dst[offset : offset+lpcnetplc.FrameSize]
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
	return true
}

func (d *Decoder) applyDREDNeuralConcealment(pcm []float32, samplesPerChannel int) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	r := d.dredRecoveryState()
	b := d.dred48kBridgeState()
	if r == nil || d.dredNeuralState() == nil {
		return false
	}
	if len(pcm) < samplesPerChannel {
		return false
	}
	if b != nil && (d.sampleRate == 48000 || d.sampleRate == 16000) {
		cachedDRED := d.dredCachedPayloadActive()
		d.prepareDRED48kNeuralEntry(samplesPerChannel, d.prevMode, false)
		if !d.applyPLCNeuralConcealment48kMono(pcm, samplesPerChannel) {
			return false
		}
		if cachedDRED {
			d.finishActiveDREDRecovery(samplesPerChannel)
		}
		return true
	}
	return false
}

func (d *Decoder) advanceHybridDREDLowbandState(frameSizeSamples int, lowbandSnapshot *silk.DeepPLCLowbandSnapshot) bool {
	if !d.ensureDREDNeuralConcealmentRuntime() {
		return false
	}
	r := d.dredRecoveryState()
	n := d.dredNeuralState()
	if d == nil || d.silkDecoder == nil || r == nil || n == nil || d.sampleRate != 48000 || d.channels != 1 || frameSizeSamples <= 0 || frameSizeSamples%3 != 0 {
		return false
	}
	nativeSamples := frameSizeSamples / 3
	if nativeSamples < lpcnetplc.FrameSize || nativeSamples > len(n.dredPLCUpdate) || nativeSamples%lpcnetplc.FrameSize != 0 {
		return false
	}
	if !d.generateDREDNeuralFrames16k(n.dredPLCUpdate[:nativeSamples], nativeSamples) {
		return false
	}
	if lowbandSnapshot != nil {
		d.silkDecoder.RestoreDeepPLCLowbandMono(lowbandSnapshot)
	}
	rendered := n.dredPLCRender[:nativeSamples]
	if d.silkDecoder.ApplyDeepPLCLossMono(n.dredPLCUpdate[:nativeSamples], rendered, n.dredFARGAN.LastPeriod()) != nativeSamples {
		return false
	}
	if lowbandSnapshot != nil {
		d.silkDecoder.AdvanceDeepPLCLowbandMono(rendered)
	}
	r.dredBlend = max(r.dredBlend, r.dredPLC.Blend())
	r.dredRecovery += frameSizeSamples
	return true
}

func (d *Decoder) markDREDUpdatedPCM(pcm []float32, samplesPerChannel int, mode Mode) {
	if !d.dredSidecarActive() {
		return
	}
	if b := d.dred48kBridgeState(); b != nil {
		b.dredLastNeural = false
	}
	r := d.dredRecoveryState()
	if !d.dredNeuralConcealmentAvailable() {
		if r != nil {
			r.dredPLC.ClearBlend()
		}
		return
	}
	if r == nil {
		return
	}
	r.dredPLC.ClearBlend()
	if mode == ModeSILK && d.sampleRate == 16000 && d.channels == 1 && samplesPerChannel >= lpcnetplc.FrameSize && samplesPerChannel%lpcnetplc.FrameSize == 0 && len(pcm) >= samplesPerChannel {
		d.updateDREDPCMHistory(pcm[:samplesPerChannel])
	}
}
