//go:build !gopus_dred && !gopus_unsupported_controls
// +build !gopus_dred,!gopus_unsupported_controls

package gopus

import "github.com/thesyncim/gopus/internal/dnnblob"

type decoderDREDState struct{}

type decoderDREDRecoveryState struct {
	dredRecovery int
}

func (d *Decoder) dredState() *decoderDREDState {
	if d == nil {
		return nil
	}
	return d.dred
}

func (d *Decoder) dredRecoveryState() *decoderDREDRecoveryState {
	return nil
}

func (d *Decoder) setDNNBlob(blob *dnnblob.Blob) error {
	var models dnnblob.DecoderModelState
	if blob != nil {
		models = blob.DecoderModels()
	}
	d.dnnBlob = blob
	d.pitchDNNLoaded = models.PitchDNN
	d.plcModelLoaded = models.PLC
	d.farganModelLoaded = models.FARGAN
	d.osceModelsLoaded = models.OSCE
	d.osceBWEModelLoaded = models.OSCEBWE
	return nil
}

func (d *Decoder) dredNeuralModelsLoaded() bool {
	return d != nil && (d.pitchDNNLoaded || d.plcModelLoaded || d.farganModelLoaded)
}

func (d *Decoder) dredDecodeSidecarPossible() bool {
	return false
}

func (d *Decoder) dredNeuralRuntimeLoaded() bool {
	return false
}

func (d *Decoder) dredNeuralConcealmentReady() bool {
	return false
}

func (d *Decoder) dredNeuralConcealmentAvailable() bool {
	return false
}

func (d *Decoder) dredCachedPayloadActive() bool {
	return false
}

func (d *Decoder) dredSidecarActive() bool {
	return false
}

func (d *Decoder) dredNeedsCELTFloatPath() bool {
	return false
}

func (d *Decoder) clearDREDPayloadState() {}

func (d *Decoder) invalidateDREDPayloadState() {}

func (d *Decoder) resetDRED48kNeuralBridge() {}

func (d *Decoder) resetDREDRuntimeState() {}

func (d *Decoder) maybeDropDREDState() {}

func (d *Decoder) applyDREDNeuralConcealment(_ []float32, _ int) bool {
	return false
}

func (d *Decoder) beginHybridDREDLowbandHook() (cleanup func(), used func() bool) {
	return func() {}, func() bool { return false }
}

func (d *Decoder) beginDREDRawMonoGoodFrameCapture(_ Mode) func() {
	return func() {}
}

func (d *Decoder) maybeCacheDREDPayload(_ []byte) {}

func (d *Decoder) markDREDConcealed() {}

func (d *Decoder) markDREDUpdatedPCM(_ []float32, _ int, _ Mode) {}
