//go:build gopus_custom_modes && gopus_qext

package custom

// celt/modes.c enables 96 kHz preemphasis and larger custom frames with QEXT.
const (
	customQEXT         = true
	maxCustomFrameSize = 2048
)
