package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
)

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for future optional
// extension paths. A nil blob clears the retained main-decoder model state.
func (d *Decoder) SetDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	models := blob.DecoderModels()
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	d.dredDNNBlob = blob
	d.dredModelLoaded = blob != nil && blob.SupportsDREDDecoder()
	if !d.dredModelLoaded {
		d.clearDREDPayloadState()
	}
}

// DNNBlobLoaded reports whether a validated model blob is retained.
func (d *Decoder) DNNBlobLoaded() bool {
	return d.dnnBlob != nil
}

// PitchDNNLoaded reports whether the retained blob contains libopus's shared
// decoder pitch model family.
func (d *Decoder) PitchDNNLoaded() bool {
	return d.pitchDNNLoaded
}

// PLCModelLoaded reports whether the retained blob contains the PLC model family.
func (d *Decoder) PLCModelLoaded() bool {
	return d.plcModelLoaded
}

// FARGANModelLoaded reports whether the retained blob contains the FARGAN model family.
func (d *Decoder) FARGANModelLoaded() bool {
	return d.farganModelLoaded
}

func makeDREDBuffers(streams int) [][]byte {
	if streams <= 0 {
		return nil
	}
	bufs := make([][]byte, streams)
	for i := range bufs {
		bufs[i] = make([]byte, internaldred.MaxDataSize)
	}
	return bufs
}

func (d *Decoder) clearDREDPayloadState() {
	for i := range d.dredCache {
		d.dredCache[i].Clear()
	}
}

func (d *Decoder) maybeCacheDREDPayload(stream int, packet []byte) {
	if !d.dredModelLoaded || d.ignoreExtensions || stream < 0 || stream >= len(d.dredData) || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok || len(payload) > len(d.dredData[stream]) {
		return
	}
	if err := d.dredCache[stream].Store(d.dredData[stream], payload, frameOffset); err != nil {
		return
	}
}

func (d *Decoder) cachedDREDMaxAvailableSamples(stream, maxDredSamples int) int {
	return d.cachedDREDResult(stream, maxDredSamples).MaxAvailableSamples()
}

func (d *Decoder) cachedDREDAvailability(stream, maxDredSamples int) internaldred.Availability {
	return d.cachedDREDResult(stream, maxDredSamples).Availability
}

func (d *Decoder) fillCachedDREDQuantizerLevels(stream int, dst []int, maxDredSamples int) int {
	return d.cachedDREDResult(stream, maxDredSamples).FillQuantizerLevels(dst)
}

func (d *Decoder) cachedDREDResult(stream, maxDredSamples int) internaldred.Result {
	if stream < 0 || stream >= len(d.dredCache) || d.dredCache[stream].Empty() || !d.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	return d.dredCache[stream].Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	return d.cachedDREDResult(stream, maxDredSamples).FeatureWindow(decodeOffsetSamples, frameSizeSamples, initFrames)
}
