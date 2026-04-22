package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

// SetDNNBlob retains a validated USE_WEIGHTS_FILE blob for future optional
// extension paths. A nil blob clears the retained main-decoder model state.
func (d *Decoder) SetDNNBlob(blob *dnnblob.Blob) {
	d.dnnBlob = blob
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}
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
		clear(d.dredProcesses)
		for i := range d.dredPLC {
			d.dredPLC[i].Reset()
		}
		d.releaseDREDSidecar()
	}
}

func (d *Decoder) ensureDREDSidecar() {
	if d == nil || len(d.dredCache) != 0 {
		return
	}
	streams := len(d.decoders)
	if streams <= 0 {
		return
	}
	d.dredDecoded = make([]internaldred.Decoded, streams)
	d.dredProcesses = make([]rdovae.Processor, streams)
	d.dredPLC = make([]lpcnetplc.State, streams)
	d.dredBlend = make([]int, streams)
	d.dredData = makeDREDBuffers(streams)
	d.dredCache = make([]internaldred.Cache, streams)
}

func (d *Decoder) releaseDREDSidecar() {
	if d == nil {
		return
	}
	d.dredDecoded = nil
	d.dredProcesses = nil
	d.dredPLC = nil
	d.dredBlend = nil
	d.dredData = nil
	d.dredCache = nil
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

func (d *Decoder) dredSidecarActive() bool {
	if d == nil {
		return false
	}
	// Multistream has standalone DRED caching today, but no per-stream neural
	// concealment consumer yet, so keep the sidecar dormant until we actually
	// cache a payload.
	return len(d.dredCache) != 0
}

func (d *Decoder) clearDREDPayloadState() {
	if !d.dredSidecarActive() {
		return
	}
	for i := range d.dredCache {
		d.dredCache[i].Clear()
		d.dredDecoded[i].Clear()
		d.dredPLC[i].FECClear()
		d.dredBlend[i] = d.dredPLC[i].Blend()
	}
}

func (d *Decoder) invalidateDREDPayloadState() {
	if !d.dredSidecarActive() {
		return
	}
	for i := range d.dredCache {
		d.dredCache[i].Invalidate()
		d.dredDecoded[i].Invalidate()
		d.dredBlend[i] = d.dredPLC[i].Blend()
	}
}

func (d *Decoder) maybeCacheDREDPayload(stream int, packet []byte) {
	if !d.dredModelLoaded || d.ignoreExtensions || stream < 0 || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok {
		return
	}
	d.ensureDREDSidecar()
	if stream >= len(d.dredData) || len(payload) > len(d.dredData[stream]) {
		return
	}
	d.dredBlend[stream] = d.dredPLC[stream].Blend()
	if err := d.dredCache[stream].Store(d.dredData[stream], payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := d.dredDecoded[stream].Decode(payload, frameOffset, minFeatureFrames); err != nil {
		d.dredCache[stream].Invalidate()
		d.dredDecoded[stream].Invalidate()
		d.dredPLC[stream].FECClear()
		return
	}
	d.dredModel.DecodeAllWithProcessor(&d.dredProcesses[stream], d.dredDecoded[stream].Features[:], d.dredDecoded[stream].State[:], d.dredDecoded[stream].Latents[:], d.dredDecoded[stream].NbLatents)
}

func (d *Decoder) markDREDUpdated(stream int) {
	if !d.dredSidecarActive() || stream < 0 || stream >= len(d.dredPLC) {
		return
	}
	d.dredPLC[stream].MarkUpdated()
}

func (d *Decoder) markDREDConcealedAll() {
	if !d.dredSidecarActive() {
		return
	}
	for i := range d.dredPLC {
		d.dredPLC[i].MarkConcealed()
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
	if stream < 0 || stream >= len(d.dredDecoded) {
		return internaldred.FeatureWindow{}
	}
	result := d.cachedDREDResult(stream, maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &d.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	if stream < 0 || stream >= len(d.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if d.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	if stream < 0 || stream >= len(d.dredDecoded) || stream >= len(d.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if d.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&d.dredPLC[stream], d.cachedDREDResult(stream, maxDredSamples), &d.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}
