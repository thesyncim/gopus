//go:build gopus_custom_modes && !gopus_qext

package custom

import "github.com/thesyncim/gopus/internal/celt"

func customSignallingUsesOpusTOC(mode *CustomMode) bool {
	return mode.Fs == 48000 && mode.ShortMdctSize == 120
}

func customSignallingPacketLimit() int { return 1276 }

func setCustomQEXTPayload(*celt.Decoder, []byte) {}
