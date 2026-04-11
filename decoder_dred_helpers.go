package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

func (d *Decoder) setDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	models := blob.DecoderModels()
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.dredModelLoaded = models.DRED
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
	if !d.dredModelLoaded {
		d.clearDREDPayloadState()
	}
}

func (d *Decoder) clearDREDPayloadState() {
	d.dredCache.Clear()
}

func (d *Decoder) maybeCacheDREDPayload(packet []byte) {
	if !d.dredModelLoaded || d.ignoreExtensions || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok || len(payload) < internaldred.MinBytes || len(payload) > len(d.dredData) {
		return
	}
	if err := d.dredCache.Store(d.dredData, payload, frameOffset); err != nil {
		return
	}
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
	return d.cachedDREDResult(maxDredSamples).FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
}
