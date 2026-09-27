//go:build gopus_qext && !gopus_fixed_point

package encoder

func (e *Encoder) encodeNativeHD96kFixed(_ []float32, _ int, _ []byte) (int, bool, error) {
	return 0, false, nil
}
