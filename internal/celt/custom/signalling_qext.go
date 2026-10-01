//go:build gopus_custom_modes && gopus_qext

package custom

import "github.com/thesyncim/gopus/internal/celt"

func customSignallingUsesOpusTOC(*CustomMode) bool { return true }

func customSignallingPacketLimit() int { return 3826 }

func setCustomQEXTPayload(dec *celt.Decoder, payload []byte) {
	dec.SetQEXTPayload(payload)
}
