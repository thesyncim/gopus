//go:build !gopus_dred && !gopus_extra_controls

package gopus

import (
	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/silk"
)

// dredFeatureWindow mirrors the field shape of internal/dred.FeatureWindow that
// the shared concealment callers read. The default build never reaches the
// neural DRED path (extsupport.DREDRuntime is false), so the stubs below always
// return the zero value. Keeping this type local avoids importing internal/dred,
// which would otherwise link the gated DRED/PitchDNN/FARGAN neural packages into
// the default build.
type dredFeatureWindow struct {
	FeaturesPerFrame         int
	NeededFeatureFrames      int
	FeatureOffsetBase        int
	MaxFeatureIndex          int
	RecoverableFeatureFrames int
	MissingPositiveFrames    int
}

type decoderDREDState struct{}

type decoderDREDRecoveryState struct {
	dredRecovery int
}

func (d *Decoder) dredState() *decoderDREDState {
	return nil
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
	d.setOSCEModelState(models)
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

func (d *Decoder) dredPayloadScannerActive() bool {
	return false
}

func (d *Decoder) dredSidecarActive() bool {
	return false
}

func (d *Decoder) dredGoodPacketMarkerActive() bool {
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

func (d *Decoder) prepareDRED48kNeuralEntry(_ int, _ Mode, _ bool) dredFeatureWindow {
	return dredFeatureWindow{}
}

func (d *Decoder) prepareCachedDREDNeuralConcealment(_ int) dredFeatureWindow {
	return dredFeatureWindow{}
}

func (d *Decoder) applyPreparedDREDNeuralConcealment(_ []float32, _ int) bool {
	return false
}

func (d *Decoder) primeHybridDREDEntryHistory(_ int) {}

func (d *Decoder) advanceHybridDREDLowbandState(_ int, _ *silk.DeepPLCLowbandSnapshot) bool {
	return false
}

func (d *Decoder) finishActiveDREDRecovery(_ int) {}

func (d *Decoder) decodeSILKNeuralPLCInto(_ []float32, _ int, _ plcDecodeState) (int, bool, error) {
	return 0, false, nil
}

func (d *Decoder) decodeCELTNeuralPLCInto(_ []float32, _ int, _ plcDecodeState) (int, bool, error) {
	return 0, false, nil
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
