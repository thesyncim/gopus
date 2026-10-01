//go:build gopus_custom_modes && !gopus_fixed_point

package custom

func newFixedCustomDecoder(*CustomMode, int) (fixedCustomDecoder, error) {
	return nil, nil
}
