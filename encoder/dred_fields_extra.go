//go:build gopus_dred || gopus_extra_controls

package encoder

type encoderDREDFields struct {
	// dred owns optional DRED encoder controls/runtime.
	dred *dredEncoderExtras
}
