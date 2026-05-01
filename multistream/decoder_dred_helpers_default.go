//go:build !gopus_dred && !gopus_unsupported_controls
// +build !gopus_dred,!gopus_unsupported_controls

package multistream

type decoderDREDState struct{}

func (d *Decoder) resetDREDRuntimeState() {}

func (d *Decoder) dredSidecarActive() bool {
	return false
}

func (d *Decoder) dredPayloadScannerActive() bool {
	return false
}

func (d *Decoder) clearDREDPayloadState() {}

func (d *Decoder) invalidateDREDPayloadState() {}

func (d *Decoder) maybeCacheDREDPayload(_ int, _ []byte) {}

func (d *Decoder) markDREDUpdated(_ int) {}

func (d *Decoder) markDREDConcealedAll() {}
