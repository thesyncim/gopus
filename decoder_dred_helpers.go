package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

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
	d.pitchDNNLoaded = models.PitchDNN && analysis.Loaded()
	d.plcModelLoaded = models.PLC && predictor.Loaded()
	d.farganModelLoaded = models.FARGAN && fargan.Loaded()
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
	d.dredAnalysis = analysis
	d.dredPredictor = predictor
	d.dredFARGAN = fargan
	return nil
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	d.dredDNNBlob = blob
	d.dredModel = nil
	d.dredModelLoaded = false
	if blob != nil && blob.SupportsDREDDecoder() {
		if model, err := rdovae.LoadDecoder(blob); err == nil {
			d.dredModel = model
			d.dredModelLoaded = true
		}
	}
	if !d.dredModelLoaded {
		d.clearDREDPayloadState()
		d.dredProcess = rdovae.Processor{}
		d.dredPLC.Reset()
	}
}

func (d *Decoder) clearDREDPayloadState() {
	d.dredCache.Clear()
	d.dredDecoded.Clear()
	d.dredPLC.FECClear()
	d.dredBlend = d.dredPLC.Blend()
}

func (d *Decoder) maybeCacheDREDPayload(packet []byte) {
	if !d.dredModelLoaded || d.ignoreExtensions || len(packet) == 0 {
		return
	}
	d.dredBlend = d.dredPLC.Blend()
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok || len(payload) > len(d.dredData) {
		return
	}
	if err := d.dredCache.Store(d.dredData, payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := d.dredDecoded.Decode(payload, frameOffset, minFeatureFrames); err != nil {
		d.clearDREDPayloadState()
		return
	}
	d.dredModel.DecodeAllWithProcessor(&d.dredProcess, d.dredDecoded.Features[:], d.dredDecoded.State[:], d.dredDecoded.Latents[:], d.dredDecoded.NbLatents)
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
	if d.dredCache.Empty() || !d.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	return d.dredCache.Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	result := d.cachedDREDResult(maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &d.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	initFrames := 0
	if d.dredBlend == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	initFrames := 0
	if d.dredBlend == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&d.dredPLC, d.cachedDREDResult(maxDredSamples), &d.dredDecoded, decodeOffsetSamples, frameSizeSamples, initFrames)
}
