//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package multistream

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

type decoderDREDState struct {
	dredDNNBlob     *dnnblob.Blob
	dredModel       *rdovae.Decoder
	dredModelLoaded bool
	dredData        [][]byte
	dredCache       []internaldred.Cache
	dredDecoded     []internaldred.Decoded
	dredProcesses   []rdovae.Processor
	dredPLC         []lpcnetplc.State
	dredBlend       []int
}

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

func (d *Decoder) maybeDropDREDState() {
	if d == nil || d.dred == nil {
		return
	}
	s := d.dred
	if s.dredDNNBlob == nil && !s.dredModelLoaded && len(s.dredCache) == 0 {
		d.dred = nil
	}
}

// setDREDDecoderBlob mirrors the standalone libopus OpusDREDDecoder
// OPUS_SET_DNN_BLOB path.
func (d *Decoder) setDREDDecoderBlob(blob *dnnblob.Blob) {
	s := d.ensureDREDState()
	if s == nil {
		return
	}
	s.dredDNNBlob = blob
	s.dredModel = nil
	s.dredModelLoaded = false
	if blob != nil && blob.SupportsDREDDecoder() {
		if model, err := rdovae.LoadDecoder(blob); err == nil {
			s.dredModel = model
			s.dredModelLoaded = true
		}
	}
	if !s.dredModelLoaded {
		d.clearDREDPayloadState()
		clear(s.dredProcesses)
		for i := range s.dredPLC {
			s.dredPLC[i].Reset()
		}
		d.releaseDREDSidecar()
		d.maybeDropDREDState()
	}
}

func (d *Decoder) ensureDREDSidecar() {
	s := d.ensureDREDState()
	if s == nil || len(s.dredCache) != 0 {
		return
	}
	streams := len(d.decoders)
	if streams <= 0 {
		return
	}
	s.dredDecoded = make([]internaldred.Decoded, streams)
	s.dredProcesses = make([]rdovae.Processor, streams)
	s.dredPLC = make([]lpcnetplc.State, streams)
	s.dredBlend = make([]int, streams)
	s.dredData = makeDREDBuffers(streams)
	s.dredCache = make([]internaldred.Cache, streams)
}

func (d *Decoder) releaseDREDSidecar() {
	s := d.dredState()
	if s == nil {
		return
	}
	s.dredDecoded = nil
	s.dredProcesses = nil
	s.dredPLC = nil
	s.dredBlend = nil
	s.dredData = nil
	s.dredCache = nil
}

func (d *Decoder) resetDREDRuntimeState() {
	s := d.dredState()
	if s == nil {
		return
	}
	for i := range s.dredPLC {
		s.dredPLC[i].Reset()
	}
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
	s := d.dredState()
	if s == nil {
		return false
	}
	// Multistream has standalone DRED caching today, but no per-stream neural
	// concealment consumer yet, so keep the sidecar dormant until we actually
	// cache a payload.
	return len(s.dredCache) != 0
}

func (d *Decoder) clearDREDPayloadState() {
	s := d.dredState()
	if s == nil || len(s.dredCache) == 0 {
		return
	}
	for i := range s.dredCache {
		s.dredCache[i].Clear()
		s.dredDecoded[i].Clear()
		s.dredPLC[i].FECClear()
		s.dredBlend[i] = s.dredPLC[i].Blend()
	}
}

func (d *Decoder) invalidateDREDPayloadState() {
	s := d.dredState()
	if s == nil || len(s.dredCache) == 0 {
		return
	}
	for i := range s.dredCache {
		s.dredCache[i].Invalidate()
		s.dredDecoded[i].Invalidate()
		s.dredBlend[i] = s.dredPLC[i].Blend()
	}
}

func (d *Decoder) maybeCacheDREDPayload(stream int, packet []byte) {
	s := d.dredState()
	if s == nil || !s.dredModelLoaded || d.ignoreExtensions || stream < 0 || len(packet) == 0 {
		return
	}
	payload, frameOffset, ok, err := findDREDPayload(packet)
	if err != nil || !ok {
		return
	}
	d.ensureDREDSidecar()
	s = d.dredState()
	if s == nil || stream >= len(s.dredData) || len(payload) > len(s.dredData[stream]) {
		return
	}
	s.dredBlend[stream] = s.dredPLC[stream].Blend()
	if err := s.dredCache[stream].Store(s.dredData[stream], payload, frameOffset); err != nil {
		return
	}
	minFeatureFrames := 2 * internaldred.NumRedundancyFrames
	if _, err := s.dredDecoded[stream].Decode(payload, frameOffset, minFeatureFrames); err != nil {
		s.dredCache[stream].Invalidate()
		s.dredDecoded[stream].Invalidate()
		s.dredPLC[stream].FECClear()
		return
	}
	s.dredModel.DecodeAllWithProcessor(&s.dredProcesses[stream], s.dredDecoded[stream].Features[:], s.dredDecoded[stream].State[:], s.dredDecoded[stream].Latents[:], s.dredDecoded[stream].NbLatents)
}

func (d *Decoder) markDREDUpdated(stream int) {
	s := d.dredState()
	if s == nil || len(s.dredPLC) == 0 || stream < 0 || stream >= len(s.dredPLC) {
		return
	}
	s.dredPLC[stream].MarkUpdated()
}

func (d *Decoder) markDREDConcealedAll() {
	s := d.dredState()
	if s == nil || len(s.dredPLC) == 0 {
		return
	}
	for i := range s.dredPLC {
		s.dredPLC[i].MarkConcealed()
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
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredCache) || s.dredCache[stream].Empty() || !s.dredModelLoaded || d.ignoreExtensions {
		return internaldred.Result{}
	}
	return s.dredCache[stream].Result(internaldred.Request{
		MaxDREDSamples: maxDredSamples,
		SampleRate:     d.sampleRate,
	})
}

func (d *Decoder) cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredDecoded) {
		return internaldred.FeatureWindow{}
	}
	result := d.cachedDREDResult(stream, maxDredSamples)
	return internaldred.ProcessedFeatureWindow(result, &s.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) cachedDREDRecoveryWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if s.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return d.cachedDREDFeatureWindow(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples, initFrames)
}

func (d *Decoder) queueCachedDREDRecovery(stream, maxDredSamples, decodeOffsetSamples, frameSizeSamples int) internaldred.FeatureWindow {
	s := d.dredState()
	if s == nil || stream < 0 || stream >= len(s.dredDecoded) || stream >= len(s.dredPLC) {
		return internaldred.FeatureWindow{}
	}
	initFrames := 0
	if s.dredBlend[stream] == 0 {
		initFrames = 2
	}
	return internaldred.QueueProcessedFeaturesWithInitFrames(&s.dredPLC[stream], d.cachedDREDResult(stream, maxDredSamples), &s.dredDecoded[stream], decodeOffsetSamples, frameSizeSamples, initFrames)
}
