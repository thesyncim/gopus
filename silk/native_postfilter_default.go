//go:build !gopus_extra_controls
// +build !gopus_extra_controls

package silk

const nativePostfilterEnabled = false

type nativePostfilterExtras struct{}

func (d *Decoder) fireNativePostfilterHook(_ int, _ *decoderState, _ *decoderControl, _ []int16) bool {
	return false
}
