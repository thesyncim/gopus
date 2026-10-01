//go:build gopus_custom_modes && !gopus_fixed_point

package custom

func newFixedCustomEncoder(*CustomMode, int) (fixedCustomEncoder, error) {
	return nil, nil
}
