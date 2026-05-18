//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

// DebugDREDRuntimeForTesting returns the encoder's active DRED runtime, or
// nil when DRED is not armed. It is meant only for diagnostic probes that
// inspect retained latent/state buffers; do not use it in production paths.
func (e *Encoder) DebugDREDRuntimeForTesting() *DREDRuntimeView {
	if e.dred == nil || e.dred.runtime == nil {
		return nil
	}
	return &DREDRuntimeView{runtime: e.dred.runtime}
}

// DREDRuntimeView is a read-only diagnostic wrapper around the encoder's DRED
// runtime state.
type DREDRuntimeView struct {
	runtime *dredEncoderRuntime
}

// LatentsBufferForTesting returns the raw latents FIFO. Position 0 is the
// newest emitted DFrame latent. Length is `MaxFrames * LatentDim`.
func (v *DREDRuntimeView) LatentsBufferForTesting() []float32 {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.latentsBuffer[:]
}

// LatentsFillForTesting returns the number of valid latents currently stored.
func (v *DREDRuntimeView) LatentsFillForTesting() int {
	if v == nil || v.runtime == nil {
		return 0
	}
	return v.runtime.latentsFill
}

// ActivityForTesting returns the activity memory window.
func (v *DREDRuntimeView) ActivityForTesting() []byte {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.activity[:]
}

// DREDOffsetForTesting returns the libopus-shaped DRED offset in 2.5 ms units.
func (v *DREDRuntimeView) DREDOffsetForTesting() int {
	if v == nil || v.runtime == nil {
		return 0
	}
	return v.runtime.dredOffset
}

// LatentOffsetForTesting returns the libopus-shaped latent offset.
func (v *DREDRuntimeView) LatentOffsetForTesting() int {
	if v == nil || v.runtime == nil {
		return 0
	}
	return v.runtime.latentOffset
}

// LastExtraDREDOffsetForTesting returns the carryover extra DRED offset.
func (v *DREDRuntimeView) LastExtraDREDOffsetForTesting() int {
	if v == nil || v.runtime == nil {
		return 0
	}
	return v.runtime.lastExtraDREDOffset
}

// StateBufferForTesting returns the state FIFO.
func (v *DREDRuntimeView) StateBufferForTesting() []float32 {
	if v == nil || v.runtime == nil {
		return nil
	}
	return v.runtime.stateBuffer[:]
}
