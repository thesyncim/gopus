//go:build !gopus_dred && !gopus_extra_controls
// +build !gopus_dred,!gopus_extra_controls

package silk

const (
	dredHooksEnabled            = false
	nativeLowbandCaptureEnabled = false
)

type dredHookState struct{}

func (d *Decoder) SetRawMonoFrameHook(_ RawMonoFrameHook) {}

func (d *Decoder) SetDeepPLCLossMonoHook(_ DeepPLCLossMonoHook) {}

func (d *Decoder) fireRawMonoFrameHook(_ int, _ *decoderState, _ []int16) {}

func (d *Decoder) hasDeepPLCLossMonoHook() bool {
	return false
}

func (d *Decoder) fireDeepPLCLossMonoHook(_ []float32) (bool, int) {
	return false, 0
}
